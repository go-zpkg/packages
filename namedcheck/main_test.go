package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, failures, plan string) (string, string) {
	t.Helper()
	d := t.TempDir()
	f := filepath.Join(d, "failures.txt")
	if failures != "\x00" {
		if err := os.WriteFile(f, []byte(failures), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if plan != "\x00" {
		if err := os.WriteFile(filepath.Join(d, "publish-plan.json"), []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f, d
}

const none = "\x00"

// ⛔ promo-r1: failures.txt held "lz4.org 1.11.1 origin", the plan staged 0
// artifacts, dist held the plan, and the step that counted FILES said 1 built.
func TestTheRunThatWasGreenIsRed(t *testing.T) {
	f, d := fixture(t, "lz4.org 1.11.1 origin\n", `{"artifacts":[]}`)
	for _, req := range []bool{false, true} {
		var out, errb bytes.Buffer
		args := []string{"--failures", f, "--stage", d}
		if req {
			args = append(args, "--require-staged")
		}
		if code := run(args, &out, &errb); code != 1 || !strings.Contains(errb.String(), "1 of it failed") {
			t.Errorf("require-staged=%v: code=%d out=%q err=%q", req, code, out.String(), errb.String())
		}
	}
}

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name          string
		failures      string
		plan          string
		requireStaged bool
		want          string // "" = pass, else a fragment of the error
	}{
		{"built and clean", "", `{"artifacts":[{}]}`, true, ""},
		{"some built, one failed: still failed", "a 1 build\n", `{"artifacts":[{}]}`, false, "failed"},
		{"blank lines are not failures", "\n  \n", `{"artifacts":[{}]}`, false, ""},
		{"nothing to do, not a promotion", "", `{"artifacts":[]}`, false, ""},
		{"a promotion that staged nothing", "", `{"artifacts":[]}`, true, "promoted nothing"},
		{"a promotion with no plan at all", "", none, true, "promoted nothing"},
		{"no failure list, no plan, not a promotion", none, none, false, ""},
		{"a plan that is not JSON", "", `{`, false, "not a stage plan"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, d := fixture(t, c.failures, c.plan)
			_, err := Check(f, d, c.requireStaged)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("unexpected: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestCheckCountsWhatItReads(t *testing.T) {
	f, d := fixture(t, "a\nb\n\nc", `{"artifacts":[{},{}]}`)
	v, err := Check(f, d, false)
	if err == nil || v.Failures != 3 || v.Staged != 2 || !v.HavePlan {
		t.Errorf("v=%+v err=%v", v, err)
	}
	if got := v.Summary(); got != "3 failure(s) recorded, 2 artifact(s) staged" {
		t.Errorf("summary = %q", got)
	}
	if got := (Verdict{}).Summary(); got != "0 failure(s) recorded, no plan" {
		t.Errorf("summary = %q", got)
	}
}

func TestAnUnreadableFileIsAFailure(t *testing.T) {
	d := t.TempDir()
	// failures.txt is a directory: it exists and cannot be read.
	if err := os.Mkdir(filepath.Join(d, "failures.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(filepath.Join(d, "failures.txt"), d, false); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("err = %v", err)
	}
	f, d2 := fixture(t, "", none)
	if err := os.Mkdir(filepath.Join(d2, "publish-plan.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(f, d2, false); err == nil || !strings.Contains(err.Error(), "stage plan") {
		t.Errorf("err = %v", err)
	}
}

func TestAnOversizedFileIsRefused(t *testing.T) {
	f, d := fixture(t, strings.Repeat("x\n", maxFileBytes/2+1), none)
	if _, err := Check(f, d, false); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
}

func TestMainExitsWithRunsStatus(t *testing.T) {
	old := osExit
	defer func() { osExit = old }()
	got := -1
	osExit = func(c int) { got = c }
	f, d := fixture(t, "x\n", none)
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"namedcheck", "--failures", f, "--stage", d}
	main()
	if got != 1 {
		t.Errorf("exit = %d", got)
	}
}

func TestACleanRunExitsZeroAndSaysWhatItRead(t *testing.T) {
	f, d := fixture(t, "", `{"artifacts":[{}]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"--failures", f, "--stage", d, "--require-staged"}, &out, &errb); code != 0 || errb.Len() != 0 {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if got := out.String(); got != "0 failure(s) recorded, 1 artifact(s) staged\n" {
		t.Errorf("out = %q", got)
	}
}

func TestUnknownFlagAndArgsAreRefused(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--nope"}, &out, &errb); code != 1 {
		t.Errorf("code = %d", code)
	}
	if code := run([]string{"stray"}, &out, &errb); code != 1 {
		t.Errorf("code = %d", code)
	}
}
