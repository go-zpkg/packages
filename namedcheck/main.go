// Command namedcheck decides whether a NAMED dispatch (recipes, a lock, or a
// mirror_from promotion) did what it was asked, from the two files the factory
// leaves behind: failures.txt and the stage plan.
//
// A chunk of the nightly is best-effort on purpose, so none of this applies to
// it. A dispatch that names what it wants is the opposite: any failure fails
// it, and a promotion that staged nothing is not a promotion.
//
// It exists because `0 built, 0 skipped, 1 failed` and `stage plan: 0
// artifact(s)` once ended in a green run (go-zpkg/packages run 45140545009):
// the step that was meant to catch it counted FILES in dist, and the plan itself
// is one.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var osExit = os.Exit

// maxFileBytes bounds what is read: both files are written by the job that ran
// recipes, so neither is trusted to be small.
const maxFileBytes = 16 << 20

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	cmd := RootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// RootCmd is the command.
func RootCmd() *cobra.Command {
	var failures, stage string
	var requireStaged bool
	cmd := &cobra.Command{
		Use:           "namedcheck --failures failures.txt --stage <dist dir> [--require-staged]",
		Short:         "fail a named dispatch that recorded a failure, or a promotion that staged nothing",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := Check(failures, stage, requireStaged)
			fmt.Fprintln(cmd.OutOrStdout(), v.Summary())
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&failures, "failures", "failures.txt", "the factory's failure list")
	f.StringVar(&stage, "stage", "dist", "the directory holding publish-plan.json")
	f.BoolVar(&requireStaged, "require-staged", false, "also fail when the plan stages no artifact (a mirror_from / require_origin promotion)")
	return cmd
}

// Verdict is what was read.
type Verdict struct {
	Failures int
	Staged   int
	HavePlan bool
}

// Summary is one line for the log.
func (v Verdict) Summary() string {
	plan := "no plan"
	if v.HavePlan {
		plan = fmt.Sprintf("%d artifact(s) staged", v.Staged)
	}
	return fmt.Sprintf("%d failure(s) recorded, %s", v.Failures, plan)
}

// Check reads the two files and returns the verdict and an error when it is a
// failure. A file that cannot be read is a failure too, except a failure list
// that does not exist, which the factory writes only when it ran to the end.
func Check(failuresPath, stageDir string, requireStaged bool) (Verdict, error) {
	var v Verdict
	b, err := readBounded(failuresPath)
	switch {
	case err == nil:
		for _, l := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(l) != "" {
				v.Failures++
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// Not written: the factory did not get to its end, or ran nothing.
	default:
		return v, fmt.Errorf("reading %s: %w", failuresPath, err)
	}
	pb, err := readBounded(filepath.Join(stageDir, "publish-plan.json"))
	switch {
	case err == nil:
		var p struct {
			Artifacts []json.RawMessage `json:"artifacts"`
		}
		if err := json.Unmarshal(pb, &p); err != nil {
			return v, fmt.Errorf("publish-plan.json is not a stage plan: %w", err)
		}
		v.HavePlan, v.Staged = true, len(p.Artifacts)
	case errors.Is(err, os.ErrNotExist):
	default:
		return v, fmt.Errorf("reading the stage plan: %w", err)
	}
	if v.Failures > 0 {
		return v, fmt.Errorf("this dispatch named what it wanted and %d of it failed — see failures-detail.txt", v.Failures)
	}
	if requireStaged && v.Staged == 0 {
		return v, errors.New("a promotion that stages no artifact promoted nothing (a plan with 0 artifacts, or none at all)")
	}
	return v, nil
}

func readBounded(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, maxFileBytes)
	}
	return b, nil
}
