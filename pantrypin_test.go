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
	On   map[string]any `yaml:"on"`
	Env  map[string]any `yaml:"env"`
	Jobs map[string]struct {
		Env   map[string]any `yaml:"env"`
		Steps []wfStep       `yaml:"steps"`
	} `yaml:"jobs"`
}

type wfStep struct {
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
		var cloneSteps int
		for _, s := range w.steps() {
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
		if cloneSteps != 1 {
			t.Errorf("%s: %d steps fetch the pantry, want 1", f, cloneSteps)
		}
	}
}

// A file the build wrote and a later step uploads is not masked like the log.
// The scrub step must come after the build and before every upload of a file
// the build or the census could have written a secret into.
func TestUploadedRecordsAreScrubbedBeforeUpload(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	var steps []wfStep
	for _, j := range w.Jobs {
		if len(j.Steps) > len(steps) {
			steps = j.Steps
		}
	}
	scrubAt, buildAt := -1, -1
	for i, s := range steps {
		if strings.Contains(s.Run, "go run ./scrubsecrets") {
			scrubAt = i
			for _, want := range []string{"failures-detail.txt", "failures.txt", "tests.txt"} {
				if !strings.Contains(s.Run, want) {
					t.Errorf("the scrub step does not cover %s", want)
				}
			}
			if s.If != "always()" {
				t.Errorf("the scrub step runs only %q; it must run on a failed build too", s.If)
			}
		}
		if s.Name == "build + publish this chunk" {
			buildAt = i
		}
	}
	if scrubAt < 0 || buildAt < 0 {
		t.Fatalf("scrub step at %d, build step at %d", scrubAt, buildAt)
	}
	if scrubAt < buildAt {
		t.Error("the scrub step runs before the build")
	}
	for i, s := range steps {
		if !strings.HasPrefix(s.Uses, "actions/upload-artifact@") {
			continue
		}
		p, _ := s.With["path"].(string)
		if (strings.Contains(p, "failures") || strings.Contains(p, "tests.txt") || strings.Contains(p, "seed-provenance")) && i < scrubAt {
			t.Errorf("step %q uploads %s before the scrub step", s.Name, p)
		}
	}
}
