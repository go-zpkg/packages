// Command inputcheck validates the dispatch inputs of build.yml and
// sovereign.yml, and writes the validated spelling to $GITHUB_ENV.
//
// Inputs arrive as environment (IN_OCI_TO, IN_SEED_DIST, ...), never
// interpolated into a script. Every value is checked against a whitelist BEFORE
// anything is written to $GITHUB_ENV, because a line written there becomes an
// environment variable of every later step: a value holding a newline would
// write a second variable of the sender's choosing. Newlines and every other
// control character are refused explicitly, whatever the field.
//
// The rules live here, once, instead of as shell `case` blocks copied into two
// jobs of two workflows, so the setup job (which only decides) and the build
// job (which also writes) cannot disagree.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var osExit = os.Exit

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	cmd := RootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.Execute(); err != nil {
		return 1
	}
	return 0
}

// Inputs is every dispatch input the workflows hand to a shell or write to the
// environment. A field that was not dispatched is "".
type Inputs struct {
	OCITo         string
	DistFrom      string
	OverlayRef    string
	SeedDist      string
	MirrorFrom    string
	SourceMirror  string
	Glibc         string
	Versions      string
	ExtraKeys     string
	RequireOrigin string
	Lock          string
	Recipes       string
	MaxVersions   string
	Flags         map[string]string // blank-or-short switches: force, rebuild, ...
	HaveZpkgKey   bool
}

const (
	defaultDist = "oci://ghcr.io/go-pkgx/packages"
)

