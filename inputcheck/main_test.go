package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const seedKey = "RWSsFdCUPJ7jCHA0LJCPd23ONL4OOKV5efaYVFSbp8jwO/JvWI0oKUYf"

// ok is a dispatch that publishes nothing (test_only), so a test sets only the
// field it is about; the destination rules are tested with okTo.
func ok() Inputs {
	return Inputs{Flags: map[string]string{"test_only": "1"}, HaveZpkgKey: true}
}

func okTo(dest string) Inputs {
	in := ok()
	in.Flags["test_only"] = ""
	in.OCITo = dest
	return in
}

func refused(t *testing.T, name string, in Inputs, want string) {
	t.Helper()
	_, err := Validate(in)
	if err == nil {
		t.Errorf("%s: accepted", name)
		return
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%s: refused for the wrong reason: %v (want %q)", name, err, want)
	}
}

func envOf(r Result) map[string]string {
	got := map[string]string{}
	for _, p := range r.Env {
		got[p[0]] = p[1]
	}
	return got
}

func TestABlankDestinationIsRefusedUnlessTestOnly(t *testing.T) {
	in := ok()
	in.Flags["test_only"] = ""
	refused(t, "blank", in, "publishing requires an explicit destination; set oci_to")
	in.Flags["test_only"] = "0"
	refused(t, "test_only 0", in, "publishing requires an explicit destination; set oci_to")
	// A dist_from naming a registry is a READ source, never a destination.
	in.DistFrom = prodDest
	refused(t, "dist_from is not a destination", in, "publishing requires an explicit destination; set oci_to")
	in = okTo("oci://127.0.0.1:5000/seed")
	if r, err := Validate(in); err != nil || r.Effective != in.OCITo {
		t.Errorf("explicit destination: %v %q", err, r.Effective)
	}
}

func TestGoPkgxIsRefusedAsADestination(t *testing.T) {
	const want = "publishing requires an explicit destination; go-pkgx is refused as one"
	for name, dest := range map[string]string{
		"default":    "oci://ghcr.io/go-pkgx/packages",
		"case":       "oci://ghcr.io/Go-PKGX/packages",
		"other":      "oci://ghcr.io/go-pkgx/seed",
		"other host": "oci://example.org/go-pkgx/x",
	} {
		for _, key := range []bool{false, true} {
			in := okTo(dest)
			in.HaveZpkgKey = key
			refused(t, "oci_to "+name, in, want)
		}
		in := ok()
		in.SeedDist = dest
		refused(t, "seed_dist "+name, in, want)
	}
	// Reading from go-pkgx stays allowed.
	in := okTo("oci://127.0.0.1:5000/seed")
	in.DistFrom = "oci://ghcr.io/go-pkgx/packages"
	r, err := Validate(in)
	if err != nil || envOf(r)["DIST_FROM"] != "oci://ghcr.io/go-pkgx/packages" {
		t.Errorf("dist_from go-pkgx refused: %v", err)
	}
}

func TestPublishToIsTheEffectiveDestination(t *testing.T) {
	r, err := Validate(okTo("oci://127.0.0.1:5000/seed"))
	if err != nil || envOf(r)["PUBLISH_TO"] != "oci://127.0.0.1:5000/seed" {
		t.Errorf("oci_to: %v %v", err, envOf(r))
	}
	in := ok()
	in.Flags["test_only"] = ""
	in.SeedDist = "oci://127.0.0.1:5000/seed"
	r, err = Validate(in)
	if err != nil || envOf(r)["PUBLISH_TO"] != in.SeedDist || envOf(r)["OCI_TO"] != "" {
		t.Errorf("seed_dist: %v %v", err, envOf(r))
	}
	r, err = Validate(ok())
	if v, found := envOf(r)["PUBLISH_TO"]; err != nil || !found || v != "" {
		t.Errorf("test_only: %v %q %v", err, v, found)
	}
}

