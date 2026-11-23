package main

import (
	"strings"
	"testing"
)

// ⛔ TRUST FOR THE SOURCE ONLY. A promotion into go-zpkg must verify a seed
// signed with the seed key without widening what anything else trusts, so the
// key list has exactly one reader: the build job's `zpkb factory` command.

func mentionsSourceKeys(s wfStep) bool {
	hit := func(v string) bool {
		return strings.Contains(v, "SOURCE_KEYS") || strings.Contains(v, "source_trusted_keys") || strings.Contains(v, "--source-trust")
	}
	if hit(s.Run) || hit(s.If) {
		return true
	}
	for k, v := range s.Env {
		if hit(k) || hit(v) {
			return true
		}
	}
	return false
}

func TestSourceKeysNeverReachThePublishJobOrAnyTrustVariable(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	if w.dispatchInput(t, "source_trusted_keys") == nil {
		t.Fatal("no source_trusted_keys dispatch input")
	}
	pub, ok := w.Jobs["publish"]
	if !ok || len(pub.Steps) == 0 {
		t.Fatal("no publish job to check")
	}
	for k, v := range pub.Env {
		if s, _ := v.(string); strings.Contains(k+s, "SOURCE_KEYS") {
			t.Errorf("the publish job sets %s at job level", k)
		}
	}
	for _, s := range pub.Steps {
		if mentionsSourceKeys(s) {
			t.Errorf("publish job step %q reads the source keys; the publish job re-signs and must not even hold them", s.Name)
		}
	}
	for name, j := range w.Jobs {
		for _, s := range j.Steps {
			for k, v := range s.Env {
				if strings.Contains(v, "SOURCE_KEYS") && (strings.HasPrefix(k, "ZPKG_") || strings.Contains(k, "TRUST")) {
					t.Errorf("job %s step %q hands the source keys to %s: that is trust for an install or a test, not for the source", name, s.Name, k)
				}
			}
		}
	}
}

func TestOnlyTheMirrorCommandReadsTheSourceKeys(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	var readers []string
	for name, j := range w.Jobs {
		for _, s := range j.Steps {
			if strings.Contains(s.Run, "--source-trust") {
				readers = append(readers, name+": "+s.Name)
				if name != "build" || !strings.Contains(s.Run, "zpkb factory --stage-only") {
					t.Errorf("--source-trust is passed outside the build job's `zpkb factory --stage-only`: %s: %s", name, s.Name)
				}
				if s.Env["SOURCE_KEYS"] != "${{ env.SOURCE_KEYS }}" {
					t.Errorf("step %q reads %q, not the validated SOURCE_KEYS", s.Name, s.Env["SOURCE_KEYS"])
				}
			}
			// Raw input: only a validator may read it.
			if strings.Contains(s.Env["IN_SOURCE_KEYS"], "source_trusted_keys") && !strings.Contains(s.Run, "go run ./inputcheck") {
				t.Errorf("job %s step %q reads the raw source_trusted_keys but is not a validator", name, s.Name)
			}
		}
	}
	if len(readers) != 1 {
		t.Errorf("want exactly one step passing --source-trust, got %v", readers)
	}
}

// ⛔ promo-r2: after the build step failed, "publish the catalogue" still pushed
// an UNSIGNED catalogue of 2057 projects, because it was always().
func TestACatalogueIsNeverPublishedAfterAFailureOrWithoutAPublish(t *testing.T) {
	for _, f := range allWorkflowFiles(t) {
		w := loadWorkflow(t, f)
		for name, j := range w.Jobs {
			published := map[string]bool{}
			for _, s := range j.Steps {
				if s.ID != "" && strings.Contains(s.Run, "zpkb publish-stage") {
					published[s.ID] = true
				}
				if !strings.Contains(s.Run, "zpkb catalog") || !strings.Contains(s.Run, "--publish") {
					continue
				}
				if strings.Contains(s.If, "always()") || strings.Contains(s.If, "failure()") || strings.Contains(s.If, "cancelled()") {
					t.Errorf("%s: job %s step %q publishes a catalogue and runs after a failure: %q", f, name, s.Name, s.If)
				}
				if name != "publish" {
					// The self-hosted copy has no staged-digest guard of its own:
					// it must name the publish step it follows, and succeed.
					ok := strings.Contains(s.If, "success()")
					at := false
					for id := range published {
						if strings.Contains(s.If, "steps."+id+".outcome == 'success'") {
							at = true
						}
					}
					if !ok || !at {
						t.Errorf("%s: job %s step %q must require success() and the outcome of the step that published: %q", f, name, s.Name, s.If)
					}
				}
			}
		}
	}
}

func TestTheCatalogueGuardHasItsPublishStep(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	var found bool
	for _, s := range w.Jobs["build"].Steps {
		if s.ID == "lan_publish" {
			found = strings.Contains(s.Run, "zpkb publish-stage")
		}
	}
	if !found {
		t.Error("the build job has no lan_publish step running `zpkb publish-stage`: the catalogue's guard names a step that does not exist")
	}
}
