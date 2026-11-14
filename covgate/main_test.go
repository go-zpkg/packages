package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// profile builds a coverage profile from records written as they appear on
// disk, so a fixture reads the way the real file does.
func profile(records ...string) string {
	return "mode: atomic\n" + strings.Join(records, "\n") + "\n"
}

func TestAProfileWhoseEveryBlockWasEnteredPasses(t *testing.T) {
	var buf bytes.Buffer
	err := run(strings.NewReader(profile(
		"github.com/o/r/a/main.go:10.2,12.3 2 1",
		"github.com/o/r/b/main.go:4.11,6.2 1 7",
	)), &buf, 40)
	if err != nil {
		t.Fatalf("a fully covered profile was refused: %v", err)
	}
	if !strings.Contains(buf.String(), "100.0000 %") {
		t.Errorf("the figure is not reported:\n%s", buf.String())
	}
}

// ⛔ THE BLOCK THIS GATE EXISTS FOR. A span inside a package-level
// `var f = func(){…}` encloses no function declaration, so `go tool cover
// -func` drops it from its per-function list AND from its total — which is
// how 12 statements sat uncovered under a green "100.0%" until #458. This
// parser never reads a function name, so the span counts like any other.
func TestABlockBelongingToNoFunctionDeclarationStillCounts(t *testing.T) {
	var buf bytes.Buffer
	err := run(strings.NewReader(profile(
		"github.com/o/r/npmtag/main.go:66.30,68.16 2 5", // inside func run
		"github.com/o/r/npmtag/main.go:53.2,55.16 3 0",  // inside var httpGet = func(…)
	)), &buf, 40)
	if err == nil {
		t.Fatal("a block that no declaration encloses was not counted")
	}
	out := buf.String()
	if !strings.Contains(out, "npmtag/main.go:53.2,55.16") {
		t.Errorf("the uncovered block is not named:\n%s", out)
	}
	if !strings.Contains(err.Error(), "3 statement(s)") {
		t.Errorf("error %q does not say how much is uncovered", err)
	}
}

// ⛔ AN EMPTY PROFILE IS NOT FULL COVERAGE — it is a run that compiled
// nothing, a path typo, or a crashed run, each of which would otherwise be
// the repository's best result. This is the refusal a pipeline cannot make:
// `grep` finding no uncovered block and `grep` reading no file both print
// nothing and exit 0.
func TestAnEmptyOrStatementlessProfileIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, in string
	}{
		{"nothing at all", ""},
		{"a header and no records", "mode: atomic\n"},
		{"blocks holding no statements", profile("a/main.go:1.1,2.2 0 1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := run(strings.NewReader(tc.in), &buf, 40); err == nil {
				t.Error("read as 100 % coverage")
			}
		})
	}
}

// A profile whose format moved must stop the gate, not be skipped past: an
// unreadable record silently dropped is an uncovered block silently dropped,
// which is the defect this command was written to end.
func TestAnUnreadableRecordIsRefusedAndLocated(t *testing.T) {
	for _, tc := range []struct{ name, record, want string }{
		{"too few fields", "a/main.go:1.1,2.2 3", "not a profile record"},
		{"statement count", "a/main.go:1.1,2.2 x 1", "unreadable statement count"},
		{"hit count", "a/main.go:1.1,2.2 3 x", "unreadable hit count"},
		{"negative", "a/main.go:1.1,2.2 3 -1", "negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := func() error {
				var buf bytes.Buffer
				return run(strings.NewReader(profile(tc.record)), &buf, 40)
			}()
			if err == nil {
				t.Fatalf("%q was accepted", tc.record)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error %q does not locate the record", err)
			}
		})
	}
}

// Two profiles concatenated hold each block twice and each mode line once per
// half. Counting them would double every figure, so the second header is the
// signal that this is not one measurement.
func TestASecondModeHeaderIsRefused(t *testing.T) {
	in := profile("a/main.go:1.1,2.2 1 1") + profile("a/main.go:1.1,2.2 1 1")
	var buf bytes.Buffer
	err := run(strings.NewReader(in), &buf, 40)
	if err == nil {
		t.Fatal("two concatenated profiles were judged as one")
	}
	if !strings.Contains(err.Error(), "not one profile") {
		t.Errorf("error %q does not say why", err)
	}
}

// The list of uncovered blocks is bounded, and says so rather than stopping
// silently at a number the reader cannot see.
func TestTheBlockListIsBoundedAndSaysSo(t *testing.T) {
	recs := []string{
		"a/main.go:1.1,2.2 1 0",
		"a/main.go:3.1,4.2 1 0",
		"a/main.go:5.1,6.2 1 0",
	}
	var buf bytes.Buffer
	if err := run(strings.NewReader(profile(recs...)), &buf, 2); err == nil {
		t.Fatal("three uncovered blocks passed")
	}
	if !strings.Contains(buf.String(), "and 1 more") {
		t.Errorf("the list was truncated without saying so:\n%s", buf.String())
	}
}