func TestATestOnlyDispatchIsAccepted(t *testing.T) {
	r, err := Validate(ok())
	if err != nil {
		t.Fatal(err)
	}
	if r.Effective != "" {
		t.Errorf("effective %q", r.Effective)
	}
	got := map[string]string{}
	for _, p := range r.Env {
		got[p[0]] = p[1]
	}
	if got["OVERLAY_URL"] != "https://raw.githubusercontent.com/go-zpkg/recipes/main/projects" || got["DIST_FROM"] != defaultDist {
		t.Errorf("env %v", got)
	}
}

func TestNewlinesAndControlCharactersAreRefusedInEveryField(t *testing.T) {
	set := map[string]func(*Inputs, string){
		"oci_to":             func(i *Inputs, v string) { i.OCITo = v },
		"dist_from":          func(i *Inputs, v string) { i.DistFrom = v },
		"overlay_ref":        func(i *Inputs, v string) { i.OverlayRef = v },
		"seed_dist":          func(i *Inputs, v string) { i.SeedDist = v },
		"mirror_from":        func(i *Inputs, v string) { i.MirrorFrom = v },
		"source_mirror":      func(i *Inputs, v string) { i.SourceMirror = v },
		"glibc":              func(i *Inputs, v string) { i.Glibc = v },
		"versions":           func(i *Inputs, v string) { i.Versions = v },
		"extra_trusted_keys": func(i *Inputs, v string) { i.ExtraKeys = v },
		"require_origin":     func(i *Inputs, v string) { i.RequireOrigin = v },
		"lock":               func(i *Inputs, v string) { i.Lock = v },
		"recipes":            func(i *Inputs, v string) { i.Recipes = v },
		"max_versions":       func(i *Inputs, v string) { i.MaxVersions = v },
		"force":              func(i *Inputs, v string) { i.Flags["force"] = v },
	}
	for name, f := range set {
		for _, bad := range []string{"a\nB=c", "a\rb", "a\x01b", "a\x7fb"} {
			in := ok()
			f(&in, bad)
			if _, err := Validate(in); err == nil {
				t.Errorf("%s accepted %q", name, bad)
			} else if !strings.Contains(err.Error(), name) {
				t.Errorf("%s: the refusal does not name the field: %v", name, err)
			}
		}
	}
	in := ok()
	in.OCITo = "oci://x\n"
	refused(t, "newline text", in, "holds a newline")
	in.OCITo = "oci://x\x01"
	refused(t, "control text", in, "control character")
}

func TestRegistries(t *testing.T) {
	in := ok()
	in.OCITo = "https://example.org/x"
	refused(t, "scheme", in, "not an oci:// registry")
	in = ok()
	in.DistFrom = "oci://host/repo;rm"
	refused(t, "dist_from chars", in, "made of A-Za-z0-9./:_@-")
	in = ok()
	in.OCITo = "oci://127.0.0.1:5000/seed"
	if _, err := Validate(in); err != nil {
		t.Errorf("a loopback registry refused: %v", err)
	}
	in = ok()
	in.SeedDist = "http://127.0.0.1:5000"
	refused(t, "seed scheme", in, "not an oci:// registry (seed_dist)")
	in.SeedDist = "oci://127.0.0.1:5000/seed;x"
	refused(t, "seed chars", in, "seed_dist")
	in.SeedDist = "oci://[::1]:5000/seed"
	if _, err := Validate(in); err != nil {
		t.Errorf("a bracketed IPv6 seed refused: %v", err)
	}
	in.OCITo = "oci://x/y"
	refused(t, "seed with oci_to", in, "cannot be combined")
	in = ok()
	in.SeedDist = "oci://h/r"
	in.DistFrom = "oci://x/y"
	refused(t, "seed with dist_from", in, "cannot be combined")
	in = ok()
	in.SourceMirror = "https://x"
	refused(t, "source_mirror", in, "source_mirror")
	in.SourceMirror = "oci://ghcr.io/x/src"
	if _, err := Validate(in); err != nil {
		t.Errorf("source_mirror refused: %v", err)
	}
	in = ok()
	in.MirrorFrom = "ftp://x"
	refused(t, "mirror_from scheme", in, "mirror_from")
	in.MirrorFrom = "https://dist.example.org/a;b"
	refused(t, "mirror_from chars", in, "mirror_from")
	in.MirrorFrom = "https://dist.example.org/a"
	if _, err := Validate(in); err != nil {
		t.Errorf("mirror_from refused: %v", err)
	}
}

