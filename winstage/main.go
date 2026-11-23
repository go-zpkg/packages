// Command winstage lists what a windows build staged, refusing anything that
// is not exactly the layout the build writes.
//
// The windows factories build on a self-hosted runner that holds no secret and
// upload `out/`. A hosted job downloads it, verifies its digest (distdigest) and
// publishes each package. The digest proves the tree is the one the build job
// produced; it does not make the names inside it safe to hand to a publisher,
// because the build job runs other people's code. So every path that reaches a
// publish command is re-derived here, from a closed set of shapes:
//
//	<slug>/package.yml
//	<slug>/windows/x86-64/versions.txt
//	<slug>/windows/x86-64/v<MAJOR>.<MINOR>[.<PATCH>].tar.gz
//
// where <slug> is one to four path segments of [A-Za-z0-9._-], none starting
// with a dot. A symlink, a device or any other shape is an error, not a skip.
//
// Output, one line per tarball: slug TAB version TAB path relative to DIR.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var osExit = os.Exit

// maxFiles bounds the walk. A variable only so a test can lower it.
var maxFiles = 100_000

var (
	segRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	tarRe = regexp.MustCompile(`^v([0-9]+\.[0-9]+(?:\.[0-9]+)?)\.tar\.gz$`)
)

// Package is one publishable tarball.
type Package struct{ Slug, Version, Path string }

func validSlug(parts []string) bool {
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if !segRe.MatchString(p) {
			return false
		}
	}
	return true
}

// classify returns the package a staged file is, nil for the files that
// accompany one, and an error for anything else.
func classify(rel string) (*Package, error) {
	parts := strings.Split(rel, "/")
	n := len(parts)
	last := parts[n-1]
	inPlatform := n >= 4 && parts[n-2] == "x86-64" && parts[n-3] == "windows"
	switch {
	case last == "package.yml" && validSlug(parts[:n-1]):
		return nil, nil
	case inPlatform && last == "versions.txt" && validSlug(parts[:n-3]):
		return nil, nil
	case inPlatform && tarRe.MatchString(last) && validSlug(parts[:n-3]):
		return &Package{
			Slug:    strings.Join(parts[:n-3], "/"),
			Version: tarRe.FindStringSubmatch(last)[1],
			Path:    rel,
		}, nil
	}
	return nil, fmt.Errorf("%q is not a file a windows build stages", rel)
}

// List returns the tarballs under dir, in path order, or the first thing that
// is not part of the staged layout.
func List(dir string) ([]Package, error) {
	var out []Package
	count := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%v)", p, d.Type())
		}
		if count++; count > maxFiles {
			return fmt.Errorf("more than %d files", maxFiles)
		}
		// WalkDir only yields paths under dir, so Rel cannot fail.
		rel, _ := filepath.Rel(dir, p)
		pkg, err := classify(filepath.ToSlash(rel))
		if pkg != nil {
			out = append(out, *pkg)
		}
		return err
	})
	return out, err
}

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
	return &cobra.Command{
		Use:          "winstage DIR",
		Short:        "list the tarballs a windows build staged, refusing any other shape",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkgs, err := List(args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "::error::%v\n", err)
				return err
			}
			for _, p := range pkgs {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", p.Slug, p.Version, p.Path)
			}
			return nil
		},
	}
}
