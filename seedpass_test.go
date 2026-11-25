package main

import (
	"os"
	"strings"
	"testing"
)

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stepOf returns the text of the workflow step whose `- name:` line contains
// name, up to the next step.
func stepOf(t *testing.T, wf, name string) string {
	t.Helper()
	i := strings.Index(wf, "- name: "+name)
	if i < 0 {
		t.Fatalf("no step %q", name)
	}
	rest := wf[i+1:]
	if j := strings.Index(rest, "\n      - "); j >= 0 {
		return rest[:j]
	}
	return rest
}

// ⛔ A SEED PASS MUST NOT BE ABLE TO LOSE ITS SEED, AND NOTHING ELSE MAY GAIN
// ONE. The seed registry reaches the build through two variables inputcheck
// derives from the `bootstrap` input, never through an input of its own.
func TestTheSeedReachesTheBuildOnlyThroughInputcheck(t *testing.T) {
	wf := readText(t, ".github/workflows/build.yml")
	build := stepOf(t, wf, "build + publish this chunk")
	for _, want := range []string{
		"BOOTSTRAP: ${{ env.BOOTSTRAP_HOST }}",
		"SOVEREIGN_SEED_FROM: ${{ env.SEED_FROM }}",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("the build step does not set %q", want)
		}
	}
	// `--bootstrap` takes the raw input nowhere: `seed` would reach it.
	if strings.Contains(build, "BOOTSTRAP: ${{ github.event.inputs.bootstrap }}") {
		t.Error("the build step hands the raw bootstrap input to --bootstrap")
	}
	if strings.Contains(wf, "seed_from") {
		t.Error("a seed_from input would make the seed registry configurable; it is a constant in inputcheck")
	}
}

func TestTheRootfsIsStagedFromTheSeedWithALedgerAndNoDist(t *testing.T) {
	wf := readText(t, ".github/workflows/build.yml")
	stage := stepOf(t, wf, "stage the sovereign rootfs")
	if strings.Count(stage, `${SEED_FROM:+--seed-from "$SEED_FROM"`) != 2 {
		t.Errorf("both the census and the staging read the seed:\n%s", stage)
	}
	if !strings.Contains(stage, `--seed-ledger "$RUNNER_TEMP/seed-ledger.json"`) {
		t.Error("the staging records no ledger")
	}
	if !strings.Contains(stage, `rm -f "$RUNNER_TEMP/seed-ledger.json"`) {
		t.Error("a stale ledger of an earlier job could survive")
	}
	if strings.Contains(stage, "--dist") {
		t.Error("--dist and --seed-from are refused together")
	}
}

// The LAN lane used to push unsigned artifacts. A seed pass's outputs are read
// back, and verified, by the next pass, so they are signed — with a key minted
// for the run, never one that exists before it.
func TestTheLanLaneSignsWithAnEphemeralKey(t *testing.T) {
	wf := readText(t, ".github/workflows/build.yml")
	pub := stepOf(t, wf, "publish to a LAN or loopback registry")
	if strings.Contains(pub, "--allow-unsigned") {
		t.Error("the LAN lane publishes unsigned again")
	}
	if !strings.Contains(pub, "go run ./seedkey") || !strings.Contains(pub, `--sign "$RUNNER_TEMP/lan-sign.key"`) {
		t.Errorf("the LAN publish does not sign with a key minted for the run:\n%s", pub)
	}
	for _, secret := range []string{"SIGNING_KEY", "OCI_PASSWORD"} {
		if strings.Contains(pub, secret) {
			t.Errorf("the LAN publish step names %s: it holds no secret", secret)
		}
	}
	cat := wf[strings.Index(wf, "- name: publish the catalogue"):]
	if i := strings.Index(cat, "\n      - "); i > 0 {
		cat = cat[:i]
	}
	if !strings.Contains(cat, `--sign "$RUNNER_TEMP/lan-sign.key"`) {
		t.Error("the LAN catalogue is unsigned")
	}
}

func TestTheSovereignScriptCarriesTheSeedAsPositionalsAndRefusesHalfOfIt(t *testing.T) {
	sh := readText(t, "builder/sovereign-build.sh")
	for _, want := range []string{
		`seedfrom="$6"; seedledger="$7"; shift 7`,
		`exec chroot "$r" /usr/local/bin/zpkx "${sd[@]}"`,
		`sd=(--seed-from "$seedfrom" --seed-ledger "$seedledger")`,
		`sf=(--seed-ledger "$seedledger")`,
		`"$seed_from" "$seed_ledger" "$@"`,
		"SOVEREIGN_SEED_LEDGER is set and no seed is",
		"the artifacts would not name what they were built with",
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("sovereign-build.sh lacks %q", want)
		}
	}
	// The ledger is NOT in /dist: that is the tree the publish step digests and
	// publish-stage reads.
	if strings.Contains(sh, `"$root/dist/seed-ledger.json"`) {
		t.Error("the ledger is inside the staged tree")
	}
}
