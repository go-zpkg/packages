package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ⛔⛔ A SELF-HOSTED JOB HOLDS NO SECRET, AND A JOB THAT PUBLISHES CHECKS WHAT IT
// IS ABOUT TO PUBLISH.
//
// The self-hosted pool is long-lived, keeps its home directory between jobs and
// runs recipe code written by third parties. A secret that reaches such a job
// is readable by that code. The design (secaudit finding 14):
//
//   - a self-hosted job stages the artifacts, uploads them and outputs a digest;
//   - a separate `publish` job on a GitHub-hosted runner `needs:` it, downloads
//     the artifact, VERIFIES the digest (`go run ./distdigest --verify`) and only
//     then signs and pushes (`zpkb publish-stage`) with the secrets.
//
// This file is the gate on both halves. The lanes that have not moved yet are
// listed in knownSelfHostedSecretHolders, each with its tracking issue, and the
// list is itself checked: an entry that no longer holds a secret fails, so the
// exception cannot outlive the work.

// knownSelfHostedSecretHolders: "<workflow file>#<job>" -> tracking issue.
//
// Empty since go-zpkg/packages#498 and #499 moved the windows lanes: a new entry
// here is a lane that holds a secret on the pool again, and needs an issue.
var knownSelfHostedSecretHolders = map[string]string{}

