// Command distdigest prints, and verifies, a sha256 digest of a staged dist
// tree: the binding between the build job and the publish job.
//
// The build job runs on a self-hosted runner and holds no secret. It uploads
// the tree it staged and outputs this digest; the publish job runs on a fresh
// GitHub-hosted runner, downloads the tree and refuses to sign or push unless
// the digest it computes equals the one the build job declared.
//
// The digest is over the tree, not over the archive that carries it, so it does
// not depend on how an artifact is zipped. It hashes one line per regular file,
// in byte order of the slash-separated relative path:
//
//	<path> NUL <decimal size> NUL <hex sha256 of the contents> LF
//
// Anything that is not a regular file or a directory (a symlink, a device, a
// socket) is refused: a tree that holds one is not a tree a publisher may sign.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/spf13/cobra"
)

var osExit = os.Exit

// maxFiles bounds the walk: a staged tree holds a few thousand files, and a
// tree of millions is not a dist.
const maxFiles = 1_000_000

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

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

// TreeDigest returns "sha256:<hex>" for the tree rooted at dir.
func TreeDigest(dir string) (string, error) {
	st, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	type entry struct{ rel, line string }
	var entries []entry
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%v)", p, d.Type())
		}
		if len(entries) >= maxFiles {
			return fmt.Errorf("more than %d files", maxFiles)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, cerr := io.Copy(h, f)
		if err := errors.Join(cerr, f.Close()); err != nil {
			return err
		}
		entries = append(entries, entry{rel, rel + "\x00" + strconv.FormatInt(n, 10) + "\x00" + hex.EncodeToString(h.Sum(nil)) + "\n"})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		io.WriteString(h, e.line)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// RootCmd builds the command; tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var verify string
	cmd := &cobra.Command{
		Use:          "distdigest [--verify sha256:<hex>] DIR",
		Short:        "print or verify the sha256 digest of a staged dist tree",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("verify") && !digestRe.MatchString(verify) {
				err := fmt.Errorf("--verify %q is not sha256:<64 hex digits>: the build job declared no digest, so there is nothing to verify against", verify)
				fmt.Fprintf(cmd.ErrOrStderr(), "::error::%v\n", err)
				return err
			}
			got, err := TreeDigest(args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "::error::%v\n", err)
				return err
			}
			if cmd.Flags().Changed("verify") && got != verify {
				err := fmt.Errorf("the downloaded tree is %s, the build job declared %s", got, verify)
				fmt.Fprintf(cmd.ErrOrStderr(), "::error::%v\n", err)
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), got)
			return nil
		},
	}
	cmd.Flags().StringVar(&verify, "verify", "", "refuse unless the tree's digest equals this value")
	return cmd
}