func TestOverlayRef(t *testing.T) {
	for _, bad := range []string{"-x", "a..b", "a b", "a;b", "a$(b)"} {
		in := ok()
		in.OverlayRef = bad
		refused(t, "overlay_ref "+bad, in, "overlay_ref")
	}
	in := ok()
	in.OverlayRef = "0123456789abcdef0123456789abcdef01234567"
	r, err := Validate(in)
	if err != nil || !strings.Contains(envOf(r)["OVERLAY_URL"], in.OverlayRef) {
		t.Errorf("%v %v", r, err)
	}
}

func TestScalarFields(t *testing.T) {
	cases := []struct {
		name string
		set  func(*Inputs, string)
		bad  []string
		good []string
	}{
		{"glibc", func(i *Inputs, v string) { i.Glibc = v }, []string{"2.27.0;x", "x", "2."}, []string{"2.27.0", "2.41"}},
		{"versions", func(i *Inputs, v string) { i.Versions = v }, []string{"-rf", "^3;x", "$(x)", strings.Repeat("1", 129)}, []string{"^3", "=1.10.0", ">=1.2 <2"}},
		{"max_versions", func(i *Inputs, v string) { i.MaxVersions = v }, []string{"-1", "1;2", "x", "1234567"}, []string{"1", "20"}},
		{"lock", func(i *Inputs, v string) { i.Lock = v }, []string{"/etc/passwd", "../x", "-x", "a;b", "a b"}, []string{"locks/seed.lock.hcl"}},
		{"recipes", func(i *Inputs, v string) { i.Recipes = v }, []string{"a;b", "-x", "ok.org a;b", "$(x)"}, []string{"lz4.org", "a.org  b.org/c+d"}},
		{"force", func(i *Inputs, v string) { i.Flags["force"] = v }, []string{"a;b", "123456789", "a b"}, []string{"0", "1", "true"}},
	}
	for _, c := range cases {
		for _, b := range c.bad {
			in := ok()
			c.set(&in, b)
			refused(t, c.name+" "+b, in, "")
		}
		for _, g := range c.good {
			in := ok()
			c.set(&in, g)
			if _, err := Validate(in); err != nil {
				t.Errorf("%s %q refused: %v", c.name, g, err)
			}
		}
	}
}

func TestRequireOrigin(t *testing.T) {
	in := ok()
	in.RequireOrigin = "0"
	refused(t, "0", in, "blank or 1")
	in.RequireOrigin = "1"
	refused(t, "no mirror", in, "needs mirror_from")
	in.MirrorFrom = "https://dist.example.org/a"
	if _, err := Validate(in); err != nil {
		t.Errorf("refused: %v", err)
	}
}

func TestEveryRecipeInTheRepositoryPassesTheWhitelist(t *testing.T) {
	f, err := os.Open("../recipes.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		n++
		in := ok()
		in.Recipes = l
		if _, err := Validate(in); err != nil {
			t.Errorf("recipes.txt entry %q refused: %v", l, err)
		}
	}
	if n < 100 {
		t.Errorf("only %d recipes read", n)
	}
}

