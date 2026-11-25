#!/usr/bin/env bash
# Run `zpkb factory` inside a staged sovereign rootfs, so neither the host's libc
# nor its compiler takes part in the build.
#
# Usage:  builder/sovereign-build.sh <rootfs> <pantry> <overrides> [zpkb args...]
#
# $SOVEREIGN_ADVISORIES, when set, is the advisory tree bound in at /advisories
# and passed as --advisories, so each published artifact carries the OpenVEX
# document that says what is known about it.
#
# $SOVEREIGN_OVERLAY, when set, is a go-zpkg/recipes checkout bound in as
# /overlay and passed to `zpkb factory --overlay`. It does not change what is
# BUILT — the factory still compiles the pantry's recipe — it widens the
# CLOSURE, so a dependency only our overlay declares is built rather than left
# for consumers to resolve and not find (go-zpkg/zpkb#224).
#
# The rootfs is what `zpkb builder --container` staged. Everything the build needs
# beyond it is arranged here, and each piece is here because its absence was
# measured:
#
#   /proc      zpkb dies at `readlink /proc/self/exe: no such file or directory`.
#              A container runtime always mounts one.
#   /dev/*     bound, not mknod'd: an unprivileged Incus container has no
#              CAP_MKNOD and mknod answers "Operation not permitted".
#   /tmp       pkgm stages blobs there; without it, "temp file: no such file or
#              directory" AFTER the registry has already answered.
#   resolv.conf  the resolver a runtime injects too.
#
# The environment is builder/Containerfile's ENV, and zpkb is invoked through
# zpkx with its ENTRYPOINT's argv, so a caller cannot drift from what the
# image promises. ZPKG_DIR matters more than it looks, and its VALUE matters as
# much as setting it at all: zpkx reads the store from it and falls back to
# $HOME/.zpkg when it is unset (go-zpkg/artifact zpkg.go:381-385), which
# re-downloads a toolchain the staged tree already holds and bakes
# PT_INTERP=/root/.zpkg/... into every artifact — an absolute path no consumer
# has. Pointing it at a directory where the builder did NOT stage reaches that
# same end by another road, which is why this file and zpkb's
# cmd/zpkb/builder.go:264 have to agree that the guest store is /zpkg.
set -euo pipefail

root="${1:?rootfs}"; pantry="${2:?pantry}"; overrides="${3:?overrides}"; shift 3
overlay="${SOVEREIGN_OVERLAY:-}"
# $SOVEREIGN_ADVISORIES is the advisory tree whose records become each artifact's
# OpenVEX referrer. Bound in like the overlay, and for the reason the LOCK
# paragraph below gives in full: a path from the workspace means nothing inside
# the rootfs. Passing `--advisories advisories` without the mount would make zpkb
# refuse with "no advisories.yaml under advisories" — which reads as "the tree
# is empty" and means "the tree is on the other side of a namespace".
advisories="${SOVEREIGN_ADVISORIES:-}"

mkdir -p "$root/pantry" "$root/overrides" "$root/overlay" "$root/advisories" "$root/dist" "$root/dev" "$root/proc" "$root/tmp" "$root/etc"
chmod 1777 "$root/tmp"
cp /etc/resolv.conf "$root/etc/resolv.conf"

log="${SOVEREIGN_LOG:-${RUNNER_TEMP:-/tmp}/zpkb.log}"

SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo"; fi

# A SEED PASS. $SOVEREIGN_SEED_FROM is the registry the build environment is
# taken from (build.yml sets it for bootstrap=seed and for nothing else), and
# $SOVEREIGN_SEED_LEDGER the ledger `zpkb builder` wrote while staging this
# rootfs from it. Both or neither: a seed with no ledger would build artifacts
# that do not say what they were built with, and a ledger with no seed is a
# claim about nothing.
#
# The ledger is copied INTO the rootfs (/seed-ledger.json, not /dist, which is
# the staged tree the publish step digests): zpkx inside the chroot appends
# what it installs from the seed, and the factory reads the whole of it into the
# stage plan, where `zpkb publish-stage` signs it into every artifact's
# provenance. Passed as POSITIONALS to the inner script, not as environment, so
# the preserve-env list below stays the list of what the factory reads.
seed_from="${SOVEREIGN_SEED_FROM:-}"
seed_ledger=""
if [ -n "$seed_from" ] || [ -n "${SOVEREIGN_SEED_LEDGER:-}" ]; then
  [ -n "$seed_from" ] || { echo "::error::SOVEREIGN_SEED_LEDGER is set and no seed is: refusing to build a ledger of nothing"; exit 1; }
  [ -s "${SOVEREIGN_SEED_LEDGER:-}" ] || { echo "::error::seed ${seed_from} is in force and the ledger '${SOVEREIGN_SEED_LEDGER:-}' is missing or empty: the artifacts would not name what they were built with"; exit 1; }
  $SUDO cp "$SOVEREIGN_SEED_LEDGER" "$root/seed-ledger.json"
  seed_ledger=/seed-ledger.json
