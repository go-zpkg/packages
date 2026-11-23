package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheLayoutTheBuildWritesIsListed(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{
		"acorn.io/acorn-cli/package.yml",
		"acorn.io/acorn-cli/windows/x86-64/versions.txt",
		"acorn.io/acorn-cli/windows/x86-64/v1.2.3.tar.gz",
		"arkade.dev/package.yml",
		"arkade.dev/windows/x86-64/v0.11.tar.gz",
	} {
		write(t, d, f)
	}
	got, err := List(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{
		{"acorn.io/acorn-cli", "1.2.3", "acorn.io/acorn-cli/windows/x86-64/v1.2.3.tar.gz"},
		{"arkade.dev", "0.11", "arkade.dev/windows/x86-64/v0.11.tar.gz"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Every shape the build does not write is an error: the names reach a publish
// command, and the tree comes from a job that runs other people's code.
func TestAnythingElseIsRefused(t *testing.T) {
	for _, rel := range []string{
		"package.yml",
		"stray.txt",
		"a/stray.txt",
		"a/windows/x86-64/stray.txt",
		"a/windows/arm64/v1.0.0.tar.gz",
		"a/linux/x86-64/v1.0.0.tar.gz",
		"windows/x86-64/v1.0.0.tar.gz",
		"a/windows/x86-64/v1.0.0.tar",
		"a/windows/x86-64/v1.tar.gz",
		"a/windows/x86-64/v1.0.0-rc1.tar.gz",
		"a/windows/x86-64/v1.0.0.tar.gz.sig",
		"a/windows/x86-64/v1.0.0..tar.gz",
		".hidden/windows/x86-64/v1.0.0.tar.gz",
		"a/.hidden/windows/x86-64/v1.0.0.tar.gz",
		"a/b/c/d/e/windows/x86-64/v1.0.0.tar.gz",
		"a b/windows/x86-64/v1.0.0.tar.gz",
		"a;b/windows/x86-64/v1.0.0.tar.gz",
		"a$(x)/windows/x86-64/v1.0.0.tar.gz",
		"a/versions.txt",
		"a/windows/versions.txt",
	} {
		d := t.TempDir()
		write(t, d, rel)
		if got, err := List(d); err == nil {
			t.Errorf("%q was accepted: %v", rel, got)
		}
	}
}

func TestADotDotSegmentCannotBeStaged(t *testing.T) {
	for _, rel := range []string{"../windows/x86-64/v1.0.0.tar.gz", "a/../windows/x86-64/v1.0.0.tar.gz", "./a/windows/x86-64/v1.0.0.tar.gz"} {
		if p, err := classify(rel); err == nil {
			t.Errorf("%q was accepted: %v", rel, p)
		}
	}
}

func TestASymlinkIsRefused(t *testing.T) {
	d := t.TempDir()
	write(t, d, "a/package.yml")
	if err := os.Symlink("package.yml", filepath.Join(d, "a", "l")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := List(d); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v", err)
	}
}

func TestTooManyFilesAreRefused(t *testing.T) {
	old := maxFiles
	maxFiles = 1
	defer func() { maxFiles = old }()
	d := t.TempDir()
	write(t, d, "a/package.yml")
	write(t, d, "b/package.yml")
	if _, err := List(d); err == nil || !strings.Contains(err.Error(), "more than 1 files") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingDirectoryIsAnError(t *testing.T) {
	_, err := List(filepath.Join(t.TempDir(), "none"))
	if err == nil || !strings.Contains(err.Error(), "staged root") {
		t.Fatalf("want a staged root error, got %v", err)
	}
}

// An unreadable directory reaches the walk callback WITH an entry and an
// error: the error must be returned, not swallowed.
func TestAnUnreadableDirectoryIsAnError(t *testing.T) {
	d := t.TempDir()
	sub := filepath.Join(d, "a")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o755)
	if _, err := os.ReadDir(sub); err == nil {
		t.Skip("directory permissions are not enforced here")
	}
	if _, err := List(d); err == nil {
		t.Fatal("an unreadable directory was listed as empty")
	}
}

func TestAFileIsNotATree(t *testing.T) {
	d := t.TempDir()
	write(t, d, "f")
	if _, err := List(filepath.Join(d, "f")); err == nil {
		t.Fatal("no error")
	}
}

func TestRunPrintsTabSeparatedLines(t *testing.T) {
	d := t.TempDir()
	write(t, d, "a.io/x/windows/x86-64/v2.0.tar.gz")
	var out, errb bytes.Buffer
	if code := run([]string{d}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if want := "a.io/x\t2.0\ta.io/x/windows/x86-64/v2.0.tar.gz\n"; out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}

func TestRunRefusesWithAnAnnotation(t *testing.T) {
	d := t.TempDir()
	write(t, d, "stray")
	var out, errb bytes.Buffer
	if code := run([]string{d}, &out, &errb); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.HasPrefix(errb.String(), "::error::") || out.Len() != 0 {
		t.Fatalf("out %q err %q", out.String(), errb.String())
	}
}

func TestRunWantsOneDirectory(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 1 {
		t.Fatalf("code %d", code)
	}
}

func TestMainExitsWithRunsCode(t *testing.T) {
	old, oldArgs := osExit, os.Args
	defer func() { osExit, os.Args = old, oldArgs }()
	got := -1
	osExit = func(c int) { got = c }
	os.Args = []string{"winstage"}
	main()
	if got != 1 {
		t.Fatalf("exit %d", got)
	}
}