func TestTheEffectiveDestinationDecidesTheGoZpkgRule(t *testing.T) {
	// The destination is oci_to or seed_dist; dist_from is only read from.
	for name, set := range map[string]func(*Inputs){
		"oci_to":    func(i *Inputs) { i.OCITo = prodDest },
		"seed_dist": func(i *Inputs) { i.SeedDist = prodDest },
	} {
		in := ok()
		set(&in)
		in.HaveZpkgKey = false
		refused(t, name, in, "needs secrets.ZPKG_SIGNING_KEY")
		in.HaveZpkgKey = true
		r, err := Validate(in)
		if err != nil || r.Effective != prodDest || envOf(r)["SIGN_PROD"] != "true" {
			t.Errorf("%s with the key: %v %q", name, err, r.Effective)
		}
	}
	// Anything else that names go-zpkg is refused, with or without the key:
	// neither key may sign a destination that only resembles production.
	for name, set := range map[string]func(*Inputs){
		"other repo":  func(i *Inputs) { i.OCITo = "oci://ghcr.io/go-zpkg/seed" },
		"case":        func(i *Inputs) { i.OCITo = "oci://ghcr.io/Go-ZPKG/packages" },
		"suffix":      func(i *Inputs) { i.OCITo = prodDest + "x" },
		"trailing /":  func(i *Inputs) { i.OCITo = prodDest + "/" },
		"other host":  func(i *Inputs) { i.OCITo = "oci://example.org/go-zpkg/packages" },
		"seed":        func(i *Inputs) { i.SeedDist = "oci://ghcr.io/go-zpkg/seed" },
		"shorter org": func(i *Inputs) { i.OCITo = "oci://ghcr.io/go-zpkg" },
	} {
		for _, key := range []bool{false, true} {
			in := ok()
			set(&in)
			in.HaveZpkgKey = key
			refused(t, name, in, "must be exactly")
		}
	}
	// A destination that does not name go-zpkg is signed with the other key.
	for _, dest := range []string{"oci://ghcr.io/other/packages", "oci://127.0.0.1:5000/seed"} {
		in := ok()
		in.OCITo = dest
		in.HaveZpkgKey = true
		r, err := Validate(in)
		if err != nil || envOf(r)["SIGN_PROD"] != "false" {
			t.Errorf("%s: %v SIGN_PROD=%q", dest, err, envOf(r)["SIGN_PROD"])
		}
	}
	in := ok()
	in.OCITo = "oci://ghcr.io/other/packages"
	in.DistFrom = "oci://ghcr.io/go-zpkg/packages"
	r, err := Validate(in)
	if err != nil || r.Effective != in.OCITo {
		t.Errorf("a go-zpkg SOURCE with another destination must pass: %v %q", err, r.Effective)
	}
	in = ok()
	in.SeedDist = "oci://127.0.0.1:5000/seed"
	r, err = Validate(in)
	if err != nil || r.Effective != in.SeedDist {
		t.Errorf("seed effective: %v %q", err, r.Effective)
	}
}

func TestExtraTrustedKeys(t *testing.T) {
	good := "seed:" + seedKey
	if len(seedKey) != 56 {
		t.Fatalf("seed key is %d characters", len(seedKey))
	}
	in := ok()
	in.ExtraKeys = good
	r, err := Validate(in)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range r.Env {
		if p[0] == "EXTRA_KEYS" && p[1] == good {
			found = true
		}
	}
	if !found {
		t.Error("EXTRA_KEYS is not exported")
	}
	in.ExtraKeys = good + " other:" + seedKey
	if _, err := Validate(in); err != nil {
		t.Errorf("two keys refused: %v", err)
	}
	in.OCITo = "oci://ghcr.io/go-zpkg/packages"
	refused(t, "with go-zpkg", in, "cannot be combined")
	many := strings.Repeat("k:"+seedKey+" ", 9)
	for name, v := range map[string]string{
		"char":      "seed:" + seedKey + ";",
		"no colon":  "seed",
		"bad name":  ":" + seedKey,
		"long name": strings.Repeat("n", 65) + ":" + seedKey,
		"no RW":     "seed:" + "XX" + seedKey[2:],
		"short":     "seed:" + seedKey[:55],
		"long":      "seed:" + seedKey + "A",
		"many":      many,
		"tab":       "seed:" + seedKey + "\t",
	} {
		in := ok()
		in.ExtraKeys = v
		refused(t, name, in, "extra_trusted_keys")
	}
	in = ok()
	in.ExtraKeys = "seed:RW" + strings.Repeat("A", 53) + "="
	refused(t, "padding", in, "character outside")
	in.ExtraKeys = "seed:RW" + strings.Repeat("A", 53) + "+"
	if _, err := Validate(in); err != nil {
		t.Errorf("a 56-character key with + refused: %v", err)
	}
	in.ExtraKeys = "seed:RW" + strings.Repeat("A", 53) + "-"
	refused(t, "dash in key", in, "RW plus 54 base64")
	// The boundary of the count and of every character class.
	in.ExtraKeys = strings.Repeat("Zz9:"+seedKey+" ", 8)
	if _, err := Validate(in); err != nil {
		t.Errorf("eight keys refused: %v", err)
	}
	in.ExtraKeys += "Zz9:" + seedKey
	refused(t, "nine keys", in, "more than 8")
	in.ExtraKeys = "seed"
	refused(t, "no colon", in, "is not name:key")
	in.ExtraKeys = "seed:" + seedKey[:2] + "ZzZzZzZz9999" + strings.Repeat("a", 42)
	if _, err := Validate(in); err != nil {
		t.Errorf("characters at the edge of the classes refused: %v", err)
	}
}