fi

# THE TRUST OF A RUN THAT NAMES A KEY IS THAT KEY, NOT THE DEFAULT PLUS IT. The
# second pass reads only what the first pass built, signed with the first pass's
# ephemeral key, so it trusts that key and no other: ZPKG_TRUSTED_KEYS REPLACES
# the compiled-in set (ZPKG_EXTRA_TRUSTED_KEYS would add to it, and a second pass
# that still trusted the production key would accept what that key signed).
# A seed pass takes no extra key at all (inputcheck refuses it): the seed is
# verified against the compiled-in keys alone.
# The workflow hands the key in ZPKG_TRUSTED_KEYS, which REPLACES the default
# set. The ADDITIVE ZPKG_EXTRA_TRUSTED_KEYS is refused outright: a pass that
# trusts the default set plus a key is a pass that accepts what the default
# keys sign, and a seed pass is verified against the compiled-in keys alone.
if [ -n "${ZPKG_EXTRA_TRUSTED_KEYS:-}" ]; then
  echo "::error::ZPKG_EXTRA_TRUSTED_KEYS is set: trust is exclusive (ZPKG_TRUSTED_KEYS), never additive"; exit 1
fi
[ -z "$seed_from" ] || [ -z "${ZPKG_TRUSTED_KEYS:-}" ] || { echo "::error::a seed pass is verified against the compiled-in keys only, and ZPKG_TRUSTED_KEYS is set"; exit 1; }

# ZPKG_VERIFY travels with ZPKG_DIST, and the two are one decision.
#
# A seed registry is a throwaway on loopback and its artifacts are unsigned, so
# build.yml turns verification off for it. That is set in the JOB's
# environment — and the build happens on the far side of a `sudo … chroot`,
# which carries only what this list names. ZPKG_DIST was here; ZPKG_VERIFY
# was not.
#
# So staging, which runs OUTSIDE the chroot, saw the variable and worked,
# while every build INSIDE it verified again and died on the first dependency
# it had to install:
#
#   pkgx: verify zlib.net v1.3.2 (linux/s390x): artifact: … is unsigned
#   zpkb: the tool environment failed: /usr/local/bin/zpkx +…
#
# Measured on the first full second-generation run (2026-10-04): 2 of 78
# built. The two that passed, lz4.org and tukaani.org/xz, are the only ones
# in the order with NO build dependencies — so they needed no eval, and the
# boundary never bit them. A variable set on one side of a boundary and not
# carried across is the same defect as a fact encoded twice.
#
# A LOCK is a FILE, and a path from the workspace means nothing inside the
# rootfs. It is copied in and $LOCK rewritten to where it landed, before the
# namespace exists — the same class of defect the paragraph above is about:
# "a variable set on one side of a boundary and not carried across". Here it
# would have been carried across and pointed at nothing, which reads as a
# missing lock rather than as a missing mount.
if [ -n "${LOCK:-}" ]; then
  [ -r "$LOCK" ] || { echo "::error::no lock at $LOCK"; exit 1; }
  echo "sovereign build locked by $LOCK ($(grep -c '= { version' "$LOCK") pin(s))"
  $SUDO cp "$LOCK" "$root/lock.hcl"
  LOCK=/lock.hcl
  export LOCK
fi

