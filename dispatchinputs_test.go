package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var workflowFiles = []string{".github/workflows/build.yml", ".github/workflows/sovereign.yml"}

// A `${{ … }}` inside a run: block is pasted into the script TEXT before the
// shell parses it, so a dispatch value is code, not data. The same value in an
// env: block is a value. This reads every run: of both workflows.
var interpolated = regexp.MustCompile(`\$\{\{\s*(github\.event\.inputs|github\.event\.inputs\.[a-z_]+|inputs|matrix|github\.head_ref|github\.event\.[a-z_.]*(title|body|label|ref|name))`)

// dispatchOnly is the same detector without `matrix`, for the workflows whose
// matrix comes from a list committed in this repository rather than from a
// dispatch: only values a dispatcher or a PR author chooses are refused there.
var dispatchOnly = regexp.MustCompile(`\$\{\{\s*(github\.event\.inputs|inputs|github\.head_ref|github\.event\.[a-z_.]*(title|body|label|ref|name))`)

// Every workflow in the repository, never a hand-kept list: a new file is
// covered the day it is added.
func allWorkflowFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{".github/workflows/*.yml", ".github/workflows/*.yaml"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m...)
	}
	if len(out) < 10 {
		t.Fatalf("found %d workflow files, expected the whole directory", len(out))
	}
	return out
}

func TestNoDispatchValueIsInterpolatedIntoAScript(t *testing.T) {
	for _, f := range allWorkflowFiles(t) {
		re := dispatchOnly
		for _, w := range workflowFiles {
			if w == f {
				re = interpolated
			}
		}
		for _, s := range loadWorkflow(t, f).steps() {
			if loc := re.FindString(s.Run); loc != "" {
				t.Errorf("%s: step %q interpolates %q into its script; pass it through env: and read it as a variable", f, s.Name, loc)
			}
		}
	}
}

// The control: the detector must see the forms that were once missed, and
// stay quiet on the same expressions held in an env: value.
func TestTheInterpolationDetectorSeesEveryFormAndOnlyInScripts(t *testing.T) {
	for _, run := range []string{
		`if [ -n "${{ github.event.inputs.recipes }}" ]; then`,
		`go run ./x -chunks "${{ inputs.chunks || '14' }}"`,
		`if [ "${{ github.event.inputs.ignore_mtime || '1' }}" = "1" ]; then`,
		`echo ${{inputs.platform}}`,
		`echo "${{ matrix.chunk }}"`,
		`echo "${{ github.head_ref }}"`,
	} {
		if interpolated.FindString(run) == "" {
			t.Errorf("detector misses %q", run)
		}
	}
	for _, run := range []string{`if [ -n "$RECIPES" ]; then`, `echo "${RUNNER_TEMP}"`} {
		if interpolated.FindString(run) != "" {
			t.Errorf("detector flags %q", run)
		}
	}
}

