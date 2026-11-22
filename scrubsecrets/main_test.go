package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCmd(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretIsReplacedInTheFile(t *testing.T) {
	t.Setenv("TEST_SECRET_A", "s3cr3t-value-123")
	p := write(t, "failures-detail.txt", "boom\nkey=s3cr3t-value-123 more\ns3cr3t-value-123\n")
	_, _, code := runCmd(t, "--env", "TEST_SECRET_A", p)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "s3cr3t-value-123") {
		t.Errorf("secret still in file: %q", b)
	}
	if got := strings.Count(string(b), "***"); got != 2 {
		t.Errorf("want 2 replacements, got %d in %q", got, b)
	}
}

func TestEveryLineOfAMultiLineSecretIsReplaced(t *testing.T) {
	t.Setenv("TEST_SECRET_B", "untrusted comment: key\nRWRYabcdefghijklmnop0123456789\n")
	p := write(t, "x.txt", "printed: RWRYabcdefghijklmnop0123456789 only the second line\n")
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_B", p); code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "RWRYabcdef") {
		t.Errorf("a line of the secret survived: %q", b)
	}
}

func TestAFileWithoutTheSecretIsLeftAlone(t *testing.T) {
	t.Setenv("TEST_SECRET_C", "another-long-secret")
	p := write(t, "x.txt", "nothing to see\n")
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_C", p); code != 0 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "nothing to see\n" {
		t.Errorf("file changed: %q", b)
	}
}

func TestAMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("TEST_SECRET_D", "yet-another-secret")
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_D", filepath.Join(t.TempDir(), "absent")); code != 0 {
		t.Errorf("exit %d for an absent file", code)
	}
}

func TestAShortSecretIsRefusedNotApplied(t *testing.T) {
	t.Setenv("TEST_SECRET_E", "1")
	p := write(t, "x.txt", "1 1 1\n")
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_E", p); code == 0 {
		t.Error("a one-character secret was accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "1 1 1\n" {
		t.Errorf("file was rewritten: %q", b)
	}
}

func TestAnEmptyVariableScrubsNothingAndSaysSo(t *testing.T) {
	t.Setenv("TEST_SECRET_F", "")
	p := write(t, "x.txt", "keep\n")
	out, _, code := runCmd(t, "--env", "TEST_SECRET_F", p)
	if code != 0 || !strings.Contains(out, "TEST_SECRET_F is empty") {
		t.Errorf("exit %d, out %q", code, out)
	}
}

func TestAnUnrewritableFileIsRemoved(t *testing.T) {
	t.Setenv("TEST_SECRET_G", "read-only-secret-1")
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("read-only-secret-1\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only file")
	}
	_, _, code := runCmd(t, "--env", "TEST_SECRET_G", p)
	if code == 0 {
		t.Error("a failed rewrite exited 0")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("the unscrubbed file was left in place")
	}
}

func TestAMissingFileIsReportedAsNotExistAndCountsNothing(t *testing.T) {
	n, err := scrubFile(filepath.Join(t.TempDir(), "absent"), []string{"whatever-secret"})
	if n != 0 || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("got %d, %v", n, err)
	}
}

func TestASymlinkIsRefusedAndRemovedNotFollowed(t *testing.T) {
	t.Setenv("TEST_SECRET_H", "symlinked-secret-1")
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("symlinked-secret-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here")
	}
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_H", link); code == 0 {
		t.Error("a symlink was accepted")
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("the symlink was left in place")
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "symlinked-secret-1") {
		t.Error("the symlink's target was rewritten")
	}
}

func TestAnUnreadableFileIsRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	t.Setenv("TEST_SECRET_I", "unreadable-secret-1")
	p := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(p, []byte("unreadable-secret-1\n"), 0o200); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_I", p); code == 0 {
		t.Error("an unreadable file exited 0")
	}
	if _, err := os.Lstat(p); err == nil {
		t.Error("the unreadable file was left in place")
	}
}

func TestAFileWithoutTheSecretIsNotRewritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only file")
	}
	t.Setenv("TEST_SECRET_J", "never-appears-here-1")
	p := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(p, []byte("clean\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runCmd(t, "--env", "TEST_SECRET_J", p); code != 0 {
		t.Errorf("a clean read-only file exited %d: it was rewritten", code)
	}
	if _, err := os.Lstat(p); err != nil {
		t.Error("a clean file was removed")
	}
}

func TestNoEnvNamesIsRefused(t *testing.T) {
	p := write(t, "x.txt", "x\n")
	if _, _, code := runCmd(t, p); code == 0 {
		t.Error("no -env accepted")
	}
}
