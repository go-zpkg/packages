package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for p, c := range files {
		fp := filepath.Join(d, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func digest(t *testing.T, d string) string {
	t.Helper()
	g, err := TreeDigest(d)
	if err != nil {
		t.Fatal(err)
	}
	if !digestRe.MatchString(g) {
		t.Fatalf("malformed digest %q", g)
	}
	return g
}

func TestDigestIsStableAndIgnoresCreationOrderAndMode(t *testing.T) {
	a := digest(t, tree(t, map[string]string{"publish-plan.json": "{}", "x/y.tar": "abc"}))
	d := tree(t, map[string]string{"x/y.tar": "abc", "publish-plan.json": "{}"})
	if err := os.Chmod(filepath.Join(d, "x", "y.tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := digest(t, d); a != b {
		t.Errorf("same content, different digest: %s vs %s", a, b)
	}
}

func TestDigestChangesWithContentNameAndSet(t *testing.T) {
	base := digest(t, tree(t, map[string]string{"a": "1", "b": "2"}))
	for name, files := range map[string]map[string]string{
		"content":  {"a": "1", "b": "3"},
		"name":     {"a": "1", "c": "2"},
		"extra":    {"a": "1", "b": "2", "c": ""},
		"missing":  {"a": "1"},
		"moved":    {"a": "1", "d/b": "2"},
		"swapped":  {"a": "2", "b": "1"},
		"joined":   {"a": "1b\x002"},
		"boundary": {"a1": "", "b": "2"},
	} {
		if got := digest(t, tree(t, files)); got == base {
			t.Errorf("%s: digest did not change", name)
		}
	}
}

func TestAnEmptyTreeHasADigest(t *testing.T) {
	if digest(t, t.TempDir()) == digest(t, tree(t, map[string]string{"a": ""})) {
		t.Error("an empty tree and a tree with one empty file share a digest")
	}
}

func TestASymlinkIsRefused(t *testing.T) {
	d := tree(t, map[string]string{"a": "1"})
	if err := os.Symlink("a", filepath.Join(d, "l")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := TreeDigest(d); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("symlink accepted: %v", err)
	}
}

func TestANonDirectoryAndAMissingPathAreRefused(t *testing.T) {
	d := tree(t, map[string]string{"a": "1"})
	if _, err := TreeDigest(filepath.Join(d, "a")); err == nil {
		t.Error("a file was accepted as a tree")
	}
	if _, err := TreeDigest(filepath.Join(d, "nope")); err == nil {
		t.Error("a missing path was accepted")
	}
}

func runCmd(args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func TestVerifyAcceptsTheDeclaredDigestAndRefusesEverythingElse(t *testing.T) {
	d := tree(t, map[string]string{"a": "1"})
	want := digest(t, d)
	if out, _, code := runCmd("--verify", want, d); code != 0 || strings.TrimSpace(out) != want {
		t.Errorf("declared digest: exit %d, %q", code, out)
	}
	other := digest(t, tree(t, map[string]string{"a": "2"}))
	if _, errb, code := runCmd("--verify", other, d); code != 1 || !strings.Contains(errb, "declared") {
		t.Errorf("a different digest: exit %d, %q", code, errb)
	}
	// An empty declaration is the build job having produced nothing: refused,
	// never read as "nothing to compare".
	for _, v := range []string{"", "sha256:", "sha256:ABC", "abc", want + "\n", strings.ToUpper(want)} {
		if _, errb, code := runCmd("--verify", v, d); code != 1 || !strings.Contains(errb, "nothing to verify against") {
			t.Errorf("--verify %q: exit %d, %q", v, code, errb)
		}
	}
}

func TestPrintWithoutVerify(t *testing.T) {
	d := tree(t, map[string]string{"a": "1"})
	out, _, code := runCmd(d)
	if code != 0 || strings.TrimSpace(out) != digest(t, d) {
		t.Errorf("exit %d, %q", code, out)
	}
	if _, _, code := runCmd(); code != 1 {
		t.Errorf("no directory: exit %d", code)
	}
	if _, _, code := runCmd(filepath.Join(d, "missing")); code != 1 {
		t.Errorf("missing directory: exit %d", code)
	}
}

func TestMainExitsWithTheRunStatus(t *testing.T) {
	var got int
	old := osExit
	osExit = func(c int) { got = c }
	defer func() { osExit = old }()
	os.Args = []string{"distdigest"}
	main()
	if got != 1 {
		t.Errorf("exit %d, want 1", got)
	}
}
