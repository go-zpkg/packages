// Command scrubsecrets removes the VALUE of named environment variables from
// files that are about to be uploaded as workflow artifacts.
//
//	scrubsecrets --env SIGNING_KEY,OCI_PASSWORD file...
//
// A build step's log is masked by the runner, but a file a build writes and a
// later step uploads is not: whatever a recipe printed into failures-detail.txt
// goes out verbatim. This runs between the build and the upload, in a step that
// holds the secrets only as environment, and rewrites each file with every
// secret (and every line of a multi-line one) replaced by "***".
//
// Fail closed: a file that cannot be rewritten is REMOVED, because an upload
// step that finds nothing warns, and one that finds an unscrubbed file leaks.
// A missing file is not an error. An environment variable named but empty
// scrubs nothing and says so; a value shorter than minLen is refused rather
// than replaced, since replacing "1" would shred the file for no protection.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// minLen is the shortest secret scrubbed. Shorter values cannot be told from
// ordinary text, and a one-character "secret" would mangle every line.
const minLen = 8

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

// RootCmd builds the command; tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var names string
	cmd := &cobra.Command{
		Use:           "scrubsecrets --env NAME[,NAME...] file...",
		Short:         "replace the values of named environment variables in files",
		SilenceUsage:  true,
		SilenceErrors: false,
		Args:          cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, files []string) error {
			if names == "" {
				return errors.New("--env names no variable")
			}
			var secrets []string
			for _, n := range strings.Split(names, ",") {
				n = strings.TrimSpace(n)
				if n == "" {
					continue
				}
				v := os.Getenv(n)
				if v == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "scrubsecrets: %s is empty, nothing to scrub for it\n", n)
					continue
				}
				if len(strings.TrimSpace(v)) < minLen {
					return fmt.Errorf("%s is shorter than %d characters; refusing to scrub a value that short", n, minLen)
				}
				secrets = append(secrets, pieces(v)...)
			}
			var failed bool
			for _, f := range files {
				n, err := scrubFile(f, secrets)
				switch {
				case errors.Is(err, fs.ErrNotExist):
				case err != nil:
					fmt.Fprintf(cmd.ErrOrStderr(), "scrubsecrets: %s: %v; removed\n", f, err)
					_ = os.Remove(f)
					failed = true
				default:
					fmt.Fprintf(cmd.OutOrStdout(), "scrubsecrets: %s: %d occurrence(s) replaced\n", f, n)
				}
			}
			if failed {
				return errors.New("a file could not be scrubbed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&names, "env", "", "comma-separated environment variable names whose values are scrubbed")
	return cmd
}

// pieces returns the secret itself and each of its lines long enough to scrub,
// longest first, so a line is not half-replaced by a shorter overlapping piece.
func pieces(v string) []string {
	v = strings.TrimSpace(v)
	out := []string{v}
	for _, l := range strings.Split(v, "\n") {
		l = strings.TrimSpace(l)
		if len(l) >= minLen && l != v {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func scrubFile(path string, secrets []string) (int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("not a regular file (%s)", info.Mode().Type())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s, n := scrub(string(b), secrets)
	if n == 0 {
		return 0, nil
	}
	return n, os.WriteFile(path, []byte(s), info.Mode().Perm())
}

// scrub replaces every secret in s and counts the replacements.
func scrub(s string, secrets []string) (string, int) {
	total := 0
	for _, sec := range secrets {
		if c := strings.Count(s, sec); c > 0 {
			total += c
			s = strings.ReplaceAll(s, sec, "***")
		}
	}
	return s, total
}
