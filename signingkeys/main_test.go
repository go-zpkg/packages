package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-attest/sign"
)

func runIt(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	c := run(args, &out, &errb)
	return c, out.String(), errb.String()
}

// secretFile writes kp's secret key to a private file and returns its path.
func secretFile(t *testing.T, kp *sign.Keypair) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(p, []byte(kp.SecretKeyFile("")), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func gen(t *testing.T) *sign.Keypair {
	t.Helper()
	kp, err := sign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func trusting(t *testing.T, kp *sign.Keypair) {
	t.Helper()
	old := trustedKey
	trustedKey = func() string { return kp.PublicKeyString() }
	t.Cleanup(func() { trustedKey = old })
}

func TestBothKeysAreDerivedAndTheProductionOneMatches(t *testing.T) {
	seed, prod := gen(t), gen(t)
	trusting(t, prod)
	sum := filepath.Join(t.TempDir(), "summary.md")
	c, out, e := runIt("--seed", secretFile(t, seed), "--prod", secretFile(t, prod), "--summary", sum)
	if c != 0 || e != "" {
		t.Fatalf("code=%d err=%q", c, e)
	}
	for _, kp := range []*sign.Keypair{seed, prod} {
		if !strings.Contains(out, kp.PublicKeyFile("")+"key id: "+strings.ToUpper(keyID(kp))) {
			t.Errorf("the public half of %s is missing:\n%s", keyID(kp), out)
		}
		if strings.Contains(out, secretLine(kp)) {
			t.Error("a secret line was printed")
		}
	}
	if !strings.Contains(out, "Matches `artifact.SigningPublicKey`") {
		t.Errorf("no match line:\n%s", out)
	}
	b, err := os.ReadFile(sum)
	if err != nil || string(b) != out {
		t.Errorf("summary differs from stdout: %v", err)
	}
}

func keyID(kp *sign.Keypair) string {
	return strings.TrimSpace(strings.Split(kp.PublicKeyFile(""), "\n")[0][len("untrusted comment: minisign public key "):])
}

func secretLine(kp *sign.Keypair) string {
	return strings.TrimSpace(strings.Split(kp.SecretKeyFile(""), "\n")[1])
}

func TestAProductionKeyThatIsNotTheTrustedOneFails(t *testing.T) {
	seed, prod := gen(t), gen(t)
	trusting(t, gen(t)) // clients trust somebody else
	c, out, e := runIt("--seed", secretFile(t, seed), "--prod", secretFile(t, prod))
	if c != 1 || !strings.Contains(e, "does NOT match") || !strings.Contains(out, "**MISMATCH**") {
		t.Fatalf("code=%d err=%q out=%q", c, e, out)
	}
	if strings.Contains(out+e, secretLine(prod)) {
		t.Error("a secret line was printed")
	}
}

func TestEachKeyIsRefusedWithAFixedPhraseThatNeverQuotesIt(t *testing.T) {
	good := secretFile(t, gen(t))
	trusting(t, gen(t))
	write := func(s string) string {
		p := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, tc := range map[string]struct{ path, want string }{
		"empty":    {write("  \n"), "empty (is the secret set for this run?)"},
		"garbage":  {write("untrusted comment: SENTINEL\nSENTINEL-BODY\n"), "not a secret key"},
		"absent":   {filepath.Join(t.TempDir(), "absent"), "cannot be opened"},
		"toolarge": {write(strings.Repeat("A", maxKeyFile+1)), "too large to be a secret key"},
	} {
		for _, side := range []string{"seed", "prod"} {
			args := []string{"--seed", good, "--prod", good}
			if side == "seed" {
				args[1] = tc.path
			} else {
				args[3] = tc.path
			}
			c, _, e := runIt(args...)
			if c != 1 || !strings.Contains(e, tc.want) || strings.Contains(e, "SENTINEL") {
				t.Errorf("%s/%s: code=%d err=%q", name, side, c, e)
			}
		}
	}
}

func TestArgumentsAreRefused(t *testing.T) {
	for name, args := range map[string][]string{
		"none":     {},
		"no prod":  {"--seed", "x"},
		"no seed":  {"--prod", "x"},
		"argv":     {"--seed", "x", "--prod", "y", "RWsecret"},
		"bad flag": {"--nope"},
	} {
		if c, _, _ := runIt(args...); c != 2 {
			t.Errorf("%s: code=%d", name, c)
		}
	}
}

func TestAnUnwritableSummaryFails(t *testing.T) {
	prod := gen(t)
	trusting(t, prod)
	bad := filepath.Join(t.TempDir(), "no", "such", "dir", "s.md")
	c, _, e := runIt("--seed", secretFile(t, gen(t)), "--prod", secretFile(t, prod), "--summary", bad)
	if c != 1 || !strings.Contains(e, "summary:") {
		t.Errorf("code=%d err=%q", c, e)
	}
}

func TestReadBoundedAndAppendFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a")
	if err := osAppendFile(p, "one\n"); err != nil {
		t.Fatal(err)
	}
	if err := osAppendFile(p, "two\n"); err != nil {
		t.Fatal(err)
	}
	b, err := readBounded(p)
	if err != nil || string(b) != "one\ntwo\n" {
		t.Errorf("append/read: %q %v", b, err)
	}
	// A directory opens but cannot be read.
	if _, err := readBounded(dir); err == nil || err.Error() != "cannot be read" {
		t.Errorf("directory: %v", err)
	}
	if err := osAppendFile(dir, "x"); err == nil {
		t.Error("appended to a directory")
	}
}

func TestTheDefaultTrustedKeyIsArtifactsAndMainRuns(t *testing.T) {
	if k := trustedKey(); len(k) != 56 || !strings.HasPrefix(k, "RW") {
		t.Errorf("trusted key %q", k)
	}
	old := osExit
	defer func() { osExit = old }()
	var got int
	osExit = func(c int) { got = c }
	oldArgs := os.Args
	os.Args = []string{"signingkeys"}
	defer func() { os.Args = oldArgs }()
	main()
	if got != 2 {
		t.Errorf("exit %d", got)
	}
}
