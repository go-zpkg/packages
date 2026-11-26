#!/usr/bin/env bash
# Runs builder/sovereign-build.sh for real (as root, on a Linux host with socat
# and busybox-static) against a rootfs whose zpkx/zpkb are stubs, and asserts
# what a pass may reach from inside the chroot:
#
#   pass 2 (ZPKG_TRUSTED_KEYS set, SOVEREIGN_NET_ALLOW=127.0.0.1:5111)
#     - the allowed registry answers,
#     - another host-loopback server (127.0.0.1:5998), a public address and an
#       unrouted one are all unreachable;
#   pass 2 without SOVEREIGN_NET_ALLOW is refused before anything runs;
#   pass 1 (no ZPKG_TRUSTED_KEYS) keeps the host network and reaches :5998.
set -euo pipefail

work="${1:?work directory}"
here="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$work/www" "$work/pantry" "$work/overrides"
echo allowed > "$work/www/probe"

# Two servers on the HOST's loopback. Only :5111 is ever forwarded.
(cd "$work/www" && exec python3 -m http.server 5111 --bind 127.0.0.1 >/dev/null 2>&1) &
s1=$!
(cd "$work/www" && exec python3 -m http.server 5998 --bind 127.0.0.1 >/dev/null 2>&1) &
s2=$!
trap 'kill "$s1" "$s2" 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do
  python3 -c 'import urllib.request as u; u.urlopen("http://127.0.0.1:5111/probe", timeout=1); u.urlopen("http://127.0.0.1:5998/probe", timeout=1)' 2>/dev/null && break
  sleep 0.2
done

newroot() {
  r="$work/$1"
  rm -rf "$r"
  mkdir -p "$r/bin" "$r/usr/local/bin"
  cp "$(command -v busybox)" "$r/bin/busybox"
  cat > "$r/usr/local/bin/zpkx" <<'EOF'
#!/bin/busybox sh
if [ "$1" = "--seed-digest" ]; then
  echo "sha256:$(/bin/busybox sha256sum "$2" | /bin/busybox cut -d' ' -f1)"
  exit 0
fi
while [ "$1" != "--" ]; do shift; done
shift
exec "$@"
EOF
  cat > "$r/usr/local/bin/zpkb" <<'EOF'
#!/bin/busybox sh
case " $* " in *" --expect=pass2 "*) pass2=1 ;; *) pass2=0 ;; esac
case " $* " in *" --seed-ledger-sha256 "*)
  # A seed pass: the ledger belongs to the outside, and a write from in here is refused.
  want=""; prev=""
  for a in "$@"; do [ "$prev" = --seed-ledger-sha256 ] && want="$a"; prev="$a"; done
  have="sha256:$(/bin/busybox sha256sum /seed-ledger.json | /bin/busybox cut -d' ' -f1)"
  [ "$want" = "$have" ] || { echo "SEED-FAIL: digest $want, ledger $have"; exit 21; }
  if echo forged >> /seed-ledger.json 2>/dev/null; then echo "SEED-FAIL: the ledger was written from inside"; exit 22; fi
  echo "SEED-OK"
  ;;
esac
get() { /bin/busybox wget -T 3 -qO- "$1" 2>/dev/null; }
[ "$(get http://127.0.0.1:5111/probe)" = allowed ] || { echo "NETNS-FAIL: the allowed registry did not answer"; exit 11; }
if [ "$pass2" = 1 ]; then
  for u in http://127.0.0.1:5998/probe http://1.1.1.1/ http://192.0.2.1:5111/; do
    if get "$u" >/dev/null; then echo "NETNS-FAIL: reached $u"; exit 12; fi
  done
else
  [ "$(get http://127.0.0.1:5998/probe)" = allowed ] || { echo "NETNS-FAIL: pass 1 lost the host network"; exit 13; }
fi
echo "NETNS-OK pass2=$pass2"
EOF
  chmod +x "$r/usr/local/bin/zpkx" "$r/usr/local/bin/zpkb"
}

run() { # <name> <expect> [env...]
  name="$1"; expect="$2"; shift 2
  newroot "$name"
  env SOVEREIGN_STRICT=0 SOVEREIGN_LOG="$work/$name.log" PUBLISH_TO=oci://http://127.0.0.1:5111/x "$@" \
    "$here/sovereign-build.sh" "$work/$name" "$work/pantry" "$work/overrides" "$expect"
}

# 1: pass 2, one allowed endpoint.
run pass2 --expect=pass2 ZPKG_TRUSTED_KEYS=lan-1-1:RWx SOVEREIGN_NET_ALLOW=127.0.0.1:5111
grep -q 'NETNS-OK pass2=1' "$work/pass2.log"

# 2: pass 2 with no allow-list is refused, and nothing ran.
if run refused --expect=pass2 ZPKG_TRUSTED_KEYS=lan-1-1:RWx; then
  echo "pass 2 without SOVEREIGN_NET_ALLOW was accepted"; exit 1
fi
if [ -e "$work/refused.log" ] && grep -q NETNS "$work/refused.log"; then
  echo "pass 2 without SOVEREIGN_NET_ALLOW ran"; exit 1
fi

# 3: pass 1 keeps the host network; the allow-list is refused there.
run pass1 --expect=pass1
grep -q 'NETNS-OK pass2=0' "$work/pass1.log"
if run pass1b --expect=pass1 SOVEREIGN_NET_ALLOW=127.0.0.1:5111; then
  echo "SOVEREIGN_NET_ALLOW was accepted on a pass 1"; exit 1
fi
# 4: a seed pass. The ledger is the host's file, mounted read-only; the factory
# stub is handed its digest, finds it unchanged, and cannot write to it.
printf '{"project":"llvm.org","version":"23.1.2","digest":"sha256:%064d"}\n' 0 > "$work/ledger.json"
before="$(sha256sum "$work/ledger.json")"
run seeded --expect=pass1 SOVEREIGN_SEED_FROM=oci://127.0.0.1:5111/seed SOVEREIGN_SEED_LEDGER="$work/ledger.json"
grep -q 'SEED-OK' "$work/seeded.log"
[ "$before" = "$(sha256sum "$work/ledger.json")" ] || { echo "the host ledger changed during the pass"; exit 1; }
# A pass 1 seed with the ledger missing is refused before anything runs.
if run noledger --expect=pass1 SOVEREIGN_SEED_FROM=oci://127.0.0.1:5111/seed SOVEREIGN_SEED_LEDGER="$work/absent.json"; then
  echo "a seed pass with no ledger was accepted"; exit 1
fi
echo "netns: all assertions held"
