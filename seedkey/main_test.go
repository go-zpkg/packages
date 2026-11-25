package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-attest/sign"
)

func runIt(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	c := run(args, &out, &errb)
	return c, out.String(), errb.String()
}

var entryRe = regexp.MustCompile(`(?m)^([A-Za-z0-9._-]+):(RW[A-Za-z0-9+/]{54})$`)

func TestTheSecretIsWrittenPrivatelyAndOnlyThePublicHalfIsPrinted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k")
	sum := filepath.Join(t.TempDir(), "summary.md")
	c, out, e := runIt("--secret", p, "--name", "lan-1", "--summary", sum)
	if c != 0 || e != "" {
		t.Fatalf("code=%d err=%q", c, e)
	}
	m := entryRe.FindStringSubmatch(out)
	if m == nil || m[1] != "lan-1" {
		t.Fatalf("no name:key entry in:\n%s", out)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("secret file: %v %v", st, err)
	}
	b, _ := os.ReadFile(p)
	kp, err := sign.LoadSecretKey(string(b))
	if err != nil {
		t.Fatalf("the file is not a secret key: %v", err)
	}
	if kp.PublicKeyString() != m[2] {
		t.Error("the printed key is not the secret's public half")
	}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n")[1:] {
		if strings.Contains(out, l) {
			t.Error("a secret line was printed")
		}
	}
	s, _ := os.ReadFile(sum)
	if !strings.Contains(string(s), m[0]) || strings.Contains(string(s), strings.TrimSpace(strings.Split(string(b), "\n")[1])) {
		t.Errorf("summary wrong: %s", s)
	}
}

func TestTwoRunsMintTwoKeys(t *testing.T) {
	d := t.TempDir()
	_, a, _ := runIt("--secret", filepath.Join(d, "a"), "--name", "x")
	_, b, _ := runIt("--secret", filepath.Join(d, "b"), "--name", "x")
	if entryRe.FindString(a) == entryRe.FindString(b) || entryRe.FindString(a) == "" {
		t.Error("two runs minted the same key")
	}
}

func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(p, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, e := runIt("--secret", p, "--name", "x")
	if c == 0 || !strings.Contains(e, "cannot create") {
		t.Fatalf("code=%d err=%q", c, e)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Error("the file was overwritten")
	}
}

func TestArgumentsAreRefused(t *testing.T) {
	d := t.TempDir()
	for name, args := range map[string][]string{
		"no secret":    {"--name", "x"},
		"no name":      {"--secret", filepath.Join(d, "k1")},
		"bad name":     {"--secret", filepath.Join(d, "k2"), "--name", "a:b"},
		"long name":    {"--secret", filepath.Join(d, "k3"), "--name", strings.Repeat("a", 65)},
		"positional":   {"--secret", filepath.Join(d, "k4"), "--name", "x", "extra"},
		"newline":      {"--secret", filepath.Join(d, "k5"), "--name", "a\nb"},
		"unknown flag": {"--nope"},
	} {
		if c, _, _ := runIt(args...); c == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, e := runIt("--name", "x"); !strings.Contains(e, "--secret is required") {
		t.Errorf("no secret: %q", e)
	}
	if _, err := os.Stat(filepath.Join(d, "k1")); err == nil {
		t.Error("a refused run left a key file")
	}
}

func TestASecretInsideAGitWorkTreeIsRefused(t *testing.T) {
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "sub", "k")
	if err := os.Mkdir(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	c, _, e := runIt("--secret", p, "--name", "x")
	if c == 0 || !strings.Contains(e, "git work tree") {
		t.Fatalf("code=%d err=%q", c, e)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("the secret was written inside the work tree")
	}
}

func TestAGenerationFailureLeavesNothingAndNamesNoKey(t *testing.T) {
	old := generate
	generate = func() (*sign.Keypair, error) { return nil, errors.New("entropy: SECRETDETAIL") }
	t.Cleanup(func() { generate = old })
	p := filepath.Join(t.TempDir(), "k")
	c, _, e := runIt("--secret", p, "--name", "x")
	if c == 0 || strings.Contains(e, "SECRETDETAIL") {
		t.Fatalf("code=%d err=%q", c, e)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a file was left behind")
	}
}

func TestAnUnwritableSummaryFails(t *testing.T) {
	d := t.TempDir()
	c, _, e := runIt("--secret", filepath.Join(d, "k"), "--name", "x", "--summary", filepath.Join(d, "no", "such", "s.md"))
	if c == 0 || !strings.Contains(e, "cannot open the summary") {
		t.Fatalf("code=%d err=%q", c, e)
	}
}

// closedFile is what a disk that refuses the write looks like.
func closedFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "c")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f
}

func TestARefusedSecretWriteIsAnErrorAndLeavesNoFile(t *testing.T) {
	old := openFile
	openFile = func(string, int, os.FileMode) (*os.File, error) { return closedFile(t), nil }
	t.Cleanup(func() { openFile = old })
	p := filepath.Join(t.TempDir(), "k")
	c, out, e := runIt("--secret", p, "--name", "x")
	if c == 0 || !strings.Contains(e, "cannot write") {
		t.Fatalf("code=%d err=%q", c, e)
	}
	if out != "" {
		t.Errorf("a public key was printed for a secret that was not written: %q", out)
	}
}

func TestARefusedSummaryWriteIsAnError(t *testing.T) {
	d := t.TempDir()
	sum := filepath.Join(d, "s.md")
	old := openFile
	openFile = func(p string, flag int, m os.FileMode) (*os.File, error) {
		if p == sum {
			return closedFile(t), nil
		}
		return old(p, flag, m)
	}
	t.Cleanup(func() { openFile = old })
	c, _, e := runIt("--secret", filepath.Join(d, "k"), "--name", "x", "--summary", sum)
	if c == 0 || !strings.Contains(e, "cannot write the summary") {
		t.Fatalf("code=%d err=%q", c, e)
	}
}

func TestAnUnreadableWorkingDirectoryRefusesTheSecret(t *testing.T) {
	old := absPath
	absPath = func(string) (string, error) { return "", errors.New("no working directory") }
	t.Cleanup(func() { absPath = old })
	p := filepath.Join(t.TempDir(), "k")
	c, _, e := runIt("--secret", p, "--name", "x")
	if c == 0 || !strings.Contains(e, "no working directory") {
		t.Fatalf("code=%d err=%q", c, e)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a secret was written where its location could not be judged")
	}
}

func TestMainRunsAndExits(t *testing.T) {
	old := osExit
	var code = -1
	osExit = func(c int) { code = c }
	t.Cleanup(func() { osExit = old })
	oa := os.Args
	os.Args = []string{"seedkey", "--nope"}
	t.Cleanup(func() { os.Args = oa })
	main()
	if code != 1 {
		t.Errorf("exit %d", code)
	}
}
