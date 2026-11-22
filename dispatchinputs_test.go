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

// The production signing key is chosen by SIGN_PROD, which is decided once (in
// inputcheck, or in sovereign.yml's validate step) from the EFFECTIVE
// destination and is true only for the exact production registry. A substring
// test on one input, as the expressions used to be, signs a lookalike and
// misses a destination that arrives through another input.
func TestTheProductionKeyFollowsSignProdAndNothingElse(t *testing.T) {
	seen := 0
	for _, f := range workflowFiles {
		for _, s := range loadWorkflow(t, f).steps() {
			v, ok := s.Env["SIGNING_KEY"]
			if !ok {
				continue
			}
			seen++
			if strings.Contains(v, "contains(") || strings.Contains(v, "OCI_TO") {
				t.Errorf("%s: step %q picks the signing key from the destination text: %s", f, s.Name, v)
			}
			if !strings.Contains(v, "env.SIGN_PROD == 'true' && secrets.ZPKG_SIGNING_KEY") ||
				!strings.Contains(v, "env.SIGN_PROD != 'true' && secrets.ARTIFACT_SIGNING_KEY") {
				t.Errorf("%s: step %q does not tie each key to SIGN_PROD: %s", f, s.Name, v)
			}
		}
	}
	if seen != 3 {
		t.Errorf("found %d SIGNING_KEY steps, expected 3 (build, catalogue, sovereign)", seen)
	}
}

func TestTheSovereignValidateStepRefusesALookalikeDestination(t *testing.T) {
	var run string
	for _, s := range loadWorkflow(t, ".github/workflows/sovereign.yml").steps() {
		if strings.Contains(s.Run, "sign_prod=") {
			run = s.Run
		}
	}
	for _, want := range []string{
		"oci://ghcr.io/go-zpkg/packages)",
		`echo "SIGN_PROD=$sign_prod"`,
		"must be exactly oci://ghcr.io/go-zpkg/packages",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("sovereign.yml validate step lacks %q", want)
		}
	}
}

// Both workflows run with the signing and registry secrets, so the tools they
// install are tags: a moving branch is whatever it points at when the job starts.
func TestSecretHoldingLanesInstallTaggedTools(t *testing.T) {
	tag := regexp.MustCompile(`go install github\.com/go-zpkg/(zpkb/cmd/zpkb|zpkx|zpkm)@v[0-9]+\.[0-9]+\.[0-9]+\b`)
	for _, f := range workflowFiles {
		n := 0
		for _, s := range loadWorkflow(t, f).steps() {
			for _, line := range strings.Split(s.Run, "\n") {
				if !strings.Contains(line, "go install") || strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				if !tag.MatchString(line) {
					t.Errorf("%s: step %q installs a tool that is not pinned to a tag: %s", f, s.Name, strings.TrimSpace(line))
					continue
				}
				n++
			}
		}
		if n != 3 {
			t.Errorf("%s: %d pinned installs, expected zpkb, zpkx and zpkm", f, n)
		}
	}
}

// The loopback decision is Go (inputcheck.isLoopbackRegistry), not a shell
// glob: no run: block of either workflow may pattern-match a host.
func TestNoShellGlobDecidesWhatIsLoopback(t *testing.T) {
	for _, f := range workflowFiles {
		for _, s := range loadWorkflow(t, f).steps() {
			if strings.Contains(s.Run, "127.*") || strings.Contains(s.Run, "seed_host") {
				t.Errorf("%s: step %q decides loopback in shell", f, s.Name)
			}
		}
	}
	for _, s := range loadWorkflow(t, ".github/workflows/build.yml").steps() {
		if strings.Contains(s.Run, "ZPKG_VERIFY=0") && !strings.Contains(s.Run, `"$SEED_LOOPBACK" = true`) {
			t.Errorf("step %q turns verification off without SEED_LOOPBACK", s.Name)
		}
	}
}