func TestTheZpkgKeyIsRequiredAndNeverWidenedByExtraKeys(t *testing.T) {
	in := ok()
	in.OCITo = "oci://ghcr.io/go-zpkg/packages"
	in.HaveZpkgKey = false
	refused(t, "no key", in, "ZPKG_SIGNING_KEY")
}

func TestWriteEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	if err := writeEnv(p, [][2]string{{"A", "1"}, {"B", "x y"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "A=1\nB=x y\n" {
		t.Errorf("%q", b)
	}
	for _, bad := range [][2]string{{"A", "1\nB=2"}, {"A\n", "1"}, {"A=B", "1"}, {"", "1"}, {"A", "1\r"}} {
		if err := writeEnv(p, [][2]string{bad}); err == nil {
			t.Errorf("wrote %q", bad)
		}
	}
	if b2, _ := os.ReadFile(p); string(b2) != string(b) {
		t.Error("a refused write changed the file")
	}
	if err := writeEnv(filepath.Join(t.TempDir(), "no", "dir", "env"), [][2]string{{"A", "1"}}); err == nil {
		t.Error("an unwritable path was accepted")
	} else if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "cannot find") {
		t.Errorf("the open error was replaced: %v", err)
	}
}

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"IN_OCI_TO", "IN_DIST_FROM", "IN_OVERLAY_REF", "IN_SEED_DIST", "IN_MIRROR_FROM", "IN_SOURCE_MIRROR", "IN_GLIBC", "IN_VERSIONS", "IN_EXTRA_KEYS", "IN_REQUIRE_ORIGIN", "IN_LOCK", "IN_RECIPES", "IN_MAX_VERSIONS", "HAVE_ZPKG_KEY", "IN_FORCE", "IN_REBUILD", "IN_NO_CLOSURE", "IN_PIN_TO_DEPENDENTS", "IN_CLEAN_BUILD_ROOT", "IN_BOOTSTRAP", "IN_TEST_ONLY", "IN_SOVEREIGN"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func runCmd(args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func TestTheCommandWritesTheValidatedEnvironment(t *testing.T) {
	setEnv(t, map[string]string{"IN_OVERLAY_REF": "abc123", "IN_SEED_DIST": "oci://127.0.0.1:5000/seed", "IN_FORCE": "0", "HAVE_ZPKG_KEY": "true"})
	p := filepath.Join(t.TempDir(), "env")
	out, _, code := runCmd("--github-env", p)
	if code != 0 || !strings.Contains(out, "publishing to oci://127.0.0.1:5000/seed") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "SEED_DIST=oci://127.0.0.1:5000/seed\n") || !strings.Contains(string(b), "recipes/abc123/projects\n") {
		t.Errorf("env file %q", b)
	}
}

func TestCheckOnlyWritesNothing(t *testing.T) {
	setEnv(t, map[string]string{"IN_OCI_TO": "oci://127.0.0.1:5000/seed"})
	p := filepath.Join(t.TempDir(), "env")
	if _, _, code := runCmd("--check-only", "--github-env", p); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("--check-only wrote the file")
	}
}