// Packages are reported worst-first so the output names where the work is.
func TestPackagesAreReportedWorstFirst(t *testing.T) {
	var buf bytes.Buffer
	err := run(strings.NewReader(profile(
		"github.com/o/r/small/main.go:1.1,2.2 1 0",
		"github.com/o/r/big/main.go:1.1,9.2 7 0",
	)), &buf, 40)
	if err == nil {
		t.Fatal("uncovered blocks passed")
	}
	out := buf.String()
	if i, j := strings.Index(out, "github.com/o/r/big"), strings.Index(out, "github.com/o/r/small"); i < 0 || j < 0 || i > j {
		t.Errorf("packages are not worst-first:\n%s", out)
	}
}

// A blank line is not a record and is not an error: `go test` has written
// one, and a gate that refused the profile over it would be refusing the
// toolchain rather than the coverage.
func TestABlankLineIsSkipped(t *testing.T) {
	var buf bytes.Buffer
	if err := run(strings.NewReader("mode: atomic\n\na/main.go:1.1,2.2 1 1\n\n"), &buf, 40); err != nil {
		t.Fatalf("a blank line was treated as a defect: %v", err)
	}
}

// errReader fails partway, the way a truncated file or a closed pipe does.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// ⛔ A PROFILE THAT COULD NOT BE READ IS NOT A PROFILE WITH NOTHING WRONG.
// Reading stops where the error is, and whatever was parsed so far is a
// prefix: calling that prefix "every block" is the same mistake one level
// down from the one this command exists to fix.
func TestAReadThatFailedIsNotAVerdict(t *testing.T) {
	var buf bytes.Buffer
	err := run(errReader{err: os.ErrDeadlineExceeded}, &buf, 40)
	if err == nil {
		t.Fatal("a profile that could not be read was judged")
	}
	if !strings.Contains(err.Error(), os.ErrDeadlineExceeded.Error()) {
		t.Errorf("error %q does not carry the read failure", err)
	}
}

// Two packages owing the same amount are ordered by name, so two runs of the
// same profile print the same report.
func TestPackagesOwingTheSameAreOrderedByName(t *testing.T) {
	var buf bytes.Buffer
	err := run(strings.NewReader(profile(
		"github.com/o/r/zeta/main.go:1.1,2.2 2 0",
		"github.com/o/r/alpha/main.go:1.1,2.2 2 0",
	)), &buf, 40)
	if err == nil {
		t.Fatal("uncovered blocks passed")
	}
	out := buf.String()
	if i, j := strings.Index(out, "r/alpha"), strings.Index(out, "r/zeta"); i < 0 || j < 0 || i > j {
		t.Errorf("a tie is not broken by name:\n%s", out)
	}
}

func TestDirOfHandlesAPathWithNoDirectory(t *testing.T) {
	if got := dirOf("main.go:1.1,2.2"); got != "main.go" {
		t.Errorf("dirOf = %q, want the path itself when it has no directory", got)
	}
}

// main() is the wiring: the profile it cannot open, the flag it cannot parse
// and the verdict it carries out are all it says.
func TestMainReportsThroughTheExitStatus(t *testing.T) {
	oldArgs, oldExit := os.Args, osExit
	t.Cleanup(func() { os.Args, osExit = oldArgs, oldExit })
	exited := -1
	osExit = func(c int) { exited = c }

	dir := t.TempDir()
	green := filepath.Join(dir, "green.out")
	if err := os.WriteFile(green, []byte(profile("a/main.go:1.1,2.2 1 1")), 0o600); err != nil {
		t.Fatal(err)
	}
	red := filepath.Join(dir, "red.out")
	if err := os.WriteFile(red, []byte(profile("a/main.go:1.1,2.2 1 0")), 0o600); err != nil {
		t.Fatal(err)
	}

	os.Args = []string{"covgate", "-profile", green}
	main()
	if exited != -1 {
		t.Errorf("a covered profile exited with %d", exited)
	}

	os.Args = []string{"covgate", "-profile", red}
	main()
	if exited != 1 {
		t.Errorf("exit = %d, want 1 for an uncovered block", exited)
	}

	// ⛔ A PROFILE THAT IS NOT THERE EXITS 2, NOT 0 AND NOT 1. The old gate's
	// shape — read a file, find nothing, say nothing — is what let a typo in
	// the path read as a pass.
	exited = -1
	os.Args = []string{"covgate", "-profile", filepath.Join(dir, "absent.out")}
	main()
	if exited != 2 {
		t.Errorf("exit = %d, want 2 for a profile that does not exist", exited)
	}

	exited = -1
	os.Args = []string{"covgate", "-no-such-flag"}
	main()
	if exited != 2 {
		t.Errorf("exit = %d, want 2 for a bad flag", exited)
	}
}
