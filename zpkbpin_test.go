package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// ⛔⛔ A LANE PINNED BEHIND THE FACTORY PUBLISHES ARTIFACTS THE REGISTRY CANNOT
// DESCRIBE, AND NOTHING SAID SO.
//
// This is the second time a pin in this repository has drifted behind what
// the registry needs, and the first time cost a whole platform's worth of
// artefacts: three lanes — wasip1, wasip1-c, windows-c — sat at zpkb v0.59.0
// while the rest ran @main. v0.59.0 was chosen as a FLOOR, for a reason that
// had nothing to do with features: Go verifies the path a module declares,
// and every earlier tag declares `github.com/go-pkgx/bk`. Nobody revisited it
// when the floor stopped being enough.
//
// What those lanes would have published:
//
//   - no build identity at all (builds gained a hash in zpkb v0.62.0);
//   - or an identity that is not a Merkle hash of its closure (v0.63.0 fixed
//     a composed dependency path that made every edge contribute an empty
//     hash);
//   - or a signature referrer with no signer key id (v0.64.0), which a
//     verifier holding several trusted keys has to brute-force.
//
// `zpks builds` can only report such an artifact as `nothing recorded`. That is
// indistinguishable, to a reader, from an artifact we never built — which is the
// whole failure this gate exists to stop.
//
// ⛔ A REPEATED LESSON BECOMES A GATE, NOT A COMMENT. The comments beside
// those pins were already accurate and already explained the floor; being
// right in prose is what let them stay wrong in fact.
//
// ⛔⛔ THE FLOOR IS "WHAT A PUBLISHING LANE MUST BE ABLE TO DO", NOT "WHEN THE
// HASH APPEARED". It moved to v0.65.0 for a reason that is not about
// identity at all: from that tag the factory REFUSES to publish bytes that
// differ from what a hashed tag already holds. A lane pinned at v0.64.0
// computes the right identity and will still overwrite an address, rewriting
// what every existing lock, install and signature refers to — the exact
// failure the hashed layout was introduced to make impossible, through the
// one door the hash does not cover (go-zpkg/zpkb#416).
//
// So this constant rises whenever a zpkb release adds something a publishing
// lane must not be without. It is not a "latest" check and must never become
// one: a lane pinned behind for a reason of its own is fine until it is
// behind THIS.
const zpkbIdentityFloor = "v0.83.0"

// zpkbFloorReason says what THIS floor buys, and moves with it.
//
// ⛔⛔ A FIXED SENTENCE WOULD HAVE LIED THE FIRST TIME THE FLOOR MOVED. The
// message used to read "an artifact built by it carries no usable build
// identity", which was exactly right while the floor was v0.64.0 and became
// false the moment it was v0.65.0 — a lane at v0.64.0 computes a perfectly
// good identity and simply will not refuse to overwrite one. A failure that
// names the wrong cause sends the reader to fix the wrong thing, and it reads
// as authoritative because it came from a gate.
const zpkbFloorReason = "from v0.66.0 the factory REFUSES to publish an artifact that is unsigned or " +
	"unidentified, v0.67.0 added --spec-recipe-from so a lane with no recipe plan can state its " +
	"identity instead of hashing one in the shell, v0.69.0 carries the trust set as it stands " +
	"after the 2026-11-06 key rotation — a lane below that reports an artifact signed by the retired " +
	"key as signed by nobody it knows (go-zpkg/zpkb#416) — and v0.83.0 reads the environment " +
	"variables this repository now SETS: the `pantry` names were retired with the repository's own " +
	"rename, so a lane below it looks for ZPKG_PANTRY_OVERLAY, finds the empty string, and builds " +
	"with NO OVERLAY while exiting 0 (go-zpkg/zpkb#496)"

// zpkbPinRE matches an install or run of our factory at an explicit version.
// `@main` is deliberately NOT matched: this repository runs most lanes from
// main on purpose, and main is by definition at or ahead of any tag.
var zpkbPinRE = regexp.MustCompile(`github\.com/go-zpkg/zpkb/cmd/zpkb@(v[0-9][^\s"']*)`)

// ⛔ THE DENOMINATOR IS ASSERTED. A regex that stops matching reports zero
// pins and passes, which is how a scan that could not read reports a clean
// result. If the spelling of these invocations changes, this fails and says
// so rather than going quietly green.
//
// 3 → 4 with the `lint --overrides` step of ci.yml ("no standalone recipe
// silently retires a live override"), which reads recipes and builds nothing:
// it is pinned at v0.91.0, the first tag whose lint knows --overrides, and
// clears the floor above all the same.
//
// 4 → 5 with the `--check-order` step (go-zpkg/packages#480): it walks zpkb's
// own closure, so it is pinned at v0.97.0 and a zpkb change cannot redden an
// unrelated pull request. It builds nothing and clears the floor all the same.
//
// 5 → 7 with the two lanes that hold the signing and registry secrets,
// build.yml and sovereign.yml, which installed zpkb @main (go-zpkg/packages#440).
//
// 7 → 9 with the `publish` jobs of build.yml and sovereign.yml (secaudit
// finding 14): the signing key now lives only there, on a GitHub-hosted runner,
// and each installs zpkb at the same tag as the build job it publishes for.
const zpkbPinsExpected = 9

func TestEveryPinnedFactoryCanRecordABuildIdentity(t *testing.T) {
	dir := filepath.Join(".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range zpkbPinRE.FindAllStringSubmatch(string(body), -1) {
			found++
			got := m[1]
			if !semver.IsValid(got) {
				t.Errorf("%s pins zpkb at %q, which is not a version", path, got)
				continue
			}
			if semver.Compare(got, zpkbIdentityFloor) < 0 {
				t.Errorf("%s pins zpkb at %s, below %s — %s", path, got, zpkbIdentityFloor, zpkbFloorReason)
			}
		}
	}

	if found != zpkbPinsExpected {
		t.Errorf("found %d pinned zpkb invocation(s), expected %d — if a lane was added, removed or respelt, "+
			"move zpkbPinsExpected deliberately; a count that drifts on its own is how this gate stops looking",
			found, zpkbPinsExpected)
	}
}

// The module's own dependency on zpkb must clear the same floor: the tools in
// this repository link it, and one that computed an identity a different way
// from the factory would be a second definition of the same thing.
func TestTheModuleDependsOnAFactoryThatRecordsIdentities(t *testing.T) {
	body, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`github\.com/go-zpkg/zpkb (v[^\s]+)`)
	m := re.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatal("go.mod does not require github.com/go-zpkg/zpkb — if that is deliberate, this test should go with it")
	}
	if semver.Compare(m[1], zpkbIdentityFloor) < 0 {
		t.Errorf("go.mod requires zpkb %s, below %s — %s", m[1], zpkbIdentityFloor, zpkbFloorReason)
	}
}