func TestTheCommandRefusesABlankDestinationAndGoPkgx(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"blank":   nil,
		"go-pkgx": {"IN_OCI_TO": "oci://ghcr.io/go-pkgx/packages"},
	} {
		setEnv(t, kv)
		p := filepath.Join(t.TempDir(), "env")
		_, errb, code := runCmd("--github-env", p)
		if code != 1 || !strings.Contains(errb, "publishing requires an explicit destination") {
			t.Errorf("%s: exit %d, %q", name, code, errb)
		}
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s: a refused dispatch wrote the file", name)
		}
	}
}

func TestARefusalExitsNonZeroAndWritesNothing(t *testing.T) {
	setEnv(t, map[string]string{"IN_OCI_TO": "oci://x\nGITHUB_PATH=/x"})
	p := filepath.Join(t.TempDir(), "env")
	_, errb, code := runCmd("--github-env", p)
	if code != 1 || !strings.Contains(errb, "::error::") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a refused dispatch wrote the file")
	}
}

func TestNoEnvironmentFileIsAnError(t *testing.T) {
	setEnv(t, map[string]string{"IN_OCI_TO": "oci://127.0.0.1:5000/seed"})
	t.Setenv("GITHUB_ENV", "")
	if _, errb, code := runCmd("--github-env", ""); code == 0 || !strings.Contains(errb, "no --github-env") {
		t.Errorf("no environment file: exit %d, %q", code, errb)
	}
}

func TestPositionalArgumentsAreRefused(t *testing.T) {
	if _, _, code := runCmd("x"); code == 0 {
		t.Error("an argument was accepted")
	}
}

func TestMainExitsWithTheRunStatus(t *testing.T) {
	old, oldArgs := osExit, os.Args
	defer func() { osExit, os.Args = old, oldArgs }()
	got := -1
	osExit = func(c int) { got = c }
	os.Args = []string{"inputcheck", "--bogus"}
	main()
	if got != 1 {
		t.Errorf("exit %d", got)
	}
}

func TestIsLoopbackRegistryIsExact(t *testing.T) {
	for dist, want := range map[string]bool{
		"oci://127.0.0.1:5000/seed":      true,
		"oci://127.0.0.1/seed":           true,
		"oci://127.0.0.1:99999/seed":     true,
		"oci://127.0.0.1:0/seed":         true,
		"oci://127.0.0.1":                true,
		"oci://localhost:5000/seed":      true,
		"oci://[::1]:5000/seed":          true,
		"oci://[::1]/seed":               true,
		"oci://127.0.0.2:5000/seed":      false,
		"oci://127.evil.example/seed":    false,
		"oci://127.0.0.1@evil.example/x": false,
		"oci://localhost.example/seed":   false,
		"oci://LOCALHOST:5000/seed":      false,
		"oci://127.0.0.1:/seed":          false,
		"oci://127.0.0.1:50a0/seed":      false,
		"oci://127.0.0.1:500000/seed":    false,
		"oci://127.0.0.1x:5000/seed":     false,
		"oci://[::1/seed":                false,
		"oci://[::1]x/seed":              false,
		"oci://::1/seed":                 false,
		"https://127.0.0.1:5000/seed":    false,
		"127.0.0.1:5000/seed":            false,
		"":                               false,
	} {
		if got := isLoopbackRegistry(dist); got != want {
			t.Errorf("isLoopbackRegistry(%q) = %v, want %v", dist, got, want)
		}
	}
}

func TestSeedLoopbackIsExported(t *testing.T) {
	in := ok()
	if envOf(mustValidate(t, in))["SEED_LOOPBACK"] != "false" {
		t.Error("a blank seed_dist is not loopback")
	}
	in.SeedDist = "oci://127.0.0.1:5000/seed"
	if envOf(mustValidate(t, in))["SEED_LOOPBACK"] != "true" {
		t.Error("a loopback seed_dist is loopback")
	}
	in.SeedDist = "oci://127.evil.example/seed"
	if envOf(mustValidate(t, in))["SEED_LOOPBACK"] != "false" {
		t.Error("a lookalike seed_dist is not loopback")
	}
}

