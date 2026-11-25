// Command zpkxcensus fails when a recipe invokes zpkx (or pkgx) itself.
//
// Under a seed pass a build environment is made of another registry's
// artifacts, and a nested recipe-level `zpkx +tool` has no seed access BY
// DESIGN: any tool a recipe needs must come from the plan, as a declared build
// dependency. A recipe that reaches for zpkx instead works on a normal build and
// fails closed under a seed, so the census runs before pass 1 and names the
// recipe to fix.
//
// It reads the trees it is given (the overlay's projects/, the pantry's
// projects/, our overrides/) and judges every recipe file in them: package.hcl,
// package.yml, *.patch, *.sh. A line that is only a comment is skipped.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// invocation matches zpkx anywhere as a word, and pkgx only where it is run
// with a tool or a flag (a bare "pkgx" names the upstream project in prose).
var invocation = regexp.MustCompile("(?:^|[^A-Za-z0-9_./-])(?:zpkx\\b|pkgx[ \\t]+(?:\\+|-))")

// recipeFile says which files are recipe text.
func recipeFile(name string) bool {
	switch filepath.Ext(name) {
	case ".hcl", ".yml", ".yaml", ".patch", ".sh":
		return true
	}
	return false
}

// Hit is one invocation found.
type Hit struct {
	Path string
	Line int
	Text string
}

// scanReader reports the invocations in one file's text.
func scanReader(path string, r io.Reader) ([]Hit, error) {
	var hits []Hit
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		// A patch line carries its +/- marker before the text.
		body := strings.TrimLeft(strings.TrimLeft(line, "+- \t"), " \t")
		if strings.HasPrefix(body, "#") || strings.HasPrefix(body, "//") {
			continue
		}
		if invocation.MatchString(line) {
			hits = append(hits, Hit{path, n, strings.TrimSpace(line)})
		}
	}
	return hits, sc.Err()
}

// Scan walks root and returns every invocation, and how many files it read. A
// root that holds no recipe file is an error: a census that read nothing says
// nothing.
func Scan(root string) ([]Hit, int, error) {
	var hits []Hit
	files := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !recipeFile(d.Name()) {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		h, serr := scanReader(p, f)
		cerr := f.Close()
		if serr != nil || cerr != nil {
			return errors.Join(serr, cerr)
		}
		files++
		hits = append(hits, h...)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return hits, files, nil
}

// Projects reads a toolchain list: one project per line, `# comment` stripped,
// an `@version` suffix dropped, blank and indented-comment lines skipped.
func Projects(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		p, _, _ := strings.Cut(f[0], "@")
		out = append(out, p)
	}
	return out, sc.Err()
}

// scanProjects judges only the named projects in every tree. A project found
// in no tree is an error: a root nobody read is a root nobody cleared.
func scanProjects(trees, projects []string) ([]Hit, int, error) {
	var all []Hit
	total := 0
	for _, p := range projects {
		found := false
		for _, t := range trees {
			dir := filepath.Join(t, filepath.FromSlash(p))
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				continue
			}
			h, n, err := Scan(dir)
			if err != nil {
				return nil, 0, err
			}
			found = found || n > 0
			all = append(all, h...)
			total += n
		}
		if !found {
			return nil, 0, fmt.Errorf("project %s has no recipe file in any tree given: a root nobody read is a root nobody cleared", p)
		}
	}
	return all, total, nil
}

// RootCmd builds the command; tests drive it with SetArgs, SetOut and SetErr.
func RootCmd() *cobra.Command {
	var roots, projects []string
	var list string
	cmd := &cobra.Command{
		Use:          "zpkxcensus --recipes DIR [--recipes DIR ...]",
		Short:        "fail if a recipe invokes zpkx itself, which a seed pass cannot serve",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(roots) == 0 {
				return errors.New("--recipes is required")
			}
			var all []Hit
			total := 0
			var err error
			if len(projects) > 0 || list != "" {
				ps := projects
				if list != "" {
					f, oerr := os.Open(list)
					if oerr != nil {
						return oerr
					}
					lp, perr := Projects(f)
					if err := errors.Join(perr, f.Close()); err != nil {
						return err
					}
					ps = append(lp, ps...)
				}
				if len(ps) == 0 {
					return fmt.Errorf("%s names no project", list)
				}
				all, total, err = scanProjects(roots, ps)
				if err != nil {
					return err
				}
			} else {
				for _, r := range roots {
					h, n, serr := Scan(r)
					if serr != nil {
						return serr
					}
					if n == 0 {
						return fmt.Errorf("%s holds no recipe file: a census that read nothing says nothing", r)
					}
					all = append(all, h...)
					total += n
				}
			}
			sort.Slice(all, func(i, j int) bool {
				if all[i].Path != all[j].Path {
					return all[i].Path < all[j].Path
				}
				return all[i].Line < all[j].Line
			})
			out := cmd.OutOrStdout()
			for _, h := range all {
				fmt.Fprintf(out, "%s:%d: %s\n", h.Path, h.Line, h.Text)
			}
			if len(all) > 0 {
				return fmt.Errorf("%d recipe line(s) invoke zpkx or pkgx: under a seed a nested call has no seed access, so declare the tool as a build dependency of the recipe", len(all))
			}
			fmt.Fprintf(out, "%d recipe file(s) read, none invokes zpkx\n", total)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&roots, "recipes", nil, "a tree of recipes to judge (repeatable)")
	cmd.Flags().StringVar(&list, "projects-from", "", "judge only the projects this toolchain list names (builder/toolchain.txt)")
	cmd.Flags().StringArrayVar(&projects, "project", nil, "judge this project too (repeatable); with either flag, --recipes names the trees holding projects/<project>")
	return cmd
}

var osExit = os.Exit

func main() {
	if err := RootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "::error::"+err.Error())
		osExit(1)
	}
}
