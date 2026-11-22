package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var workflowFiles = []string{".github/workflows/build.yml", ".github/workflows/sovereign.yml"}

// A `${{ … }}` inside a run: block is pasted into the script TEXT before the
// shell parses it, so a dispatch value is code, not data. The same value in an
// env: block is a value. This reads every run: of both workflows.
var interpolated = regexp.MustCompile(`\$\{\{\s*(github\.event\.inputs|github\.event\.inputs\.[a-z_]+|inputs|matrix|github\.head_ref|github\.event\.[a-z_.]*(title|body|label|ref|name))`)

func TestNoDispatchValueIsInterpolatedIntoAScript(t *testing.T) {
	for _, f := range workflowFiles {
		for _, s := range loadWorkflow(t, f).steps() {
			if loc := interpolated.FindString(s.Run); loc != "" {
				t.Errorf("%s: step %q interpolates %q into its script; pass it through env: and read it as a variable", f, s.Name, loc)
			}
		}
	}
}

func TestEveryExtraTrustedKeysStepScopesItNotTheWorkflow(t *testing.T) {
	for _, f := range workflowFiles {
		w := loadWorkflow(t, f)
		if _, ok := w.Env["ZPKG_EXTRA_TRUSTED_KEYS"]; ok {
			t.Errorf("%s: ZPKG_EXTRA_TRUSTED_KEYS is workflow-level, so every step and every recipe sees it", f)
		}
		for name, j := range w.Jobs {
			if _, ok := j.Env["ZPKG_EXTRA_TRUSTED_KEYS"]; ok {
				t.Errorf("%s: job %s sets ZPKG_EXTRA_TRUSTED_KEYS at job level", f, name)
			}
		}
		n := 0
		for _, s := range w.steps() {
			if v, ok := s.Env["ZPKG_EXTRA_TRUSTED_KEYS"]; ok {
				n++
				if v != "${{ env.EXTRA_KEYS }}" {
					t.Errorf("%s: step %q reads the raw value %q, not the validated EXTRA_KEYS", f, s.Name, v)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no step hands ZPKG_EXTRA_TRUSTED_KEYS to zpkb", f)
		}
		if w.dispatchInput(t, "extra_trusted_keys") == nil {
			t.Errorf("%s: no extra_trusted_keys dispatch input", f)
		}
	}
}

func TestExtraTrustedKeysCrossesSudoIntoTheChroot(t *testing.T) {
	src, err := os.ReadFile("builder/sovereign-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !preserveList(t, string(src))["ZPKG_EXTRA_TRUSTED_KEYS"] {
		t.Error("ZPKG_EXTRA_TRUSTED_KEYS is not on sovereign-build.sh's --preserve-env list: zpkx inside the chroot would verify without the extra key")
	}
}

func TestBuildValidatesWithInputcheckBeforeAnythingIsExported(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	for _, job := range []string{"setup", "build"} {
		var at = -1
		for i, s := range w.Jobs[job].Steps {
			if strings.Contains(s.Run, "go run ./inputcheck") {
				at = i
				break
			}
		}
		if at < 0 {
			t.Fatalf("job %s does not run inputcheck", job)
		}
		for i, s := range w.Jobs[job].Steps[:at] {
			if strings.Contains(s.Run, "GITHUB_ENV") {
				t.Errorf("job %s: step %d %q writes $GITHUB_ENV before inputcheck has validated the inputs", job, i, s.Name)
			}
		}
	}
	// The seed registry is read from the validated environment, not from the
	// raw input, in the step that writes it to $GITHUB_ENV.
	for _, s := range w.Jobs["build"].Steps {
		if strings.Contains(s.Name, "pull-through cache") {
			if _, raw := s.Env["SEED_DIST"]; raw {
				t.Errorf("step %q takes SEED_DIST from the raw input", s.Name)
			}
		}
	}
}
