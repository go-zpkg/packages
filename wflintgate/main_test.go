package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ The one entry that would make this gate pointless is the one somebody
// reaches for the first time the lane goes red over SC2215.
func TestTheClassItExistsForCanNeverBeForgiven(t *testing.T) {
	for _, ig := range ignored {
		if strings.Contains(theClass, ig.code) || strings.Contains(ig.code, theClass) {
			t.Fatalf("%s is on the ignore list — the gate would pass over the defect it was written for", theClass)
		}
	}
}

// The two controls must differ in ONE thing: where the comment sits. If they
// differed in anything else, a disagreement between them would not be
// evidence about comment placement.
func TestTheControlsDifferOnlyInWhereTheCommentSits(t *testing.T) {
	defect, clean := controlWorkflow(true), controlWorkflow(false)
	if defect == clean {
		t.Fatal("the two controls are identical — nothing is being compared")
	}
	norm := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			out = append(out, strings.TrimSuffix(l, " \\"))
		}
		return strings.Join(out, "\n")
	}
	if norm(defect) != norm(clean) {
		t.Errorf("the controls differ in more than the comment's position:\n--- defect\n%s\n--- clean\n%s", norm(defect), norm(clean))
	}

	// And the defect really is a comment INSIDE a continuation: a `#` line
	// whose predecessor ends with a backslash.
	if !hasCommentInsideAContinuation(defect) {
		t.Error("the positive control does not carry the defect it is meant to carry")
	}
	if hasCommentInsideAContinuation(clean) {
		t.Error("the negative control carries the defect")
	}
}

func hasCommentInsideAContinuation(s string) bool {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		prev := strings.TrimRight(lines[i-1], " ")
		if strings.HasSuffix(prev, "\\") && strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			return true
		}
	}
	return false
}

func TestKeepForgivesOnlyWhatTheListNames(t *testing.T) {
	out := strings.Join([]string{
		`.github/workflows/a.yml:1:1: shellcheck reported issue in this script: SC2016:info:2:14: Expressions don't expand [shellcheck]`,
		`  |`,
		`1 |         run: |`,
		`.github/workflows/b.yml:2:2: shellcheck reported issue in this script: SC2215:warning:8:3: This flag is used as a command name [shellcheck]`,
		`.github/workflows/c.yml:3:3: label "incus" is unknown [runner-label]`,
		`.github/workflows/d.yml:4:4: property "nope" is not defined [expression]`,
	}, "\n")
	kept := keep(out)
	if len(kept) != 3 {
		t.Fatalf("kept %d finding(s), want 3: %v", len(kept), kept)
	}
	// ⛔ `runner-label` is among them: it is answered by naming the labels in
	// .github/actionlint.yaml, not by forgiving the finding. A test that
	// expected it dropped would let that file be deleted silently.
	if !strings.Contains(kept[0], "SC2215") || !strings.Contains(kept[1], "[runner-label]") || !strings.Contains(kept[2], "[expression]") {
		t.Errorf("kept the wrong findings: %v", kept)
	}
}

func TestAMissingShellcheckIsRefusedRatherThanSkipped(t *testing.T) {
	restore := lookPath
	defer func() { lookPath = restore }()
	lookPath = func(name string) (string, error) {
		if name == "shellcheck" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "IN SILENCE") {
		t.Errorf("the refusal does not say what a missing shellcheck costs: %q", errb.String())
	}
}

// A judge that answers the same whatever it is shown is not a judge.
func TestAControlThatComesBackWrongStopsTheRun(t *testing.T) {
	stubPath(t)
	for _, tc := range []struct {
		name string
		out  func(args []string) string
	}{
		{"positive control not flagged", func([]string) string { return "" }},
		{"negative control flagged too", func([]string) string { return "x: SC2215: y" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := runCmd
			defer func() { runCmd = restore }()
			runCmd = func(_ string, args ...string) (string, int) { return tc.out(args), 1 }
			var out, errb bytes.Buffer
			if code := run([]string{"-dir", workflowDir(t)}, &out, &errb); code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			if !strings.Contains(errb.String(), "not measuring anything") {
				t.Errorf("the refusal does not say the gate is blind: %q", errb.String())
			}
		})
	}
}

