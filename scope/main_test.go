package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/go-zpkg/artifact"
)

// ⛔ THE CLOSURE NAMES ARE PRINTED, NOT JUST COUNTED. A closure-only project
// is the one kind of scope entry nobody would think to look for, so a count
// alone sends the reader back to the tool to find out which.
func TestRunNamesTheClosureOnlyProjects(t *testing.T) {
	var buf bytes.Buffer
	err := run(&buf, []string{"linux/x86-64"}, 8, 0, from(map[string]artifact.Catalog{
		"linux/x86-64": cat(
			published("app.org", "zlib.net", "libsdl.org/SDL_image"),
			namedOnly("zlib.net"),
			namedOnly("libsdl.org/SDL_image"),
		),
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "published nowhere") {
		t.Errorf("the closure was counted and not named:\n%s", out)
	}
	// sortedKeys gives the list a stable order, so the second name follows the
	// first: a set printed in map order makes two identical runs differ.
	i, j := strings.Index(out, "libsdl.org/SDL_image"), strings.Index(out, "zlib.net")
	if i < 0 || j < 0 {
		t.Fatalf("both closure names must appear:\n%s", out)
	}
	if i > j {
		t.Errorf("closure names are not sorted:\n%s", out)
	}
}

// A measurement that could not be made must not be printed as a report: run
// returns the error before it writes a table, so nothing downstream reads a
// number that was never measured.
func TestRunPrintsNothingWhenTheMeasurementFailed(t *testing.T) {
	var buf bytes.Buffer
	err := run(&buf, []string{"linux/x86-64"}, 8, 0, from(nil))
	if err == nil {
		t.Fatal("an unreadable platform produced a report")
	}
	if buf.Len() != 0 {
		t.Errorf("a failed measurement still printed:\n%s", buf.String())
	}
}

// And the control inside scopeOf has to stop a measure() of several platforms
// too, not merely a direct call: a disagreement on the third platform must
// not be answered with the union of the first two.
func TestMeasureStopsOnAPlatformWhoseInstrumentsDisagree(t *testing.T) {
	_, err := measure([]string{"linux/x86-64", "darwin/aarch64"}, 8, from(map[string]artifact.Catalog{
		"linux/x86-64":   cat(published("a.org")),
		"darwin/aarch64": cat(published("b.org"), published("b.org")),
	}))
	if err == nil {
		t.Fatal("a platform whose instruments disagreed was folded into the union")
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Errorf("error %q does not say the two instruments disagree", err)
	}
}

// main() is the wiring, and the exit status is the only thing it says.
func TestMainReportsThroughTheExitStatus(t *testing.T) {
	oldArgs, oldExit, oldFetch := os.Args, osExit, fetchCatalog
	t.Cleanup(func() { os.Args, osExit, fetchCatalog = oldArgs, oldExit, oldFetch })

	fetchCatalog = from(map[string]artifact.Catalog{
		"linux/x86-64": cat(published("a.org"), published("b.org")),
	})
	exited := -1
	osExit = func(c int) { exited = c }

	os.Args = []string{"scope", "--platforms", "linux/x86-64"}
	main()
	if exited != -1 {
		t.Errorf("a clean run exited with %d", exited)
	}

	// The gate: a scope below the floor must leave a failing status behind.
	os.Args = []string{"scope", "--platforms", "linux/x86-64", "--at-least", "3"}
	main()
	if exited != 1 {
		t.Errorf("exit = %d, want 1 — the scope was below the floor", exited)
	}

	// An unparseable flag is a different failure from a measurement that came
	// out short, and says so.
	exited = -1
	os.Args = []string{"scope", "--no-such-flag"}
	main()
	if exited != 2 {
		t.Errorf("exit = %d, want 2 for a bad flag", exited)
	}
}

// ⛔ THE NETWORK SEAM REPORTS, IT DOES NOT RETURN AN EMPTY CATALOGUE. Both of
// its failures are reachable without a registry, and both must be errors: an
// empty Catalog with a nil error would be read as "this platform holds
// nothing", which is exactly the wrong answer about an unreachable one.
func TestFetchCatalogErrsRatherThanReturningAnEmptyCatalogue(t *testing.T) {
	old := artifact.DistBase
	t.Cleanup(func() { artifact.DistBase = old })

	artifact.DistBase = "https://example.invalid/packages" // not an oci:// ref
	if _, err := fetchCatalog("linux", "x86-64"); err == nil {
		t.Error("a base that is not an oci:// ref must be an error")
	}

	artifact.DistBase = "oci://127.0.0.1:1/nothing" // parses, refuses to connect
	if _, err := fetchCatalog("linux", "x86-64"); err == nil {
		t.Error("an unreachable registry must be an error, not an empty catalogue")
	}
}