var (
	registryRe   = regexp.MustCompile(`^oci://[A-Za-z0-9./:_@-]+$`)
	seedRe       = regexp.MustCompile(`^oci://[A-Za-z0-9./:_@\[\]-]+$`)
	mirrorRe     = regexp.MustCompile(`^(https?|oci)://[A-Za-z0-9./:_@-]+$`)
	overlayRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	glibcRe      = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,3}$`)
	versionsRe   = regexp.MustCompile(`^[A-Za-z0-9.^~=<>*,_+ -]{1,128}$`)
	lockRe       = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	recipeRe     = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
	digitsRe     = regexp.MustCompile(`^[0-9]{1,6}$`)
	flagRe       = regexp.MustCompile(`^[A-Za-z0-9]{1,8}$`)
	keyNameRe    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	keyMaterial  = regexp.MustCompile(`^RW[A-Za-z0-9+/]{54}$`)
	maxExtraKeys = 8
)

// noControl refuses a value holding a newline or any other control character.
func noControl(field, v string) error {
	for _, r := range v {
		switch {
		case r == '\n' || r == '\r':
			return fmt.Errorf("%s holds a newline", field)
		case r < 0x20 || r == 0x7f:
			return fmt.Errorf("%s holds a control character (%U)", field, r)
		}
	}
	return nil
}

func badChars(field, v, set string) error {
	return fmt.Errorf("%s '%s' is not %s", field, v, set)
}

// Result is what the build job exports to its later steps.
type Result struct {
	// Env is written to $GITHUB_ENV, in order.
	Env [][2]string
	// Effective is the registry a run publishes to: oci_to, else seed_dist
	// (which `zpkb factory --to "${OCI_TO:-$ZPKG_DIST}"` falls back to), else
	// the registry the run reads.
	Effective string
}

// Validate checks every field and returns what to export. workflow is "build"
// or "sovereign": the sovereign workflow has no seed_dist, mirror_from or
// source_mirror, and a blank oci_to means the default registry there.
func Validate(in Inputs) (Result, error) {
	var r Result
	fields := []struct{ name, v string }{
		{"oci_to", in.OCITo}, {"dist_from", in.DistFrom}, {"overlay_ref", in.OverlayRef},
		{"seed_dist", in.SeedDist}, {"mirror_from", in.MirrorFrom}, {"source_mirror", in.SourceMirror},
		{"glibc", in.Glibc}, {"versions", in.Versions}, {"extra_trusted_keys", in.ExtraKeys},
		{"require_origin", in.RequireOrigin}, {"lock", in.Lock}, {"recipes", in.Recipes},
		{"max_versions", in.MaxVersions},
	}
	for k, v := range in.Flags {
		fields = append(fields, struct{ name, v string }{k, v})
	}
	for _, f := range fields {
		if err := noControl(f.name, f.v); err != nil {
			return r, err
		}
	}
	for _, f := range []struct{ name, v string }{{"oci_to", in.OCITo}, {"dist_from", in.DistFrom}} {
		if f.v == "" {
			continue
		}
		if !strings.HasPrefix(f.v, "oci://") {
			return r, fmt.Errorf("'%s' is not an oci:// registry (%s)", f.v, f.name)
		}
		if !registryRe.MatchString(f.v) {
			return r, badChars(f.name, f.v, "made of A-Za-z0-9./:_@-")
		}
	}
	if in.SeedDist != "" {
		if !strings.HasPrefix(in.SeedDist, "oci://") {
			return r, fmt.Errorf("'%s' is not an oci:// registry (seed_dist)", in.SeedDist)
		}
		if !seedRe.MatchString(in.SeedDist) {
			return r, badChars("seed_dist", in.SeedDist, "made of A-Za-z0-9./:_@[]-")
		}
	}
	if in.SourceMirror != "" && !registryRe.MatchString(in.SourceMirror) {
		return r, badChars("source_mirror", in.SourceMirror, "an oci:// registry made of A-Za-z0-9./:_@-")
	}
	if in.MirrorFrom != "" && !mirrorRe.MatchString(in.MirrorFrom) {
		return r, badChars("mirror_from", in.MirrorFrom, "an http(s):// or oci:// dist made of A-Za-z0-9./:_@-")
	}
	ref := in.OverlayRef
	if ref == "" {
		ref = "main"
	}
	if strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") || !overlayRe.MatchString(ref) {
		return r, fmt.Errorf("overlay_ref '%s' is not a branch, tag or SHA", ref)
	}
	if in.Glibc != "" && !glibcRe.MatchString(in.Glibc) {
		return r, badChars("glibc", in.Glibc, "a version such as 2.27.0")
	}
	if in.Versions != "" && (strings.HasPrefix(in.Versions, "-") || !versionsRe.MatchString(in.Versions)) {
		return r, badChars("versions", in.Versions, "a pkgx constraint (A-Za-z0-9.^~=<>*,_+ and spaces, not starting with -)")
	}
	if in.MaxVersions != "" && !digitsRe.MatchString(in.MaxVersions) {
		return r, badChars("max_versions", in.MaxVersions, "a number")
	}
	if in.Lock != "" {
		if !lockRe.MatchString(in.Lock) || strings.HasPrefix(in.Lock, "-") || strings.HasPrefix(in.Lock, "/") || strings.Contains(in.Lock, "..") {
			return r, badChars("lock", in.Lock, "a relative path inside this repository")
		}
	}
	for _, p := range strings.Fields(in.Recipes) {
		if !recipeRe.MatchString(p) || strings.HasPrefix(p, "-") {
			return r, badChars("recipes entry", p, "a project name (A-Za-z0-9._/+-)")
		}
	}
	for k, v := range in.Flags {
		if v != "" && !flagRe.MatchString(v) {
			return r, badChars(k, v, "a short alphanumeric switch")
		}
	}
	switch in.RequireOrigin {
	case "", "1":
	default:
		return r, fmt.Errorf("require_origin must be blank or 1, not '%s'", in.RequireOrigin)
	}
	if in.RequireOrigin != "" && in.MirrorFrom == "" {
		return r, errors.New("require_origin reads the provenance of the artifact being copied, so it needs mirror_from")
	}
	if in.SeedDist != "" && (in.OCITo != "" || in.DistFrom != "") {
		return r, errors.New("seed_dist already names the one registry a seed run reads and writes; oci_to and dist_from cannot be combined with it")
	}
	if err := checkExtraKeys(in.ExtraKeys); err != nil {
		return r, err
	}

	distFrom := in.DistFrom
	if distFrom == "" {
		distFrom = defaultDist
	}
	// The destination `zpkb factory --to "${OCI_TO:-$ZPKG_DIST}"` actually
	// publishes to, not the input that may be blank.
	switch {
	case in.OCITo != "":
		r.Effective = in.OCITo
	case in.SeedDist != "":
		r.Effective = in.SeedDist
	default:
		r.Effective = distFrom
	}
	if looksGoZpkg(r.Effective) {
		if !in.HaveZpkgKey {
			return r, fmt.Errorf("publishing to %s needs secrets.ZPKG_SIGNING_KEY, which is empty. The old ARTIFACT_SIGNING_KEY never signs go-zpkg artifacts", r.Effective)
		}
		if in.ExtraKeys != "" {
			return r, errors.New("extra_trusted_keys cannot be combined with a destination naming go-zpkg: a promotion into go-zpkg must not widen trust, it re-signs with ZPKG_SIGNING_KEY")
		}
	}
	r.Env = [][2]string{
		{"OCI_TO", in.OCITo},
		{"DIST_FROM", distFrom},
		{"OVERLAY_URL", "https://raw.githubusercontent.com/go-zpkg/recipes/" + ref + "/projects"},
		{"SEED_DIST", in.SeedDist},
		// Validated public keys. Steps that install or verify hand them to
		// zpkb as ZPKG_EXTRA_TRUSTED_KEYS in their own env, so that the build
		// step is not given a trust variable the dispatch did not mean for it.
		{"EXTRA_KEYS", in.ExtraKeys},
	}
	return r, nil
}

// looksGoZpkg is deliberately the broad match: a destination that merely
// contains the name is treated as go-zpkg and refused without the new key.
func looksGoZpkg(dest string) bool { return strings.Contains(strings.ToLower(dest), "go-zpkg") }

// checkExtraKeys validates `name:RW<base64>` entries separated by whitespace
// (go-zpkg/artifact trust.go ParseSigners).
func checkExtraKeys(v string) error {
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._:+/ -", r)) {
			return errors.New("extra_trusted_keys holds a character outside A-Za-z0-9._:+/ - and the space separator")
		}
	}
	entries := strings.Fields(v)
	if len(entries) > maxExtraKeys {
		return fmt.Errorf("extra_trusted_keys names %d keys, more than %d", len(entries), maxExtraKeys)
	}
	for _, e := range entries {
		name, key, ok := strings.Cut(e, ":")
		if !ok {
			return fmt.Errorf("extra_trusted_keys entry '%s' is not name:key", e)
		}
		if !keyNameRe.MatchString(name) {
			return fmt.Errorf("extra_trusted_keys name '%s' is empty, longer than 64 or outside A-Za-z0-9._-", name)
		}
		if !keyMaterial.MatchString(key) {
			return fmt.Errorf("extra_trusted_keys key for '%s' is not RW plus 54 base64 characters, 56 in all (a minisign public key)", name)
		}
	}
	return nil
}

// writeEnv appends validated pairs to the $GITHUB_ENV file. A value holding a
// newline is refused here too, so that no future caller can write a second
// variable by accident.
func writeEnv(path string, pairs [][2]string) error {
	var b strings.Builder
	for _, p := range pairs {
		if strings.ContainsAny(p[0]+p[1], "\n\r") || strings.Contains(p[0], "=") || p[0] == "" {
			return fmt.Errorf("refusing to write %q to the environment file", p[0])
		}
		b.WriteString(p[0] + "=" + p[1] + "\n")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(b.String())
	return errors.Join(werr, f.Close())
}

func fromEnv() Inputs {
	g := os.Getenv
	in := Inputs{
		OCITo: g("IN_OCI_TO"), DistFrom: g("IN_DIST_FROM"), OverlayRef: g("IN_OVERLAY_REF"),
		SeedDist: g("IN_SEED_DIST"), MirrorFrom: g("IN_MIRROR_FROM"), SourceMirror: g("IN_SOURCE_MIRROR"),
		Glibc: g("IN_GLIBC"), Versions: g("IN_VERSIONS"), ExtraKeys: g("IN_EXTRA_KEYS"),
		RequireOrigin: g("IN_REQUIRE_ORIGIN"), Lock: g("IN_LOCK"), Recipes: g("IN_RECIPES"),
		MaxVersions: g("IN_MAX_VERSIONS"), HaveZpkgKey: g("HAVE_ZPKG_KEY") == "true",
		Flags: map[string]string{},
	}
	for _, n := range []string{"FORCE", "REBUILD", "NO_CLOSURE", "PIN_TO_DEPENDENTS", "CLEAN_BUILD_ROOT", "BOOTSTRAP", "TEST_ONLY", "SOVEREIGN"} {
		in.Flags[strings.ToLower(n)] = g("IN_" + n)
	}
	return in
}

// RootCmd builds the command; tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var envFile string
	var checkOnly bool
	cmd := &cobra.Command{
		Use:          "inputcheck [--check-only] [--github-env FILE]",
		Short:        "validate the dispatch inputs read from IN_* and export them to $GITHUB_ENV",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := Validate(fromEnv())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "::error::%v\n", err)
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "inputs valid; publishing to %s\n", res.Effective)
			if checkOnly {
				return nil
			}
			if envFile == "" {
				return errors.New("no --github-env and $GITHUB_ENV is empty")
			}
			return writeEnv(envFile, res.Env)
		},
	}
	cmd.Flags().StringVar(&envFile, "github-env", os.Getenv("GITHUB_ENV"), "file the validated variables are appended to")
	cmd.Flags().BoolVar(&checkOnly, "check-only", false, "validate and write nothing")
	return cmd
}