func TestItPassesWhenOnlyForgivenFindingsRemain(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	runCmd = func(_ string, args ...string) (string, int) {
		switch {
		case strings.HasSuffix(args[len(args)-1], "positive.yml"):
			return "ctl: SC2215: there", 1
		case strings.HasSuffix(args[len(args)-1], "negative.yml"):
			return "", 0
		}
		// ⛔ Exit 1 WITH a finding that is forgiven: the shape the first
		// version of this gate refused, calling a clean repository unjudged.
		return `.github/workflows/a.yml:1:1: SC2016:info [shellcheck]`, 1
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-dir", workflowDir(t)}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%q", code, errb.String())
	}
	// The forgiven list is printed, with its reasons, on a PASSING run too.
	if !strings.Contains(out.String(), "forgiven on purpose") || !strings.Contains(out.String(), "SC2016") {
		t.Errorf("a passing run does not say what it forgave: %q", out.String())
	}
}

func TestAnUnforgivenFindingFailsTheRun(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	runCmd = func(_ string, args ...string) (string, int) {
		switch {
		case strings.HasSuffix(args[len(args)-1], "positive.yml"):
			return "ctl: SC2215: there", 1
		case strings.HasSuffix(args[len(args)-1], "negative.yml"):
			return "", 0
		}
		return `.github/workflows/a.yml:1:1: shellcheck reported issue: SC2215:warning [shellcheck]`, 1
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-dir", workflowDir(t)}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "SC2215") {
		t.Errorf("the failure does not name the finding: %q", errb.String())
	}
}

// ⛔⛔ THE DEFECT THIS GATE ALMOST SHIPPED. The first version passed the
// DIRECTORY to actionlint, which answers `is a directory` and exits 3 — and
// the gate printed "0 finding(s)" and exited 0 over a tree it had never read.
// The synthetic controls did not catch it, because a control is a single file.
func TestActionlintDecliningToJudgeIsNotAPass(t *testing.T) {
	stubPath(t)
	for _, code := range []int{2, 3, -1} {
		restore := runCmd
		runCmd = func(_ string, args ...string) (string, int) {
			if strings.HasSuffix(args[len(args)-1], ".yml") && strings.Contains(args[len(args)-1], "positive") {
				return "ctl: SC2215: there", 1
			}
			if strings.Contains(args[len(args)-1], "negative") {
				return "", 0
			}
			return `could not read "x": is a directory`, code
		}
		var out, errb bytes.Buffer
		got := run([]string{"-dir", workflowDir(t)}, &out, &errb)
		runCmd = restore
		if got != 2 {
			t.Errorf("actionlint exit %d read as exit %d, want a refusal (2)", code, got)
		}
		if !strings.Contains(errb.String(), "did not judge this tree") {
			t.Errorf("exit %d: the refusal does not say the tree went unjudged: %q", code, errb.String())
		}
	}
}

// A directory with no workflow in it is the same failure one level earlier.
func TestAnEmptyDirectoryIsARefusal(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	runCmd = func(_ string, args ...string) (string, int) {
		if strings.Contains(args[len(args)-1], "positive") {
			return "ctl: SC2215: there", 1
		}
		return "", 0
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-dir", t.TempDir()}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "nothing to read is not a pass") {
		t.Errorf("the refusal does not say why: %q", errb.String())
	}
}

// The count of files linted is printed, so "it read nothing" is visible in the
// log of a PASSING run and not only in a failing one.
func TestAPassingRunSaysHowManyFilesItRead(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	runCmd = func(_ string, args ...string) (string, int) {
		if strings.Contains(args[len(args)-1], "positive") {
			return "ctl: SC2215: there", 1
		}
		return "", 0
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-dir", workflowDir(t)}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "2 file(s) linted") {
		t.Errorf("a passing run does not say how much it read: %q", out.String())
	}
}

// ⛔ The real runCmd, not a stub: its three arms are the gate's only contact
// with actionlint, and a stub proves nothing about them. `go` stands in for
// actionlint here — the question is how an exit code is READ, not who produced
// it.
func TestTheRealRunCmdReadsAnExitCode(t *testing.T) {
	if out, code := runCmd("go", "env", "GOROOT"); code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("a command that succeeds read as code %d, output %q", code, out)
	}
	if _, code := runCmd("go", "--not-a-flag"); code <= 0 {
		t.Errorf("a command that exits non-zero read as code %d, want a positive status", code)
	}
	// Never started at all: -1, and NOT 0. A missing judge must not read as
	// a clean verdict.
	if _, code := runCmd("wflintgate-no-such-program"); code != -1 {
		t.Errorf("a command that cannot start read as code %d, want -1", code)
	}
}

