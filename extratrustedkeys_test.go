package main

import (
	"os"
	"strings"
	"testing"
)

// go-zpkg/packages#440: generation 1 of the bootstrap must verify what
// generation 0 signed with a throwaway seed key, so a dispatch can NAME extra
// trusted keys. ZPKG_EXTRA_TRUSTED_KEYS (go-zpkg/artifact trust.go) reads a
// whitespace-separated `name:RW<base64>` list.
//
// These tests pin the wiring, because every link fails SILENTLY: a variable
// missing from the workflow env is an empty trust set extension, and one
// missing from sudo's --preserve-env is dropped at the chroot.

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestExtraTrustedKeysCrossesSudoIntoTheChroot(t *testing.T) {
	preserved := preserveList(t, readFile(t, "builder/sovereign-build.sh"))
	if !preserved["ZPKG_EXTRA_TRUSTED_KEYS"] {
		t.Error("ZPKG_EXTRA_TRUSTED_KEYS is not on sovereign-build.sh's --preserve-env list: " +
			"zpkx inside the chroot would verify without the extra key")
	}
}

func TestExtraTrustedKeysIsDeclaredExportedAndValidated(t *testing.T) {
	for _, f := range []string{".github/workflows/build.yml", ".github/workflows/sovereign.yml"} {
		src := readFile(t, f)
		for _, want := range []string{
			"      extra_trusted_keys:\n",
			"  ZPKG_EXTRA_TRUSTED_KEYS: ${{ github.event.inputs.extra_trusted_keys }}\n",
			"IN_EXTRA_KEYS: ${{ github.event.inputs.extra_trusted_keys }}",
			"extra_trusted_keys cannot be combined with an oci_to naming go-zpkg",
			`[ "${#key}" -eq 56 ]`,
		} {
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", f, want)
			}
		}
		// The refusal must use the same broad match as the signing-key choice,
		// so the two cannot disagree about what "a go-zpkg destination" is.
		if !strings.Contains(src, "*go-zpkg*) echo \"::error::extra_trusted_keys cannot") {
			t.Errorf("%s: the go-zpkg refusal does not use the *go-zpkg* match", f)
		}
	}
}

// The seed key the bootstrap trusts is 56 base64 characters after RW, like
// every minisign public key; the validation's length check must accept it.
func TestTheSeedKeyPassesTheLengthCheck(t *testing.T) {
	const seed = "RWSsFdCUPJ7jCHA0LJCPd23ONL4OOKV5efaYVFSbp8jwO/JvWI0oKUYf"
	if len(seed) != 56 {
		t.Fatalf("the seed key is %d characters, the workflows require 56", len(seed))
	}
}
