// Command covgate decides the 100 % coverage verdict from the coverage
// PROFILE, which holds every block, rather than from `go tool cover -func`,
// which does not.
//
// # WHY THE INSTRUMENT HAD TO CHANGE (#458)
//
// `-func` attributes each profile block to the enclosing function
// DECLARATION. A block inside a package-level `var f = func(){…}` belongs to
// no declaration, so it is dropped — from the per-function list and from the
// `total:` line the old gate compared against "100.0%". Measured 2026-11-14
// at b16498c, on ONE profile:
//
//	go tool cover -func   total: 100.0 %
//	the profile itself    99.499374 %, 12 uncovered statements in 6 blocks
//
// The twelve were npmtag's `httpGet` and sonamesweep's `runCmd` — in each
// program, the one piece of code that opens a socket or execs a child. Both
// are vars so that tests can replace them, which is the right design and is
// precisely why the gate could not see them.
//
// Rounding was not the mechanism and is worth ruling out explicitly, because
// it is the first thing anyone suspects: the comparison was an exact string
// match and `-func` prints one decimal, so ONE uncovered statement out of
// 2397 already prints "99.9%". The twelve were not inside a tolerance, they
// were outside the count.
//
// # WHY IT IS A PROGRAM AND NOT A PIPELINE
//
// The verdict is "no block has count 0". A shell pipeline reaching that
// answer cannot tell "no uncovered blocks" from "no blocks parsed" — an
// empty file, a profile whose format moved, a path typo and a clean run all
// print nothing and exit 0. Every refusal below exists because the silent
// version of it would read as a pass.
//
//	go test ./... -covermode=atomic -coverprofile=cov.out
//	go run ./covgate -profile cov.out
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// osExit is a seam so a test can exercise the failure path without exiting.
var osExit = os.Exit

func main() {
	fs := flag.NewFlagSet("covgate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "cov.out", "coverage profile to judge")
	show := fs.Int("show", 40, "how many uncovered blocks to name")
	if err := fs.Parse(os.Args[1:]); err != nil {
		osExit(2)
		return
	}
	f, err := os.Open(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covgate:", err)
		osExit(2)
		return
	}
	defer f.Close()

	if err := run(f, os.Stdout, *show); err != nil {
		fmt.Fprintln(os.Stderr, "covgate:", err)
		osExit(1)
	}
}

// block is one record of the profile: a span of source, how many statements
// it holds, and how many times the tests entered it.
type block struct {
	where string
	stmts int
	count int
}

// parse reads a coverage profile.
//
// ⛔ IT NEVER LOOKS AT A FUNCTION NAME, which is the entire point: the
// profile's records are spans of source, and a span counts whether or not any
// declaration encloses it.
func parse(r io.Reader) ([]block, error) {
	var out []block
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "mode:") {
			if line != 1 {
				return nil, fmt.Errorf("line %d: a second mode: header — this is not one profile", line)
			}
			continue
		}
		// "name.go:startLine.col,endLine.col numStatements count"
		fields := strings.Fields(s)
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d is not a profile record: %q", line, s)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: unreadable statement count: %q", line, s)
		}
		c, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("line %d: unreadable hit count: %q", line, s)
		}
		if n < 0 || c < 0 {
			return nil, fmt.Errorf("line %d: a negative count: %q", line, s)
		}
		out = append(out, block{where: fields[0], stmts: n, count: c})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func run(r io.Reader, w io.Writer, show int) error {
	blocks, err := parse(r)
	if err != nil {
		return err
	}
	// ⛔ AN EMPTY PROFILE IS NOT FULL COVERAGE. It is the shape of a test run
	// that compiled nothing, a path that pointed at the wrong file, and a
	// profile written by a crashed run. All three would otherwise be the
	// repository's best-ever result.
	if len(blocks) == 0 {
		return fmt.Errorf("the profile holds no blocks at all, which is not 100 %% coverage")
	}

	var total, covered int
	var gaps []block
	byPkg := map[string]int{}
	for _, b := range blocks {
		total += b.stmts
		if b.count > 0 {
			covered += b.stmts
			continue
		}
		gaps = append(gaps, b)
		byPkg[dirOf(b.where)] += b.stmts
	}
	if total == 0 {
		return fmt.Errorf("the profile's %d block(s) hold no statements, which is not 100 %% coverage", len(blocks))
	}

	fmt.Fprintf(w, "blocks      %d\n", len(blocks))
	fmt.Fprintf(w, "statements  %d\n", total)
	fmt.Fprintf(w, "covered     %d\n", covered)
	fmt.Fprintf(w, "coverage    %.4f %%\n", float64(covered)/float64(total)*100)

	if len(gaps) == 0 {
		fmt.Fprintln(w, "\nevery block of the profile was entered")
		return nil
	}

	// Named, not merely counted: the next person's first question is which.
	fmt.Fprintf(w, "\nuncovered statements by package:\n")
	for _, p := range sortedByCount(byPkg) {
		fmt.Fprintf(w, "  %5d  %s\n", byPkg[p], p)
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].where < gaps[j].where })
	fmt.Fprintf(w, "\nuncovered blocks:\n")
	for i, b := range gaps {
		if i == show {
			fmt.Fprintf(w, "  … and %d more\n", len(gaps)-show)
			break
		}
		fmt.Fprintf(w, "  %s  (%d statement(s))\n", b.where, b.stmts)
	}
	return fmt.Errorf("%d statement(s) in %d block(s) were never entered", total-covered, len(gaps))
}

// dirOf is the package a profile path belongs to.
func dirOf(where string) string {
	path := where
	if i := strings.IndexByte(path, ':'); i >= 0 {
		path = path[:i]
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return path
}

// sortedByCount orders packages by how much they owe, worst first, and by
// name within a tie so two runs print the same thing.
func sortedByCount(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
