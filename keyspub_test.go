package main

import (
	"os"
	"strings"
	"testing"
)

// keys.yml handles two signing secrets. Each property below is a way for one to
// reach a log, another step or an argv.
func TestTheKeysWorkflowHandlesTheSecretsNarrowly(t *testing.T) {
	b, err := os.ReadFile(".github/workflows/keys.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// Trigger and authority: dispatch only, read-only token.
	if !strings.Contains(s, "on:\n  workflow_dispatch:\n\npermissions:\n  contents: read\n") {
		t.Error("keys.yml must be dispatch-only with contents: read")
	}
	for _, bad := range []string{"schedule:", "pull_request", "push:", "set -x", "set -o xtrace", "echo \"$SEED", "echo \"$PROD", "::add-mask", "contents: write", "packages: write"} {
		if strings.Contains(s, bad) {
			t.Errorf("keys.yml contains %q", bad)
		}
	}
	// Each secret appears exactly once, in the env of the staging step.
	for _, name := range []string{"secrets.ARTIFACT_SIGNING_KEY", "secrets.ZPKG_SIGNING_KEY"} {
		if n := strings.Count(s, name); n != 1 {
			t.Errorf("%s appears %d times", name, n)
		}
	}
	stage := between(s, "      - name: stage the secret keys", "      - name: derive")
	if !strings.Contains(stage, "SEED: ${{ secrets.ARTIFACT_SIGNING_KEY }}") || !strings.Contains(stage, "PROD: ${{ secrets.ZPKG_SIGNING_KEY }}") {
		t.Error("the secrets must be in the env of the staging step")
	}
	if !strings.Contains(stage, "umask 077") {
		t.Error("the key files must be created private")
	}
	// No other step sees SEED or PROD, and no env above the steps does.
	if strings.Contains(strings.Replace(s, stage, "", 1), "SEED") || strings.Contains(strings.Replace(s, stage, "", 1), "PROD:") {
		t.Error("a secret variable is visible outside the staging step")
	}
	// The tool gets paths, and the cleanup runs after a failure.
	derive := between(s, "      - name: derive", "      - name: delete")
	if !strings.Contains(derive, `--seed "$RUNNER_TEMP/signing-keys/seed"`) || !strings.Contains(derive, `--prod "$RUNNER_TEMP/signing-keys/prod"`) {
		t.Error("the tool must be given file paths")
	}
	if !strings.Contains(s, "      - name: delete the secret key files\n        if: always()\n        run: rm -rf \"$RUNNER_TEMP/signing-keys\"") {
		t.Error("the files must be deleted by a step that runs after a failure")
	}
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], to)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j]
}
