// Command scope answers "how many recipes does the sovereign pantry need?"
// (go-zpkg/packages#431 item 2) by measuring the live registry rather than
// upstream's corpus.
//
// It counts what the registry actually HOLDS on each platform, plus what those
// projects' closures require and nobody publishes, and reports the UNION. It
// reads only the catalogue artifact the registry itself publishes, so it is a
// measurement of the registry and not a reading of a corpus that may be read
// but not copied.
//
//	go run ./scope
//	go run ./scope --platforms linux/x86-64,darwin/aarch64
//
// With --at-least it is a gate rather than a report: the republication of
// #440 has a figure to reach, and a namespace holding fewer projects than the
// one it replaces has lost some.
//
//	go run ./scope --at-least 1524
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-zpkg/artifact"
)

// osExit is a seam so a test can exercise the failure path without exiting.
var osExit = os.Exit

// defaultPlatforms are the four the registry publishes for. They are a default
// and not a constant: the point of --platforms is that the set can grow, and
// the union over whatever is asked for is the answer.
const defaultPlatforms = "linux/x86-64,linux/aarch64,darwin/x86-64,darwin/aarch64"

// fetchCatalog is the one place this command touches the network, as a var so
// a test can drive run() and main() from a fixture instead.
var fetchCatalog = func(osn, arch string) (artifact.Catalog, error) {
	c, err := artifact.NewOCIClient(artifact.DistBase)
	if err != nil {
		return artifact.Catalog{}, err
	}
	return artifact.FetchCatalog(c, osn, arch)
}

func main() {
	// A fresh FlagSet, not the package-level default: main is called more
	// than once by its own test, and re-registering a flag on flag.CommandLine
	// panics.
	fs := flag.NewFlagSet("scope", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	plats := fs.String("platforms", defaultPlatforms, "comma-separated os/arch to measure and UNION")
	depth := fs.Int("depth", 64, "closure depth to walk")
	atLeast := fs.Int("at-least", 0, "fail unless the scope is at least this many recipes (0 reports only)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		osExit(2)
		return
	}

	if err := run(os.Stdout, strings.Split(*plats, ","), *depth, *atLeast, fetchCatalog); err != nil {
		fmt.Fprintln(os.Stderr, "scope:", err)
		osExit(1)
	}
}

func run(w io.Writer, platforms []string, depth, atLeast int, fetch func(osn, arch string) (artifact.Catalog, error)) error {
	r, err := measure(platforms, depth, fetch)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "registry   %s\n\n", artifact.DistBase)
	fmt.Fprintf(w, "%-18s %9s %9s %9s\n", "platform", "named", "published", "closure")
	for _, s := range r.Platforms {
		fmt.Fprintf(w, "%-18s %9d %9d %9d\n", s.Platform, s.Named, len(s.Published), len(s.Closure))
	}
	fmt.Fprintf(w, "%-18s %9s %9d %9d\n", "UNION", "-", len(r.Published), len(r.Closure))
	fmt.Fprintf(w, "\nTOTAL %d recipe(s) to write for this scope\n", r.Total())

	// Named, not just counted, while the list is short enough to read: a
	// closure-only project is the one kind of scope entry nobody would think
	// to look for, and four of the five found on 2026-11-14 turned out to be
	// published on another platform.
	if n := len(r.Closure); n > 0 && n <= 40 {
		fmt.Fprintln(w, "\nrequired by a closure, published nowhere:")
		for _, p := range sortedKeys(r.Closure) {
			fmt.Fprintln(w, "   ", p)
		}
	}

	if atLeast > 0 && r.Total() < atLeast {
		return fmt.Errorf("the scope is %d recipe(s), fewer than the %d required: the registry holds less than it did",
			r.Total(), atLeast)
	}
	return nil
}