var (
	// publishCmdRE is every command that signs or pushes: `zpkb publish`,
	// `zpkb publish-stage` and `zpkb catalog ... --publish`.
	publishCmdRE = regexp.MustCompile(`(?s)\bzpkb (publish\b|catalog\b.*--publish\b)`)
	// usesLineRE finds the action a step (or a reusable-workflow job) runs.
	usesLineRE  = regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*([^\s#]+)`)
	secretRefRE = regexp.MustCompile(`\bsecrets(\.[A-Za-z0-9_-]+|\[|\))`)
	tokenRefRE  = regexp.MustCompile(`secrets\.GITHUB_TOKEN|github\.token`)
	sha40Use    = regexp.MustCompile(`@[0-9a-f]{40}(\s|$)`)
	// Variables only a job that builds reads; the publish jobs must not.
	buildOnlyEnvRE = regexp.MustCompile(`\b(OVERLAY_URL|DIST_FROM|SEED_DIST|SEED_LOOPBACK|ZPKG_RECIPES_OVERLAY)\b`)
)

func parseWorkflowBytes(b []byte) (workflow, error) {
	var w workflow
	err := yaml.Unmarshal(b, &w)
	return w, err
}

// jobText is every place a job can name a secret: its env, and each step's
// name, condition, env, with and run.
func jobText(j struct {
	Env         map[string]any    `yaml:"env"`
	Steps       []wfStep          `yaml:"steps"`
	RunsOn      any               `yaml:"runs-on"`
	Needs       any               `yaml:"needs"`
	If          string            `yaml:"if"`
	Permissions any               `yaml:"permissions"`
	Outputs     map[string]string `yaml:"outputs"`
}) string {
	var b strings.Builder
	fmt.Fprintln(&b, j.Env, j.If, j.Outputs)
	for _, s := range j.Steps {
		fmt.Fprintln(&b, s.Name, s.If, s.Uses, s.Env, s.With, s.Run)
	}
	return b.String()
}

// canWriteWithToken reports whether the job's GITHUB_TOKEN may write: only an
// explicit permissions block with no `write` and no `write-all` says it cannot.
func canWriteWithToken(wfPerm, jobPerm any) bool {
	p := jobPerm
	if p == nil {
		p = wfPerm
	}
	switch v := p.(type) {
	case map[string]any:
		for _, x := range v {
			if s, _ := x.(string); s == "write" {
				return true
			}
		}
		return false
	case string:
		return v != "read-all"
	}
	return true // unspecified: the repository default, which may write
}

// selfHostedCapable: the job can land on the self-hosted pool. A matrix runner
// counts only when the workflow itself names the pool somewhere (build.yml's
// matrix is computed by its setup job); wasip1-c.yml's matrix is all hosted.
func selfHostedCapable(runsOn any, workflowText string) bool {
	r := fmt.Sprint(runsOn)
	if strings.Contains(r, "self-hosted") {
		return true
	}
	// darwin.yml's macOS runners are GitHub-hosted but run third-party recipe
	// code exactly like the pool does, so they are judged the same way.
	return strings.Contains(r, "matrix.runner") &&
		(strings.Contains(workflowText, "self-hosted") || strings.Contains(workflowText, "macos-"))
}

// selfHostedViolations returns, per job, what a self-hosted-capable job holds
// that it must not.
func selfHostedViolations(w workflow, workflowText string) map[string][]string {
	out := map[string][]string{}
	for name, j := range w.Jobs {
		if !selfHostedCapable(j.RunsOn, workflowText) {
			continue
		}
		text := jobText(j)
		for _, m := range secretRefRE.FindAllString(text, -1) {
			if m == "secrets.GITHUB_TOKEN" || strings.HasPrefix(m, "secrets.GITHUB_TOKEN") {
				continue
			}
			out[name] = append(out[name], "references "+m)
		}
		if tokenRefRE.MatchString(text) && canWriteWithToken(w.Permissions, j.Permissions) {
			out[name] = append(out[name], "holds a GITHUB_TOKEN that may write (no read-only permissions block)")
		}
	}
	return out
}

// publishViolations judges the jobs that publish: a job named `publish*`, or any
// job that runs `zpkb publish`, `zpkb publish-stage` or `zpkb catalog --publish`
// and is not the unsigned LAN push.
//
// Only a workflow with a self-hosted-capable job is judged: catalogue.yml has a
// `publish` job of its own, runs on hosted runners alone and builds nothing to
// verify.
func publishViolations(w workflow, workflowText string) map[string][]string {
	out := map[string][]string{}
	split := false
	for _, j := range w.Jobs {
		if selfHostedCapable(j.RunsOn, workflowText) {
			split = true
		}
	}
	if !split {
		return out
	}
	for name, j := range w.Jobs {
		runsPublish := false
		for _, s := range j.Steps {
			if publishCmdRE.MatchString(s.Run) && !strings.Contains(s.Run, "--allow-unsigned") && !strings.Contains(s.If, "PUBLISH_LANE == 'lan'") {
				runsPublish = true
			}
		}
		if !strings.Contains(name, "publish") && !runsPublish {
			continue
		}
		if !strings.Contains(name, "publish") {
			out[name] = append(out[name], "runs a publish command (zpkb publish, publish-stage or catalog --publish) but is not a publish job")
			continue
		}
		if fmt.Sprint(j.RunsOn) != "ubuntu-latest" {
			out[name] = append(out[name], fmt.Sprintf("runs on %v, not ubuntu-latest", j.RunsOn))
		}
		if j.Needs == nil {
			out[name] = append(out[name], "has no needs: on the job that built what it publishes")
		}
		verifyAt, publishAt := -1, -1
		for i, s := range j.Steps {
			if s.Uses != "" && !sha40Use.MatchString(s.Uses+" ") {
				out[name] = append(out[name], "uses "+s.Uses+", not pinned to a commit")
			}
			if strings.Contains(s.Run, "distdigest") && strings.Contains(s.Run, "--verify") && verifyAt < 0 {
				verifyAt = i
			}
			// A missing declaration must fail the leg: only an explicit
			// `none` from the build leg may skip, so no step that reads the
			// declaration may fall back to `none` itself.
			if strings.Contains(s.Run, "--declared") && (strings.Contains(s.Run, "=none") || strings.Contains(s.Run, "else") || strings.Contains(s.Run, "||")) {
				out[name] = append(out[name], "reads the declaration with a fallback: a missing one must fail, not skip")
			}
			// A publish job builds nothing: it exports only what publishing
			// reads, and no step names a build-only variable.
			if strings.Contains(s.Run, "./inputcheck") && !strings.Contains(s.Run, "--publish-env") {
				out[name] = append(out[name], "runs inputcheck without --publish-env, so it is handed the build's OVERLAY_URL, DIST_FROM and SEED_*")
			}
			if buildOnlyEnvRE.MatchString(fmt.Sprint(s.Env, s.Run, s.If, s.With)) {
				out[name] = append(out[name], "reads a build-only variable (OVERLAY_URL, DIST_FROM, SEED_DIST, SEED_LOOPBACK, ZPKG_RECIPES_OVERLAY)")
			}
			if publishCmdRE.MatchString(s.Run) && publishAt < 0 {
				publishAt = i
			}
		}
		switch {
		case verifyAt < 0:
			out[name] = append(out[name], "has no `distdigest --verify` step")
		case publishAt >= 0 && verifyAt > publishAt:
			out[name] = append(out[name], "verifies the digest AFTER it publishes")
		}
	}
	return out
}

func repoWorkflows(t *testing.T) map[string][]byte {
	t.Helper()
	dir := filepath.Join(".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func TestNoSelfHostedJobHoldsASecret(t *testing.T) {
	files := repoWorkflows(t)
	if len(files) < 10 {
		t.Fatalf("read %d workflows, expected the whole directory", len(files))
	}
	capable, stale := 0, map[string]bool{}
	for k := range knownSelfHostedSecretHolders {
		stale[k] = true
	}
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, f := range names {
		w, err := parseWorkflowBytes(files[f])
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, j := range w.Jobs {
			if selfHostedCapable(j.RunsOn, string(files[f])) {
				capable++
			}
		}
		for job, vs := range selfHostedViolations(w, string(files[f])) {
			key := f + "#" + job
			if issue, ok := knownSelfHostedSecretHolders[key]; ok {
				delete(stale, key)
				_ = issue
				continue
			}
			t.Errorf("%s: self-hosted job %s %s — secrets belong in a `publish` job on a hosted runner (secaudit finding 14)",
				f, job, strings.Join(vs, "; "))
		}
	}
	if capable < 5 {
		t.Errorf("found %d self-hosted-capable jobs, expected at least 5 — the scan stopped reading them", capable)
	}
	for k := range stale {
		t.Errorf("%s is listed in knownSelfHostedSecretHolders (%s) but no longer holds a secret — remove the entry",
			k, knownSelfHostedSecretHolders[k])
	}
}

func TestEveryPublishJobVerifiesTheDigestOnAHostedRunner(t *testing.T) {
	files := repoWorkflows(t)
	publishJobs := 0
	for f, b := range files {
		w, err := parseWorkflowBytes(b)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for job, vs := range publishViolations(w, string(b)) {
			t.Errorf("%s: job %s %s", f, job, strings.Join(vs, "; "))
		}
	}
	for _, f := range []string{"build.yml", "sovereign.yml", "darwin.yml", "windows.yml", "windows-rust.yml"} {
		w, err := parseWorkflowBytes(files[f])
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := w.Jobs["publish"]; ok {
			publishJobs++
		}
	}
	if publishJobs != 5 {
		t.Errorf("found %d publish jobs, expected 5 (build, sovereign, darwin, windows, windows-rust)", publishJobs)
	}
}

// The workflows without a permissions block hand every job the repository's
// default token; build.yml and sovereign.yml say read-only at the top.
func TestTheBuildAndSovereignWorkflowsDefaultToNoToken(t *testing.T) {
	for _, f := range []string{"build.yml", "sovereign.yml", "darwin.yml", "windows.yml", "windows-rust.yml"} {
		w := loadWorkflow(t, filepath.Join(".github", "workflows", f))
		if canWriteWithToken(w.Permissions, nil) {
			t.Errorf("%s: no workflow-level permissions block that is read-only", f)
		}
	}
}

// Negative controls: the gate has to fail on the shapes it exists for.
func TestSelfHostedSecretGateFailsOnTheShapesItGuards(t *testing.T) {
	const bad = `
permissions: {}
jobs:
  build:
    runs-on: [self-hosted, incus, amd64]
    permissions:
      contents: read
    steps:
      - run: echo
        env:
          SIGNING_KEY: ${{ secrets.ZPKG_SIGNING_KEY }}
  writer:
    runs-on: [self-hosted]
    permissions:
      packages: write
    steps:
      - run: echo
        env:
          T: ${{ secrets.GITHUB_TOKEN }}
  reader:
    runs-on: [self-hosted]
    permissions:
      contents: read
    steps:
      - run: echo
        env:
          T: ${{ secrets.GITHUB_TOKEN }}
  hosted:
    runs-on: ubuntu-latest
    steps:
      - run: echo
        env:
          SIGNING_KEY: ${{ secrets.ZPKG_SIGNING_KEY }}
`
	w, err := parseWorkflowBytes([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	v := selfHostedViolations(w, bad)
	if len(v["build"]) == 0 {
		t.Error("a self-hosted job with a signing key was not flagged")
	}
	if len(v["writer"]) == 0 {
		t.Error("a self-hosted job with a write token was not flagged")
	}
	if len(v["reader"]) != 0 {
		t.Errorf("a self-hosted job with a read-only token was flagged: %v", v["reader"])
	}
	if len(v["hosted"]) != 0 {
		t.Errorf("a hosted job was flagged: %v", v["hosted"])
	}

	// A matrix of GitHub-hosted macOS runners that run recipes is judged too.
	const macos = `
jobs:
  build:
    runs-on: ${{ matrix.runner }}
    strategy:
      matrix:
        include: [{runner: macos-15}]
    steps:
      - run: echo
        env:
          SIGNING_KEY: ${{ secrets.ZPKG_SIGNING_KEY }}
`
	w, err = parseWorkflowBytes([]byte(macos))
	if err != nil {
		t.Fatal(err)
	}
	if len(selfHostedViolations(w, macos)["build"]) == 0 {
		t.Error("a macOS matrix job with a signing key was not flagged")
	}

	const noVerify = `
jobs:
  publish:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: zpkb publish-stage dist
  publish-late:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: zpkb publish-stage dist
      - run: go run ./distdigest --verify "$D"
  publish-tag:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: go run ./distdigest --verify "$D"
  publish-selfhosted:
    runs-on: [self-hosted]
    steps:
      - run: go run ./distdigest --verify "$D"
  publish-fallback:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: |
          if [ -f f ]; then d=$(go run ./distdigest --declared f); else d=none; fi
      - run: go run ./distdigest --verify "$D"
  publish-orfallback:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: d=$(go run ./distdigest --declared f || echo none)
      - run: go run ./distdigest --verify "$D"
  publish-strict:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: d=$(go run ./distdigest --declared f)
      - run: go run ./distdigest --verify "$D"
  publish-buildenv:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: go run ./inputcheck
      - run: go run ./distdigest --verify "$D"
  publish-overlay:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: go run ./inputcheck --publish-env
        env:
          ZPKG_RECIPES_OVERLAY: ${{ env.OVERLAY_URL }}
      - run: go run ./distdigest --verify "$D"
  ok:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: echo
  sneaky:
    runs-on: ubuntu-latest
    steps:
      - run: zpkb publish-stage dist
`
	w, err = parseWorkflowBytes([]byte(noVerify))
	if err != nil {
		t.Fatal(err)
	}
	v = publishViolations(w, noVerify)
	for _, job := range []string{"publish", "publish-late", "publish-tag", "publish-selfhosted", "sneaky", "publish-fallback", "publish-orfallback", "publish-buildenv", "publish-overlay"} {
		if len(v[job]) == 0 {
			t.Errorf("job %s was not flagged", job)
		}
	}
	for _, v := range v["publish-fallback"] {
		if !strings.Contains(v, "fallback") {
			t.Errorf("the fallback was flagged for another reason: %s", v)
		}
	}
	if len(v["publish-strict"]) != 0 {
		t.Errorf("a strict declaration read was flagged: %v", v["publish-strict"])
	}
	if len(v["ok"]) != 0 {
		t.Errorf("a job that publishes nothing was flagged: %v", v["ok"])
	}
}

// The windows lanes publish with `zpkb publish` and `zpkb catalog --publish`,
// not `publish-stage`: the gate has to see those too.
func TestPublishGateSeesZpkbPublishAndTheCatalogue(t *testing.T) {
	const shapes = `
jobs:
  build:
    runs-on: [self-hosted]
    steps:
      - run: zpkb publish --to "$T" --project p --platform windows/x86-64 f.tar.gz
  lan:
    runs-on: [self-hosted]
    steps:
      - run: zpkb publish --to "$T" --allow-unsigned f.tar.gz
  catalogue:
    runs-on: ubuntu-latest
    steps:
      - run: |
          zpkb catalog \
            --recipe-dir pantry \
            --publish
  publish-early:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: zpkb publish --to "$T" f
      - run: go run ./distdigest --verify "$D"
  publish-ok:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: go run ./distdigest --verify "$D"
      - run: zpkb publish --to "$T" f
      - run: |
          zpkb catalog \
            --publish
  installs-only:
    runs-on: ubuntu-latest
    steps:
      - run: go install github.com/go-zpkg/zpkb/cmd/zpkb@v0.104.1
`
	w, err := parseWorkflowBytes([]byte(shapes))
	if err != nil {
		t.Fatal(err)
	}
	v := publishViolations(w, shapes)
	for _, job := range []string{"build", "catalogue", "publish-early"} {
		if len(v[job]) == 0 {
			t.Errorf("job %s was not flagged", job)
		}
	}
	for _, job := range []string{"lan", "publish-ok", "installs-only"} {
		if len(v[job]) != 0 {
			t.Errorf("job %s was flagged: %v", job, v[job])
		}
	}
}

// unpinnedActions returns the `uses:` references in a workflow that are not a
// full 40-hex commit SHA. A tag can be moved to other code after review, and a
// job that holds a token or a key would run what it now points at.
func unpinnedActions(text string) []string {
	var bad []string
	for _, m := range usesLineRE.FindAllStringSubmatch(text, -1) {
		ref := m[1]
		if strings.HasPrefix(ref, "./") { // a local action is this repository's own code
			continue
		}
		if !sha40Use.MatchString(ref + " ") {
			bad = append(bad, ref)
		}
	}
	return bad
}

// ⛔ EVERY ACTION IS PINNED TO A COMMIT, with the tag it was taken from kept as
// a trailing comment, in every workflow (go-zpkg/packages#502). Renovate bumps
// the SHA and the comment together (helpers:pinGitHubActionDigests).
func TestEveryActionIsPinnedToACommit(t *testing.T) {
	files := repoWorkflows(t)
	if len(files) < 10 {
		t.Fatalf("read %d workflows, expected the whole directory", len(files))
	}
	uses := 0
	for f, b := range files {
		uses += len(usesLineRE.FindAllString(string(b), -1))
		for _, ref := range unpinnedActions(string(b)) {
			t.Errorf("%s: %s is not pinned to a full commit SHA", f, ref)
		}
	}
	if uses < 50 {
		t.Errorf("found %d `uses:` lines, expected dozens — the scan stopped reading them", uses)
	}
}

// Negative controls: the pin gate has to fail on the shapes it exists for.
func TestThePinGateFailsOnTheShapesItGuards(t *testing.T) {
	const text = `
jobs:
  j:
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7 # v7
      - uses: actions/cache@main
      - uses: actions/upload-artifact@3d3c42e5aac5ba805825da76410c181273ba90b
      - uses: actions/download-artifact@3d3c42e5aac5ba805825da76410c181273ba90b1a
      - uses: bytecodealliance/actions/wasmtime/setup@v1
      -   uses: docker://alpine:3
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
      - uses: ./local-action
      - name: pinned, with a trailing comment
        uses: actions/setup-go@d8cbb8045d7f43706b9177cceb36c1f91a6644d5 # v7
`
	want := []string{
		"actions/checkout@v7",
		"actions/setup-go@v7",
		"actions/cache@main",
		"actions/upload-artifact@3d3c42e5aac5ba805825da76410c181273ba90b",
		"actions/download-artifact@3d3c42e5aac5ba805825da76410c181273ba90b1a",
		"bytecodealliance/actions/wasmtime/setup@v1",
		"docker://alpine:3",
	}
	got := unpinnedActions(text)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("flagged\n  %v\nwant\n  %v", got, want)
	}
}
