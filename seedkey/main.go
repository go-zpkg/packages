// Command seedkey mints the EPHEMERAL signing key of one LAN-lane run.
//
// The LAN lane used to push UNSIGNED artifacts, so nothing built there could be
// read back by a client that verifies (and a seed pass's outputs have to be
// verified by the pass that consumes them: the second pass proves it no longer
// depends on the first one's seed, which means little if it believes an
// unsigned tree). The build job holds no secret, so it makes its own key: the
// secret goes to a 0600 file that lives as long as the job's temporary
// directory, the PUBLIC half is printed as a `name:RW...` entry ready to paste
// into the next dispatch's extra_trusted_keys (or source_trusted_keys, for a
// promotion), and nothing else ever sees the secret.
//
// It is never the production key and is trusted by nobody who is not handed it.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-attest/sign"
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

// nameRe is what extra_trusted_keys accepts as a key name.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// generate is a seam for the tests.
var generate = sign.Generate

// openFile and absPath are seams for the tests: a write that fails and a
// working directory that cannot be read are not reachable otherwise.
var (
	openFile = os.OpenFile
	absPath  = filepath.Abs
)

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var secret, name, summary string
	cmd := &cobra.Command{
		Use:          "seedkey --secret FILE --name NAME [--summary FILE]",
		Short:        "mint an ephemeral signing key for one LAN-lane run and print its public half",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if secret == "" {
				return errors.New("--secret is required: the file the secret key is written to")
			}
			if !nameRe.MatchString(name) {
				return fmt.Errorf("--name %q is empty, longer than 64 or outside A-Za-z0-9._-", name)
			}
			if err := refuseInRepository(secret); err != nil {
				return err
			}
			kp, err := generate()
			if err != nil {
				return errors.New("cannot generate a key")
			}
			// O_EXCL: an existing file is a key of another run (or a symlink
			// planted to redirect the secret), never one to overwrite.
			f, err := openFile(secret, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return fmt.Errorf("cannot create %s: %v", secret, err)
			}
			_, werr := f.WriteString(kp.SecretKeyFile(""))
			if err := errors.Join(werr, f.Close()); err != nil {
				_ = os.Remove(secret)
				return fmt.Errorf("cannot write %s", secret)
			}
			entry := name + ":" + kp.PublicKeyString()
			fmt.Fprintf(cmd.OutOrStdout(), "ephemeral signing key %q (id %X) — public half, for extra_trusted_keys or source_trusted_keys:\n%s\n", name, kp.ID, entry)
			if summary != "" {
				s := "### Ephemeral LAN signing key\n\nThe artifacts this run published are signed with this key, and no other key vouches for them. Public half:\n\n```\n" + entry + "\n```\n"
				f, err := openFile(summary, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
				if err != nil {
					return fmt.Errorf("cannot open the summary: %v", err)
				}
				_, werr := f.WriteString(s)
				if err := errors.Join(werr, f.Close()); err != nil {
					return errors.New("cannot write the summary")
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&secret, "secret", "", "file the secret key is written to (created 0600, never overwritten)")
	cmd.Flags().StringVar(&name, "name", "", "key name, as it will appear in a trusted-keys entry")
	cmd.Flags().StringVar(&summary, "summary", "", "file the public half is also appended to ($GITHUB_STEP_SUMMARY)")
	return cmd
}

// refuseInRepository refuses a secret path inside a git work tree: a secret
// written there is one `git add .` from a commit.
func refuseInRepository(path string) error {
	abs, err := absPath(path)
	if err != nil {
		return err
	}
	// Every ancestor of the file, from its directory up to the root: a range
	// over the path's own components, so it ends by construction.
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.Dir(abs), sep)
	for i := len(parts); i > 0; i-- {
		d := strings.Join(parts[:i], sep)
		if d == "" {
			d = sep
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return fmt.Errorf("%s is inside a git work tree (%s): a secret key is never written there", path, d)
		}
	}
	return nil
}