# A SECOND PASS HAS NO NETWORK BUT THE REGISTRY IT READS. A pass that trusts a
# pass-1 key (ZPKG_TRUSTED_KEYS non-empty) runs recipe code that a pass-1 build
# produced the tools for; it must not be able to reach anything else. It gets a
# NEW network namespace (unshare --net) with a loopback and nothing more, and
# $SOVEREIGN_NET_ALLOW (host:port,host:port — inputcheck's NET_ALLOW, LAN or
# loopback IPv4 literals only) is reached through socat: a unix socket under
# the chroot, outside, forwards to the registry; inside the namespace a socat
# listener on the same address and port forwards to that socket. Pass 1 and
# every other pass keep the host's network.
net_allow="${SOVEREIGN_NET_ALLOW:-}"
netflag=""
nf_pids=""
if [ -n "${ZPKG_TRUSTED_KEYS:-}" ]; then
  [ -n "$net_allow" ] || { echo "::error::ZPKG_TRUSTED_KEYS is set (a second pass) and SOVEREIGN_NET_ALLOW is not: refusing to run recipe code with the host's network"; exit 1; }
  command -v socat >/dev/null || { echo "::error::a second pass needs socat to reach its registry from a network namespace"; exit 1; }
  netflag="--net"
  $SUDO mkdir -p "$root/nf"
  i=0
  for ep in $(echo "$net_allow" | tr ',' ' '); do
    i=$((i+1))
    $SUDO socat "UNIX-LISTEN:$root/nf/$i.sock,fork,mode=0600" "TCP:$ep" &
    nf_pids="$nf_pids $!"
    # The inner listener connects lazily, but a socket that does not exist yet
    # would make the first connection fail: wait for it.
    for _ in $(seq 1 100); do
      $SUDO test -S "$root/nf/$i.sock" && break
      sleep 0.1
    done
    $SUDO test -S "$root/nf/$i.sock" || { echo "::error::the forward to $ep did not come up"; exit 1; }
  done
elif [ -n "$net_allow" ]; then
  echo "::error::SOVEREIGN_NET_ALLOW is set and this is not a second pass: nothing would use it"; exit 1
fi
stop_forwards() { for p in $nf_pids; do $SUDO kill "$p" 2>/dev/null || true; done; }
trap stop_forwards EXIT