func TestMainReportsThroughOsExit(t *testing.T) {
	stubPath(t)
	restoreExit, restoreArgs, restoreRun := osExit, os.Args, runCmd
	defer func() { osExit, os.Args, runCmd = restoreExit, restoreArgs, restoreRun }()
	runCmd = func(_ string, args ...string) (string, int) {
		if strings.Contains(args[len(args)-1], "positive") {
			return "ctl: SC2215: there", 1
		}
		return "", 0
	}
	got := -1
	osExit = func(code int) { got = code }
	os.Args = []string{"wflintgate", "-dir", workflowDir(t)}
	main()
	if got != 0 {
		t.Errorf("main exited %d, want 0", got)
	}
}

func TestAnUnknownFlagIsRefused(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-not-a-flag"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestAMissingActionlintIsNamed(t *testing.T) {
	restore := lookPath
	defer func() { lookPath = restore }()
	lookPath = func(name string) (string, error) {
		if name == "actionlint" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "actionlint is not on PATH") {
		t.Errorf("the refusal does not name what is missing: %q", errb.String())
	}
}

// A `-dir` that cannot even be globbed is a refusal, like an empty one.
func TestADirectoryThatCannotBeGlobbedIsARefusal(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	runCmd = func(_ string, args ...string) (string, int) {
		if strings.Contains(args[len(args)-1], "positive") {
			return "ctl: SC2215: there", 1
		}
		return "", 0
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-dir", "[" /* ErrBadPattern */}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// -no-proof skips the controls. It exists for debugging and a test holds it to
// that: with the controls skipped the verdict still comes from actionlint.
func TestNoProofSkipsTheControlsAndStillJudges(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	calls := 0
	runCmd = func(_ string, args ...string) (string, int) {
		calls++
		return `.github/workflows/a.yml:1:1: SC2215:warning [shellcheck]`, 1
	}
	var out, errb bytes.Buffer
	if code := run([]string{"-no-proof", "-dir", workflowDir(t)}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if calls != 1 {
		t.Errorf("actionlint ran %d time(s) — with -no-proof it should run once, for the tree alone", calls)
	}
	if strings.Contains(out.String(), "control") {
		t.Errorf("-no-proof still printed a control: %q", out.String())
	}
}

// The two arms of prove that only a seam can enter: a temp directory it cannot
// make, and a control it cannot write.
func TestProveRefusesWhenItCannotWriteItsControls(t *testing.T) {
	stubPath(t)
	t.Run("no temp directory", func(t *testing.T) {
		restore := mkdirTemp
		defer func() { mkdirTemp = restore }()
		mkdirTemp = func(string, string) (string, error) { return "", errors.New("no space") }
		var out, errb bytes.Buffer
		if code := run(nil, &out, &errb); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
		if !strings.Contains(errb.String(), "no space") {
			t.Errorf("the refusal does not say why: %q", errb.String())
		}
	})
	t.Run("control not written", func(t *testing.T) {
		restore := writeFile
		defer func() { writeFile = restore }()
		writeFile = func(string, []byte, os.FileMode) error { return errors.New("read-only") }
		var out, errb bytes.Buffer
		if code := run(nil, &out, &errb); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
		if !strings.Contains(errb.String(), "read-only") {
			t.Errorf("the refusal does not say why: %q", errb.String())
		}
	})
}

// ⛔ A control actionlint DECLINED to read is not a control that came back
// clean. Same distinction as for the tree, one step earlier.
func TestAControlActionlintCouldNotReadIsARefusal(t *testing.T) {
	stubPath(t)
	restore := runCmd
	defer func() { runCmd = restore }()
	runCmd = func(_ string, args ...string) (string, int) {
		return `could not read: is a directory`, 3
	}
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "did not read it") {
		t.Errorf("the refusal does not say the control went unread: %q", errb.String())
	}
}

// workflowDir is a directory holding two workflow files, so filesIn has
// something real to glob. Their CONTENT does not matter: runCmd is stubbed.
func workflowDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, n := range []string{"a.yml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte("name: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func stubPath(t *testing.T) {
	t.Helper()
	restore := lookPath
	t.Cleanup(func() { lookPath = restore })
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
}
