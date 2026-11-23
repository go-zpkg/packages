package main

import (
	"os"
	"strings"
	"testing"
)

// ⛔ overlay_ref HAS TO ARRIVE AS THE ARGUMENT THE INNER SCRIPT READS.
//
// builder/sovereign-build.sh takes the overlay from $SOVEREIGN_OVERLAY, a
// directory, and hands it to `zpkb factory --overlay`. The sovereign workflow
// accepted an overlay_ref input and turned it into a URL (ZPKG_RECIPES_OVERLAY)
// only, so the script's overlay was EMPTY while overlay_ref was set — the smoke
// run logged `+ overlay=` and the closure was not widened by the overlay.
//
// A workflow cannot be executed here, so the chain is read: the input is
// validated and exported, a step clones go-zpkg/recipes at that ref into
// ./overlay, and the step that runs sovereign-build.sh points
// SOVEREIGN_OVERLAY at it.
func TestSovereignOverlayRefReachesTheBuildScript(t *testing.T) {
	const f = ".github/workflows/sovereign.yml"
	w := loadWorkflow(t, f)
	if w.dispatchInput(t, "overlay_ref") == nil {
		t.Fatalf("%s declares no overlay_ref input; this test guards its use", f)
	}
	jobsRunning := 0
	for _, j := range w.Jobs {
		exported, cloned, ran := false, false, 0
		for _, s := range j.Steps {
			if strings.Contains(s.Run, `echo "OVERLAY_REF=$ref"`) {
				exported = true
			}
			if strings.Contains(s.Run, "git -C overlay fetch") &&
				strings.Contains(s.Run, "https://github.com/go-zpkg/recipes") &&
				strings.Contains(s.Run, `"$ref"`) &&
				strings.Contains(s.Env["OVERLAY_REF"], "env.OVERLAY_REF") {
				if !exported {
					t.Errorf("%s: step %q reads OVERLAY_REF before any step exports it", f, s.Name)
				}
				cloned = true
			}
			if !strings.Contains(s.Run, "builder/sovereign-build.sh") {
				continue
			}
			ran++
			if !cloned {
				t.Errorf("%s: step %q runs sovereign-build.sh before the overlay is cloned at overlay_ref", f, s.Name)
			}
			if !strings.Contains(s.Run, `SOVEREIGN_OVERLAY="$PWD/overlay"`) {
				t.Errorf("%s: step %q runs sovereign-build.sh with no SOVEREIGN_OVERLAY, so its overlay argument is EMPTY while overlay_ref is set", f, s.Name)
			}
		}
		// The publish job runs no recipe and has no build script step.
		if ran > 0 {
			jobsRunning++
		}
	}
	if jobsRunning == 0 {
		t.Errorf("%s: no job has a step running builder/sovereign-build.sh", f)
	}
}

// The script must honour what the workflow sets: SOVEREIGN_OVERLAY is read, and a
// non-empty one becomes `--overlay /overlay` inside the chroot.
func TestBuildScriptTurnsSovereignOverlayIntoTheOverlayFlag(t *testing.T) {
	b, err := os.ReadFile("builder/sovereign-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`overlay="${SOVEREIGN_OVERLAY:-}"`,
		`if [ -n "$overlay" ]; then`,
		`ov=(--overlay /overlay)`,
		`"$root" "$pantry" "$overrides" "$overlay" "$advisories"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("sovereign-build.sh lost %q", want)
		}
	}
}

// ⛔ NO OCI_* VARIABLE REACHES THE CHROOT. A username without its password is a
// half credential: Basic auth with an empty password, which ghcr.io refuses
// (403) on a pull it serves anonymously. OCI_PASSWORD was taken out of the
// chroot when build and publish were split, OCI_USERNAME was left, and zpkx
// there could no longer list the tags of a public package.
func TestNoOCIVariableReachesTheChroot(t *testing.T) {
	b, err := os.ReadFile("builder/sovereign-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	pl := preserveList(t, string(b))
	for k := range pl {
		if strings.HasPrefix(k, "OCI_") {
			t.Errorf("sovereign-build.sh carries %s across sudo into the chroot", k)
		}
	}
	ran := 0
	for _, f := range []string{".github/workflows/build.yml", ".github/workflows/sovereign.yml"} {
		for _, s := range loadWorkflow(t, f).steps() {
			if !strings.Contains(s.Run, "builder/sovereign-build.sh") {
				continue
			}
			ran++
			for k, v := range s.Env {
				if strings.HasPrefix(k, "OCI_") {
					t.Errorf("%s: step %q runs the chroot build and sets %s=%s", f, s.Name, k, v)
				}
			}
		}
	}
	if ran < 2 {
		t.Errorf("found %d steps running sovereign-build.sh, expected at least 2", ran)
	}
}

// ⛔ build.yml CLONES THE OVERLAY AT overlay_ref. The step "clone the pantry
// overlay" was `git clone --depth 1 .../recipes overlay`: a moving main that
// ignored overlay_ref, while OVERLAY_URL (another consumer of the same input)
// named the ref. A dispatch that pinned a ref built from whatever main was.
func TestBuildOverlayIsClonedAtOverlayRef(t *testing.T) {
	const f = ".github/workflows/build.yml"
	found := 0
	// The build job clones once; the publish job clones once more for the
	// catalogue of a hosted destination, so the count is per job.
	perJob := map[string]int{}
	w := loadWorkflow(t, f)
	for jn, j := range w.Jobs {
		for _, s := range j.Steps {
			if !strings.Contains(s.Run, "go-zpkg/recipes") || !strings.Contains(s.Run, "overlay") {
				continue
			}
			perJob[jn]++
			if strings.Contains(s.Run, "git clone") {
				t.Errorf("%s: step %q clones the overlay with git clone, which ignores overlay_ref", f, s.Name)
			}
			if !strings.Contains(s.Run, "git -C overlay fetch") || !strings.Contains(s.Run, `"$ref"`) {
				t.Errorf("%s: step %q does not fetch the overlay at \"$ref\"", f, s.Name)
			}
			if !strings.Contains(s.Env["IN_OVERLAY_REF"], "github.event.inputs.overlay_ref") {
				t.Errorf("%s: step %q does not take the ref from the overlay_ref input", f, s.Name)
			}
			if !strings.Contains(s.Run, `:-main}`) {
				t.Errorf("%s: step %q has no main default for an empty overlay_ref", f, s.Name)
			}
			found++
		}
	}
	for jn, n := range perJob {
		if n > 1 {
			t.Errorf("%s: job %s has %d overlay clone steps, expected at most 1", f, jn, n)
		}
	}
	if perJob["build"] != 1 {
		t.Errorf("%s: build job has %d overlay clone steps, expected 1", f, perJob["build"])
	}
	if found < 1 {
		t.Errorf("%s: found no overlay clone step", f)
	}
}
