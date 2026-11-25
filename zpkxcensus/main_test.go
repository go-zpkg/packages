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
	for name, body := range files {
		p := filepath.Join(d, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func run(args ...string) (string, error) {
	var out bytes.Buffer
	c := RootCmd()
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&out)
	err := c.Execute()
	return out.String(), err
}

func TestACleanTreePasses(t *testing.T) {
	d := tree(t, map[string]string{
		"a.org/package.hcl": "# zpkx +foo is only discussed here\nbuild { script = \"make\" }\n",
		"b.org/package.yml": "# pkgx.sh is the upstream project\nbuild:\n  script: make\n",
		"notes.txt":         "zpkx +ignored",
	})
	out, err := run("--recipes", d)
	if err != nil || !strings.Contains(out, "2 recipe file(s) read, none invokes zpkx") {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestEveryInvocationSpellingIsFound(t *testing.T) {
	for name, body := range map[string]string{
		"zpkx":       "  zpkx +gnu.org/make -- make\n",
		"in a shell": "FOO=1; zpkx +x -- tool\n",
		"pkgx plus":  "  pkgx +gnu.org/make\n",
		"pkgx flag":  "pkgx --quiet foo\n",
		"patch":      "+  zpkx +x\n",
		"quoted":     "script = \"cd src && zpkx +x -- t\"\n",
	} {
		d := tree(t, map[string]string{"p.org/package.hcl": body})
		out, err := run("--recipes", d)
		if err == nil || !strings.Contains(out, "p.org/package.hcl:1:") || !strings.Contains(err.Error(), "1 recipe line(s)") {
			t.Errorf("%s: %q, %v", name, out, err)
		}
	}
}

func TestWordsThatMerelyContainTheNameAreNotInvocations(t *testing.T) {
	d := tree(t, map[string]string{"p.org/package.hcl": "a = \"mzpkx\"\nb = \"github.com/pkgx/pkgx\"\nc = \"pkgx is upstream\"\n"})
	if out, err := run("--recipes", d); err != nil {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestACensusThatReadNothingIsRefused(t *testing.T) {
	if _, err := run("--recipes", t.TempDir()); err == nil || !strings.Contains(err.Error(), "holds no recipe file") {
		t.Fatalf("an empty tree passed: %v", err)
	}
	if _, err := run("--recipes", filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing tree passed")
	}
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "--recipes is required") {
		t.Fatalf("no tree: %v", err)
	}
}

func TestSeveralTreesAreAllJudgedAndSorted(t *testing.T) {
	clean := tree(t, map[string]string{"a.org/package.hcl": "x = 1\n"})
	bad := tree(t, map[string]string{"z.org/package.hcl": "zpkx +a\n", "b.org/package.sh": "x\nzpkx +b\nzpkx +c\n"})
	out, err := run("--recipes", clean, "--recipes", bad)
	if err == nil || !strings.Contains(err.Error(), "3 recipe line(s)") {
		t.Fatalf("%q, %v", out, err)
	}
	if strings.Index(out, "package.sh:3:") < strings.Index(out, "package.sh:2:") || strings.Index(out, "b.org") > strings.Index(out, "z.org") || !strings.Contains(out, "package.sh:2:") {
		t.Errorf("not sorted or wrong line: %q", out)
	}
}

func TestAGitDirectoryIsNotRead(t *testing.T) {
	d := tree(t, map[string]string{"a.org/package.hcl": "x\n", ".git/hooks/pre-push.sh": "zpkx +a\n"})
	if out, err := run("--recipes", d); err != nil {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestAFileThatCannotBeOpenedIsAnErrorNotASkip(t *testing.T) {
	d := tree(t, map[string]string{"a.org/package.hcl": "x\n"})
	if err := os.Symlink(filepath.Join(d, "absent"), filepath.Join(d, "b.hcl")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := run("--recipes", d); err == nil {
		t.Fatal("a dangling recipe passed")
	}
}

func TestAnOverlongLineIsAnErrorNotASilentSkip(t *testing.T) {
	d := tree(t, map[string]string{"a.org/package.hcl": strings.Repeat("x", 9*1024*1024) + "\n"})
	if _, err := run("--recipes", d); err == nil {
		t.Fatal("a line the scanner could not read passed")
	}
}

func TestOnlyTheNamedProjectsAreJudged(t *testing.T) {
	d := tree(t, map[string]string{
		"gnu.org/make/package.yml":     "build:\n  - make\n",
		"gnu.org/gcc/libstdcxx/p.hcl":  "x = 1\n",
		"other.org/package.yml":        "- pkgx +openssl.org^3\n",
		"go.dev/package.hcl":           "x = 1\n",
		"toolchain.txt":                "# a list\n\ngnu.org/make   # driver\n  # indented comment\ngnu.org/gcc/libstdcxx@~1.2\n",
		"onlyhere.org/package.hcl":     "zpkx +a\n",
		"onlyhere.org/other/notes.txt": "x\n",
	})
	list := filepath.Join(d, "toolchain.txt")
	out, err := run("--recipes", d, "--projects-from", list, "--project", "go.dev")
	if err != nil || !strings.Contains(out, "3 recipe file(s) read") {
		t.Fatalf("the unrelated project was judged: %q, %v", out, err)
	}
	// The same tree, with the project that does invoke it named.
	if _, err := run("--recipes", d, "--project", "other.org"); err == nil || !strings.Contains(err.Error(), "1 recipe line(s)") {
		t.Fatalf("a named project that invokes zpkx passed: %v", err)
	}
	// A root found in no tree, or found with no recipe in it, was not read.
	if _, err := run("--recipes", d, "--project", "absent.org"); err == nil || !strings.Contains(err.Error(), "no recipe file in any tree") {
		t.Fatalf("a missing root passed: %v", err)
	}
	if _, err := run("--recipes", d, "--project", "onlyhere.org/other"); err == nil || !strings.Contains(err.Error(), "no recipe file in any tree") {
		t.Fatalf("a root with no recipe passed: %v", err)
	}
}

func TestAProjectListThatCannotBeUsedIsRefused(t *testing.T) {
	d := tree(t, map[string]string{"a.org/package.hcl": "x\n", "empty.txt": "# nothing\n", "long.txt": strings.Repeat("x", 70000) + "\n"})
	if _, err := run("--recipes", d, "--projects-from", filepath.Join(d, "absent.txt")); err == nil {
		t.Error("an absent list passed")
	}
	if _, err := run("--recipes", d, "--projects-from", filepath.Join(d, "empty.txt")); err == nil || !strings.Contains(err.Error(), "names no project") {
		t.Errorf("an empty list passed: %v", err)
	}
	if _, err := run("--recipes", d, "--projects-from", filepath.Join(d, "long.txt")); err == nil {
		t.Error("an unreadable list passed")
	}
}

func TestAnUnreadableRecipeInANamedProjectIsAnError(t *testing.T) {
	d := tree(t, map[string]string{"a.org/package.hcl": "x\n"})
	if err := os.Symlink(filepath.Join(d, "absent"), filepath.Join(d, "a.org", "b.hcl")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := run("--recipes", d, "--project", "a.org"); err == nil {
		t.Fatal("a dangling recipe passed")
	}
}

func TestMainExitsNonzeroOnARefusal(t *testing.T) {
	var code int
	osExit = func(c int) { code = c }
	defer func() { osExit = os.Exit }()
	os.Args = []string{"zpkxcensus"}
	main()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
}