func mustValidate(t *testing.T, in Inputs) Result {
	t.Helper()
	r, err := Validate(in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ⛔ WHO PUSHES. The self-hosted build job holds no credential, so what it can
// push is exactly what lives on the pool's own machine or network; everything
// else is pushed by a fresh GitHub-hosted job that holds the credentials.
func TestPublishLane(t *testing.T) {
	for dest, want := range map[string]string{
		"":                                "none",
		"oci://127.0.0.1:5000/seed":       "lan",
		"oci://localhost:5000/seed":       "lan",
		"oci://[::1]:5000/seed":           "lan",
		"oci://http://10.46.81.1:5111":    "lan",
		"oci://http://10.46.81.1:5111/a":  "lan",
		"oci://ghcr.io/go-zpkg/packages":  "hosted",
		"oci://ghcr.io/someone/else":      "hosted",
		"oci://127.evil.example/seed":     "hosted",
		"oci://http://10.46.81.1:5112/a":  "hosted",
		"oci://http://10.46.81.10:5111/a": "hosted",
		"oci://http://10.46.81.1:51111/a": "hosted",
		"oci://http://10.46.81.1:5111@h":  "hosted",
	} {
		if got := PublishLane(dest); got != want {
			t.Errorf("PublishLane(%q) = %q, want %q", dest, got, want)
		}
	}
}

func TestThePublishLaneIsExported(t *testing.T) {
	for dest, want := range map[string]string{
		"oci://ghcr.io/go-zpkg/packages": "hosted",
		"oci://127.0.0.1:5000/seed":      "lan",
	} {
		r, err := Validate(okTo(dest))
		if err != nil {
			t.Fatal(err)
		}
		if got := envOf(r)["PUBLISH_LANE"]; got != want {
			t.Errorf("%s: PUBLISH_LANE=%q, want %q", dest, got, want)
		}
	}
	r, err := Validate(ok())
	if err != nil || envOf(r)["PUBLISH_LANE"] != "none" {
		t.Errorf("test_only: %v, lane %q", err, envOf(r)["PUBLISH_LANE"])
	}
}

// The build job holds no secret, so it cannot know whether the production key
// exists: --no-key-check lets it validate, and the hosted jobs, which do hold
// the key, still refuse.
func TestNoKeyCheckOnlyRelaxesTheKeyPresenceRefusal(t *testing.T) {
	in := okTo(prodDest)
	in.HaveZpkgKey = false
	refused(t, "no key", in, "needs secrets.ZPKG_SIGNING_KEY")
	in.NoKeyCheck = true
	if _, err := Validate(in); err != nil {
		t.Errorf("--no-key-check still refused: %v", err)
	}
	in.ExtraKeys = "k:" + seedKey
	refused(t, "extra keys with prod", in, "must not widen trust")
	in = okTo("oci://ghcr.io/go-zpkg/other")
	in.NoKeyCheck = true
	refused(t, "look-alike", in, "must be exactly")
}

func TestTheLaneFlagPrintsTheLane(t *testing.T) {
	out, _, code := runCmd("--lane", "oci://ghcr.io/go-zpkg/packages")
	if code != 0 || strings.TrimSpace(out) != "hosted" {
		t.Errorf("exit %d, %q", code, out)
	}
	out, _, code = runCmd("--lane", "")
	if code != 0 || strings.TrimSpace(out) != "none" {
		t.Errorf("blank: exit %d, %q", code, out)
	}
	if _, _, code = runCmd("--lane", "oci://x\ny"); code != 1 {
		t.Errorf("a newline in the destination was accepted: exit %d", code)
	}
}

func TestTheNoKeyCheckFlagReachesValidation(t *testing.T) {
	setEnv(t, map[string]string{"IN_OCI_TO": prodDest})
	if _, _, code := runCmd("--check-only"); code != 1 {
		t.Errorf("without the flag and without a key: exit %d, want 1", code)
	}
	if _, _, code := runCmd("--check-only", "--no-key-check"); code != 0 {
		t.Errorf("with --no-key-check: exit %d, want 0", code)
	}
}
