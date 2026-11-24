package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing is published to ghcr.io/go-pkgx any more (go-zpkg/packages#440);
// reading from it stays. Publishing requires an explicit destination: oci_to is
// a dispatch input of every publishing workflow, a blank one is refused, there
// is no default, and `inputcheck` (or sovereign.yml's own shell) refuses go-pkgx.

// publishers are the workflows that publish, and so must name their destination.
var publishers = []string{
	"build.yml", "sovereign.yml", "darwin.yml", "wasip1.yml", "wasip1-c.yml",
	"windows.yml", "windows-rust.yml", "catalogue.yml",
}

// publishes matches a line that pushes: a publish, a staged publish, a catalogue
// with --publish.
var publishes = regexp.MustCompile(`zpkb publish |zpkb publish-stage|--publish\b|zpkb factory`)

// ZPKG_DIST is what dependencies are resolved from, and go-pkgx is refused
// there too (the artifact client refuses it, so a workflow that assigned it
// would fail late, after the build started).
func TestNoWorkflowAssignsGoPkgxAsTheDistToRead(t *testing.T) {
	files, _ := filepath.Glob(".github/workflows/*.yml")
	assign := regexp.MustCompile(`ZPKG_DIST[:=]`)
	n := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(l)
			if strings.HasPrefix(s, "#") || !assign.MatchString(s) {
				continue
			}
			n++
			if strings.Contains(strings.ToLower(s), "go-pkgx") {
				t.Errorf("%s:%d: ZPKG_DIST names go-pkgx: %s", f, i+1, s)
			}
		}
	}
	if n < 4 {
		t.Errorf("only %d ZPKG_DIST assignments read: the scan is blind", n)
	}
}

func TestEveryPublishingWorkflowIsListed(t *testing.T) {
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil || len(files) < 10 {
		t.Fatalf("workflows: %v, %d files", err, len(files))
	}
	listed := map[string]bool{}
	for _, p := range publishers {
		listed[p] = true
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(l)
			if strings.HasPrefix(s, "#") || !publishes.MatchString(s) {
				continue
			}
			// reproduce.yml publishes nothing: its NO_PUBLISH build names a
			// destination that cannot resolve, and the sovereign-build.sh call
			// is not a zpkb publish line.
			if !listed[filepath.Base(f)] && strings.Contains(s, "--to") {
				t.Errorf("%s:%d publishes (%q) but is not in the publishers list, so nothing requires its destination to be explicit", f, i+1, s)
			}
		}
	}
}

func TestNoPublishingLineNamesGoPkgxOrFallsBackToADestination(t *testing.T) {
	files, _ := filepath.Glob(".github/workflows/*.yml")
	scripts, _ := filepath.Glob("builder/*.sh")
	fallback := regexp.MustCompile(`--to\s+"?\$\{[A-Z_]+:-`)
	n := 0
	for _, f := range append(files, scripts...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(l)
			if strings.HasPrefix(s, "#") || !strings.Contains(s, "--to") {
				continue
			}
			n++
			if strings.Contains(strings.ToLower(s), "go-pkgx") {
				t.Errorf("%s:%d: a destination names go-pkgx: %s", f, i+1, s)
			}
			// The one allowed fallback is build.yml's test_only sentinel,
			// which cannot resolve.
			if fallback.MatchString(s) && !strings.Contains(s, "test-only.invalid") {
				t.Errorf("%s:%d: a destination falls back to a default: %s", f, i+1, s)
			}
		}
	}
	if n < 8 {
		t.Errorf("only %d --to lines read: the scan is blind", n)
	}
}

func TestEveryPublisherRequiresAnExplicitDestination(t *testing.T) {
	for _, p := range publishers {
		f := ".github/workflows/" + p
		w := loadWorkflow(t, f)
		in := w.dispatchInput(t, "oci_to")
		if in == nil {
			t.Errorf("%s: no oci_to dispatch input", f)
			continue
		}
		if d, has := in["default"]; has && d != "" {
			t.Errorf("%s: oci_to has the default %v", f, d)
		}
		if p == "build.yml" {
			// seed_dist and test_only are the two other ways to name or
			// waive a destination, so the input itself cannot be required;
			// inputcheck refuses the blank dispatch.
			continue
		}
		if req, _ := in["required"].(bool); !req {
			t.Errorf("%s: oci_to is not required", f)
		}
	}
}

func TestEveryPublisherRefusesABlankAndGoPkgxBeforeItPublishes(t *testing.T) {
	for _, p := range publishers {
		f := ".github/workflows/" + p
		w := loadWorkflow(t, f)
		found := false
		for _, s := range w.steps() {
			switch {
			case p == "sovereign.yml":
				// Its own shell validation, which cannot call a Go program
				// before the checkout.
				found = found || (strings.Contains(s.Run, "publishing requires an explicit destination; set oci_to") &&
					strings.Contains(s.Run, "go-pkgx is refused as one"))
			default:
				found = found || (strings.Contains(s.Run, "go run ./inputcheck") &&
					s.Env["IN_OCI_TO"] == "${{ github.event.inputs.oci_to }}")
			}
		}
		if !found {
			t.Errorf("%s: no step refuses a blank or go-pkgx oci_to", f)
		}
	}
}
