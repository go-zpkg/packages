package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow is the part of a GitHub workflow these tests read.
type workflow struct {
	On          map[string]any `yaml:"on"`
	Env         map[string]any `yaml:"env"`
	Permissions any            `yaml:"permissions"`
	Jobs        map[string]struct {
		Env         map[string]any    `yaml:"env"`
		Steps       []wfStep          `yaml:"steps"`
		RunsOn      any               `yaml:"runs-on"`
		Needs       any               `yaml:"needs"`
		If          string            `yaml:"if"`
		Permissions any               `yaml:"permissions"`
		Outputs     map[string]string `yaml:"outputs"`
	} `yaml:"jobs"`
}

type wfStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

func loadWorkflow(t *testing.T, path string) workflow {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(w.Jobs) == 0 {
		t.Fatalf("%s: no jobs parsed", path)
	}
	return w
}

func (w workflow) dispatchInput(t *testing.T, name string) map[string]any {
	t.Helper()
	d, _ := w.On["workflow_dispatch"].(map[string]any)
	in, _ := d["inputs"].(map[string]any)
	v, _ := in[name].(map[string]any)
	return v
}

func (w workflow) steps() []wfStep {
	var out []wfStep
	for _, j := range w.Jobs {
		out = append(out, j.Steps...)
	}
	return out
}

var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// The pantry's recipe scripts run during the build, so which pantry commit is a
// decision that has to be written down: a dispatch input whose default is a
// SHA, validated like overlay_ref, and no clone of a moving HEAD.
func TestThePantryIsClonedAtARecordedCommit(t *testing.T) {
	for _, f := range []string{".github/workflows/build.yml", ".github/workflows/sovereign.yml"} {
		w := loadWorkflow(t, f)
		in := w.dispatchInput(t, "pantry_ref")
		if in == nil {
			t.Errorf("%s declares no pantry_ref input", f)
			continue
		}
		def, _ := in["default"].(string)
		if !sha40.MatchString(def) {
			t.Errorf("%s: pantry_ref default %q is not a 40-hex commit", f, def)
		}
		// One clone per job: build.yml's publish job reads the pantry a second
		// time, for the catalogue of a hosted destination, in a job of its own.
		total := 0
		for name, j := range w.Jobs {
			cloneSteps := 0
			for _, s := range j.Steps {
				if strings.Contains(s.Run, "pkgxdev/pantry") {
					cloneSteps++
					if strings.Contains(s.Run, "git clone") {
						t.Errorf("%s: step %q clones the pantry's moving HEAD", f, s.Name)
					}
					if s.Env["PANTRY_REF"] != "${{ github.event.inputs.pantry_ref }}" {
						t.Errorf("%s: step %q does not read pantry_ref through env", f, s.Name)
					}
					if !strings.Contains(s.Run, "-*|*..*|*[!A-Za-z0-9._/-]*)") {
						t.Errorf("%s: step %q does not validate pantry_ref like overlay_ref", f, s.Name)
					}
					if !strings.Contains(s.Run, "checkout -q FETCH_HEAD") {
						t.Errorf("%s: step %q does not check out what it fetched", f, s.Name)
					}
				}
			}
			if cloneSteps > 1 {
				t.Errorf("%s: job %s has %d steps that fetch the pantry, want at most 1", f, name, cloneSteps)
			}
			total += cloneSteps
		}
		if total == 0 {
			t.Errorf("%s: no step fetches the pantry", f)
		}
	}
}

// A file the build wrote and a later step uploads is not masked like the log.
// There used to be a scrub step that held the signing key and the registry
// token only to find them in those files. The job that uploads them holds
// neither any more (selfhostedsecrets_test.go), so the scrub step is GONE, and
// this test fails if one comes back: a step that holds a secret in the job that
// runs recipes is exactly what that file refuses.
func TestNoScrubStepHoldsSecretsInTheBuildJob(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	for _, s := range w.Jobs["build"].Steps {
		if strings.Contains(s.Run, "scrubsecrets") {
			t.Errorf("step %q runs scrubsecrets in the build job, which must hold no secret to scrub", s.Name)
		}
	}
	if len(w.Jobs["build"].Steps) == 0 {
		t.Fatal("no build job steps parsed")
	}
}
