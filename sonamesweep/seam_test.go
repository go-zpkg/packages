package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The helper-process protocol: this binary re-executed with the env var set
// writes known bytes to each stream and exits 0.
const (
	helperEnv    = "SONAMESWEEP_RUNCMD_HELPER"
	helperStdout = "helper-wrote-this-to-stdout"
	helperStderr = "helper-wrote-this-to-stderr"
)

// TestRunCmdHelperProcess is not a test. It is the child process
// TestTheRealRunCmdCapturesOutputAndDistinguishesAFailureToStart starts, and
// it returns at once unless it was started on purpose.
func TestRunCmdHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	fmt.Fprint(os.Stdout, helperStdout)
	fmt.Fprint(os.Stderr, helperStderr)
	os.Exit(0)
}

// realRunCmd is the shipped seam, captured before any test replaces the var.
// Go initialises a package-level variable after the ones it reads, so this
// holds the function literal from main.go whatever the tests do to runCmd.
var realRunCmd = runCmd

// ⛔ THE SEAM ITSELF WAS NEVER EXERCISED, AND THE GATE COULD NOT SAY SO.
// `go tool cover -func` attributes a block to the enclosing function
// DECLARATION; the body of `var runCmd = func(…)` encloses none, so its five
// statements were invisible to the 100 % gate. See #458.
//
// It matters here more than almost anywhere else in this repository: the
// header of main.go records this program passing on 2026-11-01 and
// 2026-11-08 having run no checker at all, because "could not start" and "ran
// and said no" were indistinguishable. The code that tells those apart is
// this seam and [neverRan], and the seam had no test.
func TestTheRealRunCmdCapturesOutputAndDistinguishesAFailureToStart(t *testing.T) {
	// The test binary itself is the one child guaranteed to exist on every
	// platform this runs on, and the env var turns it into a helper that
	// writes known bytes. Nothing here is skipped or conditional: a seam test
	// that quietly does not run is how coverage goes nondeterministic.
	t.Setenv(helperEnv, "1")
	out, err := realRunCmd(os.Args[0], "-test.run=TestRunCmdHelperProcess")
	if err != nil {
		t.Fatalf("a child that exits 0 was an error: %v", err)
	}
	// Both streams, because runCmd points Stdout and Stderr at one buffer:
	// `zpkb unresolved` reports on one and explains itself on the other, and
	// a seam that kept only one would lose half of every finding.
	if !strings.Contains(out, helperStdout) || !strings.Contains(out, helperStderr) {
		t.Errorf("output = %q, want both %q and %q: a seam that loses a stream loses findings",
			out, helperStdout, helperStderr)
	}

	// A binary that is not there must be an error neverRan can classify. The
	// whole sweep's honesty rests on this distinction.
	out, err = realRunCmd("zpkb-that-is-not-installed-here")
	if err == nil {
		t.Fatal("a missing binary ran successfully")
	}
	if !neverRan(err) {
		t.Errorf("neverRan(%v) = false: a missing binary must be 'could not start', not 'ran and said no'", err)
	}
	if out != "" {
		t.Errorf("output = %q, want none from a command that never started", out)
	}
}