# --preserve-env, never a command line: the token and the signing key must not
# become argv anybody can read out of /proc.
#
# ⛔ IT IS SUDO'S FLAG, SO IT GOES WHEN SUDO DOES. --preserve-env exists to
# carry the caller's environment ACROSS a privilege change; already root,
# there is no change and the variables are simply there. Left in, the line
# became `--preserve-env=… unshare …` and the shell ran the FLAG as a
# command:
#
#   builder/sovereign-build.sh: line 94: --preserve-env=OCI_USERNAME,…: command not found
#   Process completed with exit code 127
#
# Measured 2026-10-25 on the `ubuntu-24.04-arm` lane, where the job runs as
# root inside debian:stable-slim. The self-hosted pool never hit it because
# its runner user is unprivileged, so $SUDO is set there — which is why a
# path that cannot work has been in this file unnoticed.
PRESERVE=""
# ⛔⛔ A VARIABLE MISSING FROM THIS LIST IS SILENTLY DROPPED, AND NO_PUBLISH IS
# THE ONE WHERE THAT COSTS SOMETHING IRREVERSIBLE. sudo clears the environment
# except what --preserve-env names; a job that sets NO_PUBLISH=1 and is not
# listed here does not fail, it PUBLISHES. Every other name on this line
# changes what gets built; this one changes whether the result is pushed.
#
# ⛔⛔ NEITHER OCI_PASSWORD NOR SIGNING_KEY IS ON THIS LIST, AND MUST NOT BE. This
# list is what the process running RECIPE CODE can read, and a recipe is
# third-party shell. The build is `zpkb factory --stage-only`: it holds no key and
# no token. What it staged is signed and pushed afterwards by `zpkb publish-stage`,
# on the HOST side of the chroot, which runs no recipe (go-zpkg/zpkb publish-stage;
# secaudit finding 2). `env -u` below is for the root case, where there is no sudo
# to clear the environment and the secrets would otherwise be inherited.
#
# ⛔⛔ NO OCI_* NAME IS ON IT AT ALL, THE USERNAME INCLUDED. OCI_USERNAME alone
# is a half credential: zpkx in the chroot sent Basic auth with an EMPTY
# password and ghcr.io answered 403 to a pull it serves anonymously (smoke run
# 45059582252: `zpkx: GET …/gnu.org/coreutils/tags/list: … 403: denied`). It
# stayed here when OCI_PASSWORD was taken out. The destination travels as
# PUBLISH_TO, so OCI_TO is not needed either.
# STAGE_ONLY is here because the factory reads it and the guard test
# (preserveenv_test.go) holds every variable the factory reads to this list.
[ -n "$SUDO" ] && PRESERVE="--preserve-env=STAGE_ONLY,RECIPES,LOCK,FORCE,JOBS,QEMU_RESERVED_VA,MAX_VERSIONS,NO_CLOSURE,NO_PUBLISH,PUBLISH_TO,PLATFORM,ZPKG_DIR,HOME,ZPKG_DIST,ZPKG_CACHE,ZPKG_VERIFY,ZPKG_RECIPES_OVERLAY,VERSIONS,COMPRESS,KEEP_BUILD_TREES,PIN_TO_DEPENDENTS,REBUILD,TEST_ONLY,SOURCE_MIRROR,ADVISORIES_DIR,ZPKG_TRUSTED_KEYS"
# shellcheck disable=SC2016  # the inner script is deliberately literal: it runs
# in the new namespace, where these variables are the ones that matter.
# shellcheck disable=SC2086  # $PRESERVE is one flag or nothing, and must not
# become an empty argument when it is nothing.
$SUDO $PRESERVE \
  env -u SIGNING_KEY -u OCI_PASSWORD \
  unshare --mount --pid --fork $netflag "$(command -v bash)" -euxc '
    r="$1"; pantry="$2"; overrides="$3"; overlay="$4"; advisories="$5"; seedfrom="$6"; seedledger="$7"; netallow="$8"; shift 8
    if [ -n "$netallow" ]; then
      ip link set lo up
      i=0
      for ep in $(echo "$netallow" | tr "," " "); do
        i=$((i+1)); host="${ep%:*}"; port="${ep##*:}"
        case "$host" in 127.*) ;; *) ip addr add "$host/32" dev lo ;; esac
        socat "TCP-LISTEN:$port,bind=$host,fork,reuseaddr" "UNIX-CONNECT:$r/nf/$i.sock" &
      done
    fi
    for d in null zero full random urandom tty; do
      [ -e "$r/dev/$d" ] || : > "$r/dev/$d"
      mount --bind "/dev/$d" "$r/dev/$d"
    done
    mount -t proc proc "$r/proc"
    mount --bind "$pantry" "$r/pantry"
    mount --bind "$overrides" "$r/overrides"
    ov=()
    if [ -n "$overlay" ]; then
      mount --bind "$overlay" "$r/overlay"
      ov=(--overlay /overlay)
    fi
    ad=()
    if [ -n "$advisories" ]; then
      mount --bind "$advisories" "$r/advisories"
      ad=(--advisories /advisories)
    fi
    sd=(); sf=()
    if [ -n "$seedfrom" ]; then
      sd=(--seed-from "$seedfrom" --seed-ledger "$seedledger")
      sf=(--seed-ledger "$seedledger")
    fi
    exec chroot "$r" /usr/local/bin/zpkx "${sd[@]}" \
      +gnu.org/glibc +gnu.org/coreutils +gnu.org/make +llvm.org \
      -- /usr/local/bin/zpkb factory --stage-only "${sf[@]}" \
           --recipe-dir /pantry --overrides /overrides --libc zpkx "${ov[@]}" "${ad[@]}" \
           --to "${PUBLISH_TO:?publishing requires an explicit destination}" --artifacts /dist "$@"
  ' bash "$root" "$pantry" "$overrides" "$overlay" "$advisories" "$seed_from" "$seed_ledger" "$net_allow" "$@" 2>&1 | tee "$log"

# This script PUBLISHES NOTHING. It staged artifacts and a publish-plan.json under
# $root/dist; the caller signs and pushes them in a LATER step that holds the
# secrets (`zpkb publish-stage`), so this step never had them. Files written by
# root in the chroot are made readable for that step.
$SUDO chmod -R a+rX "$root/dist"

# `zpkb factory` is best-effort: it exits 0 with a per-recipe tally. That is right
# for a 50-project chunk and useless as a signal on its own, so read the tally.
# Both directions matter — the first green run of this path reported
# "0 built, 15 skipped, 0 failed": every step passed and nothing was compiled.
# SOVEREIGN_STRICT=1 (the default) is the PROOF posture: one named recipe, and
# the job means nothing unless it compiled and nothing failed.
#
# A chunk is the opposite and must set 0. Fifty projects always contain some
# that genuinely do not build — the tally and the failure artifacts ARE the
# result — and failing the job on that buries what did build under a red mark.
if [ "${SOVEREIGN_STRICT:-1}" != 1 ]; then
  exit 0
fi
if grep -qE '=== summary .*: [0-9]+ built, [0-9]+ skipped, [1-9][0-9]* failed ===' "$log"; then
  echo "::error::a build failed inside the sovereign rootfs"
  exit 1
fi
if ! grep -qE '=== summary .*: [1-9][0-9]* built,' "$log"; then
  echo "::error::nothing was built — a run that compiles nothing proves nothing"
  exit 1
fi