func TestEveryExtraTrustedKeysStepScopesItNotTheWorkflow(t *testing.T) {
	for _, f := range workflowFiles {
		w := loadWorkflow(t, f)
		if _, ok := w.Env["ZPKG_EXTRA_TRUSTED_KEYS"]; ok {
			t.Errorf("%s: ZPKG_EXTRA_TRUSTED_KEYS is workflow-level, so every step and every recipe sees it", f)
		}
		for name, j := range w.Jobs {
			if _, ok := j.Env["ZPKG_EXTRA_TRUSTED_KEYS"]; ok {
				t.Errorf("%s: job %s sets ZPKG_EXTRA_TRUSTED_KEYS at job level", f, name)
			}
		}
		n := 0
		for _, s := range w.steps() {
			if v, ok := s.Env["ZPKG_EXTRA_TRUSTED_KEYS"]; ok {
				t.Errorf("%s: step %q sets the ADDITIVE ZPKG_EXTRA_TRUSTED_KEYS=%q: trust is exclusive", f, s.Name, v)
			}
			if v, ok := s.Env["ZPKG_TRUSTED_KEYS"]; ok {
				n++
				if v != "${{ env.EXTRA_KEYS }}" {
					t.Errorf("%s: step %q reads the raw value %q, not the validated EXTRA_KEYS", f, s.Name, v)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no step hands ZPKG_TRUSTED_KEYS to zpkb", f)
		}
		if w.dispatchInput(t, "extra_trusted_keys") == nil {
			t.Errorf("%s: no extra_trusted_keys dispatch input", f)
		}
	}
}

func TestExtraTrustedKeysCrossesSudoIntoTheChroot(t *testing.T) {
	src, err := os.ReadFile("builder/sovereign-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	// The extra key crosses as the EXCLUSIVE set: the script turns
	// ZPKG_EXTRA_TRUSTED_KEYS into ZPKG_TRUSTED_KEYS (which replaces the
	// compiled-in keys) and the preserved name is the second one.
	l := preserveList(t, string(src))
	if !l["ZPKG_TRUSTED_KEYS"] {
		t.Error("ZPKG_TRUSTED_KEYS is not on sovereign-build.sh's --preserve-env list: zpkx inside the chroot would verify with the default keys")
	}
	if l["ZPKG_EXTRA_TRUSTED_KEYS"] {
		t.Error("ZPKG_EXTRA_TRUSTED_KEYS is preserved into the chroot: the second pass would trust the default keys as well")
	}
	for _, want := range []string{`if [ -n "${ZPKG_EXTRA_TRUSTED_KEYS:-}" ]; then`, "trust is exclusive (ZPKG_TRUSTED_KEYS), never additive", `a seed pass is verified against the compiled-in keys only, and ZPKG_TRUSTED_KEYS is set`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("sovereign-build.sh no longer says %q", want)
		}
	}
}

func TestBuildValidatesWithInputcheckBeforeAnythingIsExported(t *testing.T) {
	w := loadWorkflow(t, ".github/workflows/build.yml")
	for _, job := range []string{"setup", "build"} {
		var at = -1
		for i, s := range w.Jobs[job].Steps {
			if strings.Contains(s.Run, "go run ./inputcheck") {
				at = i
				break
			}
		}
		if at < 0 {
			t.Fatalf("job %s does not run inputcheck", job)
		}
		for i, s := range w.Jobs[job].Steps[:at] {
			if strings.Contains(s.Run, "GITHUB_ENV") {
				t.Errorf("job %s: step %d %q writes $GITHUB_ENV before inputcheck has validated the inputs", job, i, s.Name)
			}
		}
	}
	// The seed registry is read from the validated environment, not from the
	// raw input, in the step that writes it to $GITHUB_ENV.
	for _, s := range w.Jobs["build"].Steps {
		if strings.Contains(s.Name, "pull-through cache") {
			if _, raw := s.Env["SEED_DIST"]; raw {
				t.Errorf("step %q takes SEED_DIST from the raw input", s.Name)
			}
		}
	}
}

// The production signing key is chosen by SIGN_PROD, which is decided once (in
// inputcheck, or in sovereign.yml's validate step) from the EFFECTIVE
// destination and is true only for the exact production registry. A substring
// test on one input, as the expressions used to be, signs a lookalike and
// misses a destination that arrives through another input.
func TestTheProductionKeyFollowsSignProdAndNothingElse(t *testing.T) {
	seen := 0
	for _, f := range workflowFiles {
		for _, s := range loadWorkflow(t, f).steps() {
			v, ok := s.Env["SIGNING_KEY"]
			if !ok {
				continue
			}
			seen++
			if strings.Contains(v, "contains(") || strings.Contains(v, "OCI_TO") {
				t.Errorf("%s: step %q picks the signing key from the destination text: %s", f, s.Name, v)
			}
			if !strings.Contains(v, "env.SIGN_PROD == 'true' && secrets.ZPKG_SIGNING_KEY") ||
				!strings.Contains(v, "env.SIGN_PROD != 'true' && secrets.ARTIFACT_SIGNING_KEY") {
				t.Errorf("%s: step %q does not tie each key to SIGN_PROD: %s", f, s.Name, v)
			}
		}
	}
	if seen != 3 {
		t.Errorf("found %d SIGNING_KEY steps, expected 3 (build publish job: publish-stage and catalogue; sovereign publish job: publish-stage)", seen)
	}
}

// A recipe is third-party shell, and anything in the environment of the step
// that runs it is readable by it. The steps that RUN recipes therefore hold no
// signing key and no registry password; the step after them, which runs none,
// signs and pushes with `zpkb publish-stage`, in a job on a hosted runner. There
// is no exception: a mirror_from dispatch is staged like a build, by
// `zpkb factory --mirror-from --require-origin --stage-only`, and the hosted
// publish job signs it (go-zpkg/packages#500).
func TestNoStepThatRunsRecipesHoldsTheKeyOrTheRegistryPassword(t *testing.T) {
	ran := 0
	for _, f := range []string{".github/workflows/build.yml", ".github/workflows/sovereign.yml"} {
		for _, s := range loadWorkflow(t, f).steps() {
			runsRecipes := strings.Contains(s.Run, "zpkb factory") || strings.Contains(s.Run, "sovereign-build.sh")
			if !runsRecipes {
				continue
			}
			ran++
			for _, k := range []string{"SIGNING_KEY", "OCI_PASSWORD"} {
				if v, ok := s.Env[k]; ok {
					t.Errorf("%s: step %q runs recipes and holds %s: %s", f, s.Name, k, v)
				}
			}
			if strings.Contains(s.Run, "zpkb factory") && !strings.Contains(s.Run, "--stage-only") {
				t.Errorf("%s: step %q runs `zpkb factory` without --stage-only", f, s.Name)
			}
		}
	}
	if ran < 2 {
		t.Errorf("found %d steps that run recipes, expected at least 2 (build, sovereign)", ran)
	}
	src, err := os.ReadFile("builder/sovereign-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	pl := preserveList(t, string(src))
	for _, k := range []string{"SIGNING_KEY", "OCI_PASSWORD"} {
		if pl[k] {
			t.Errorf("sovereign-build.sh carries %s across sudo into the chroot that runs recipes", k)
		}
	}
	if !strings.Contains(string(src), "zpkb factory --stage-only") || !strings.Contains(string(src), "env -u SIGNING_KEY -u OCI_PASSWORD") {
		t.Error("sovereign-build.sh must run `zpkb factory --stage-only` under `env -u SIGNING_KEY -u OCI_PASSWORD`")
	}
}

func TestTheSovereignValidateStepRefusesALookalikeDestination(t *testing.T) {
	var run string
	for _, s := range loadWorkflow(t, ".github/workflows/sovereign.yml").steps() {
		if strings.Contains(s.Run, "sign_prod=") {
			run = s.Run
		}
	}
	for _, want := range []string{
		"oci://ghcr.io/go-zpkg/packages)",
		`echo "SIGN_PROD=$sign_prod"`,
		"must be exactly oci://ghcr.io/go-zpkg/packages",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("sovereign.yml validate step lacks %q", want)
		}
	}
	// The smoke destination is an exact arm that leaves SIGN_PROD false and
	// comes BEFORE the refusal of anything else naming go-zpkg.
	smoke := strings.Index(run, "oci://ghcr.io/go-zpkg/smoke)")
	refuse := smoke + strings.Index(run[max(smoke, 0):], "*go-zpkg*)")
	if smoke < 0 || refuse < 0 || smoke > refuse {
		t.Fatalf("sovereign.yml validate step: smoke arm at %d, refusal at %d", smoke, refuse)
	}
	arm := run[smoke:refuse]
	if !strings.Contains(arm, "sign_prod=false") || strings.Contains(arm, "sign_prod=true") {
		t.Errorf("the smoke arm must keep sign_prod=false: %s", arm)
	}
}

// Both workflows run with the signing and registry secrets, so the tools they
// install are tags: a moving branch is whatever it points at when the job starts.
func TestSecretHoldingLanesInstallTaggedTools(t *testing.T) {
	tag := regexp.MustCompile(`go install github\.com/go-zpkg/(zpkb/cmd/zpkb|zpkx|zpkm)@v[0-9]+\.[0-9]+\.[0-9]+\b`)
	for _, f := range workflowFiles {
		n := 0
		for _, s := range loadWorkflow(t, f).steps() {
			for _, line := range strings.Split(s.Run, "\n") {
				if !strings.Contains(line, "go install") || strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				if !tag.MatchString(line) {
					t.Errorf("%s: step %q installs a tool that is not pinned to a tag: %s", f, s.Name, strings.TrimSpace(line))
					continue
				}
				n++
			}
		}
		if n != 4 {
			t.Errorf("%s: %d pinned installs, expected zpkb, zpkx, zpkm and the publish job's zpkb", f, n)
		}
	}
}

// The loopback decision is Go (inputcheck.isLoopbackRegistry), not a shell
// glob: no run: block of either workflow may pattern-match a host.
func TestNoShellGlobDecidesWhatIsLoopback(t *testing.T) {
	for _, f := range workflowFiles {
		for _, s := range loadWorkflow(t, f).steps() {
			if strings.Contains(s.Run, "127.*") || strings.Contains(s.Run, "seed_host") {
				t.Errorf("%s: step %q decides loopback in shell", f, s.Name)
			}
		}
	}
	for _, s := range loadWorkflow(t, ".github/workflows/build.yml").steps() {
		if strings.Contains(s.Run, "ZPKG_VERIFY=0") && !strings.Contains(s.Run, `"$SEED_LOOPBACK" = true`) {
			t.Errorf("step %q turns verification off without SEED_LOOPBACK", s.Name)
		}
	}
}

// A pass that consumes a seed pass's output checks, after staging, that every
// staged artifact is verifiably seed-built, with the keys in reach and the
// registry it read them from, and only on such a pass.
func TestPassTwoChecksEveryStagedArtifactIsSeedBuilt(t *testing.T) {
	found := 0
	for _, s := range loadWorkflow(t, ".github/workflows/build.yml").steps() {
		if !strings.Contains(s.Run, "origin --seeded") {
			continue
		}
		found++
		for _, want := range []string{`--dist "$DIST_FROM"`, `--root "$RUNNER_TEMP/rootfs/zpkg"`, `--seed-ledger-sha256 "$LEDGER_SHA256"`, `[ -z "$LEDGER_SHA256" ]`} {
			if !strings.Contains(s.Run, want) {
				t.Errorf("step %q lacks %s", s.Name, want)
			}
		}
		if !strings.Contains(s.If, "EXTRA_KEYS != ''") {
			t.Errorf("step %q is not limited to a pass with extra keys: %q", s.Name, s.If)
		}
		if s.Env["ZPKG_TRUSTED_KEYS"] == "" {
			t.Errorf("step %q has no trusted keys in reach", s.Name)
		}
		if s.Env["LEDGER_SHA256"] != "${{ env.PASS1_LEDGER_SHA256 }}" {
			t.Errorf("step %q does not read the validated PASS1_LEDGER_SHA256: %q", s.Name, s.Env["LEDGER_SHA256"])
		}
	}
	if found != 1 {
		t.Errorf("%d steps run `origin --seeded`, expected exactly the pass-2 check", found)
	}
}

// go-zpkg/packages#500: a mirror_from dispatch is staged by the build job and
// signed by the hosted publish job. Nothing refuses it before the build any
// more, and the step that stages it carries the flags that make it a verified
// copy, with no key in reach.
func TestMirrorFromIsStagedWithVerifiedOriginAndNeverRefusedByPreflight(t *testing.T) {
	staged := 0
	for _, s := range loadWorkflow(t, ".github/workflows/build.yml").steps() {
		if strings.Contains(s.Run, "mirror_from is refused") {
			t.Errorf("step %q still refuses mirror_from", s.Name)
		}
		if !strings.Contains(s.Run, "zpkb factory") {
			continue
		}
		staged++
		for _, want := range []string{"--stage-only", "${MIRROR_FROM:+--mirror-from \"$MIRROR_FROM\"}", "${REQUIRE_ORIGIN:+--require-origin}"} {
			if !strings.Contains(s.Run, want) {
				t.Errorf("step %q does not pass %s", s.Name, want)
			}
		}
		if strings.Contains(s.Run, "ZPKG_VERIFY=0") {
			t.Errorf("step %q disables verification", s.Name)
		}
		for _, k := range []string{"SIGNING_KEY", "OCI_PASSWORD"} {
			if _, ok := s.Env[k]; ok {
				t.Errorf("step %q holds %s", s.Name, k)
			}
		}
	}
	if staged != 1 {
		t.Errorf("found %d build.yml steps running `zpkb factory`, expected 1", staged)
	}
}

// ⛔ promo-r1 (run 45140545009) ended green on `0 built, 0 skipped, 1 failed`
// and `stage plan: 0 artifact(s)`. The step that should have caught it counted
// FILES in dist (the plan is one), ran for `recipes` only, and looked in dist
// while the sovereign path stages elsewhere. It is now `namedcheck`, for every
// way of naming what to build, and never for the nightly's chunks.
func TestANamedDispatchFailsOnAnyFailureOrAnEmptyPromotion(t *testing.T) {
	var found bool
	for _, s := range loadWorkflow(t, ".github/workflows/build.yml").steps() {
		if s.Name != "a named dispatch that built nothing" {
			continue
		}
		found = true
		for _, in := range []string{"recipes", "lock", "mirror_from", "require_origin"} {
			if !strings.Contains(s.If, "github.event.inputs."+in+" != ''") {
				t.Errorf("the named-dispatch step does not run for %s: if = %q", in, s.If)
			}
		}
		if strings.Contains(s.If, "inputs.recipes == ''") || strings.Contains(s.If, "matrix") {
			t.Errorf("the step must not be conditioned on the chunk: %q", s.If)
		}
		for _, want := range []string{"go run ./namedcheck", "--failures failures.txt", "--require-staged", "$RUNNER_TEMP/rootfs/dist"} {
			if !strings.Contains(s.Run, want) {
				t.Errorf("the named-dispatch step lacks %q", want)
			}
		}
		if strings.Contains(s.Run, "find dist") || strings.Contains(s.Run, "grep -c") {
			t.Errorf("the verdict is shell again (counting files / grep): %s", s.Run)
		}
	}
	if !found {
		t.Fatal("no step named `a named dispatch that built nothing`")
	}
}

// The build step makes a named dispatch strict (zpkb reads STRICT; the
// sovereign script reads SOVEREIGN_STRICT) and leaves the nightly's chunks
// best-effort: SOVEREIGN_STRICT=0 survives only on the chunk branch.
func TestOnlyChunksAreBestEffort(t *testing.T) {
	n := 0
	for _, s := range loadWorkflow(t, ".github/workflows/build.yml").steps() {
		if !strings.Contains(s.Run, "export SOVEREIGN_STRICT=0") {
			continue
		}
		n++
		for _, want := range []string{"NAMED_DISPATCH=1", "export STRICT=1", "export SOVEREIGN_STRICT=1", `[ -n "$MIRROR_FROM" ]`, `"$MATRIX_CHUNK" = "manual"`, `[ -n "$IN_LOCK" ]`} {
			if !strings.Contains(s.Run, want) {
				t.Errorf("step %q lacks %q", s.Name, want)
			}
		}
		if strings.Index(s.Run, "export SOVEREIGN_STRICT=0") < strings.Index(s.Run, `if [ "$NAMED_DISPATCH" = 1 ]`) {
			t.Errorf("SOVEREIGN_STRICT=0 is exported before the named-dispatch test")
		}
	}
	if n != 1 {
		t.Errorf("found %d steps exporting SOVEREIGN_STRICT=0, want 1", n)
	}
}
