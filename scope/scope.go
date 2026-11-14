package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-zpkg/artifact"
)

// platformScope is what one platform's catalogue says about the scope.
type platformScope struct {
	Platform string
	// Named is how many projects the catalogue NAMES. It is reported and
	// never summed: it is the pantry's size as seen from here, not work.
	Named int
	// Published is the set the registry actually HOLDS for this platform.
	Published map[string]bool
	// Closure is what a published project's closure requires and that is
	// published, on this platform, by nobody.
	Closure map[string]bool
}

// scopeOf reduces one platform's catalogue to its scope sets.
//
// ⛔ A LEAF IS NOT A PUBLISHED PROJECT. CatalogProject.Versions is legally
// empty and means "named by the pantry, nothing published here" — the honest
// answer for most of a pantry on most architectures. Counting names answered
// 1966 on linux/x86-64 against 1411 published, and 1966 is larger than the
// 1932 recipes the upstream pantry holds at all: an impossible number, which
// is the only reason the mistake was caught. The version list is the test.
func scopeOf(platform string, cat artifact.Catalog, depth int) (platformScope, error) {
	s := platformScope{
		Platform:  platform,
		Named:     len(cat.Projects),
		Published: map[string]bool{},
		Closure:   map[string]bool{},
	}
	for _, p := range cat.Projects {
		if len(p.Versions) > 0 {
			s.Published[p.Project] = true
		}
	}

	// ⛔ THE CONTROL: Catalog.Projects IS the list, and the tree the browser
	// walks must reach exactly the same projects. It does so only if every
	// leaf resolves through Lookup and no name is reachable twice, and nothing
	// guarantees either. Counting both and refusing a disagreement is what
	// keeps a future change to the tree code from silently moving a published
	// figure — the judge and the subject have to count alike.
	walkNamed, walkPublished := 0, 0
	var walk func(prefix string)
	walk = func(prefix string) {
		for _, n := range cat.Children(prefix) {
			if n.Leaf {
				walkNamed++
				if p, ok := cat.Lookup(n.Name); ok && len(p.Versions) > 0 {
					walkPublished++
				}
			}
			walk(n.Name)
		}
	}
	walk("")
	if walkNamed != s.Named || walkPublished != len(s.Published) {
		return platformScope{}, fmt.Errorf(
			"%s: the tree and Catalog.Projects disagree — tree %d named / %d published, slice %d named / %d published",
			platform, walkNamed, walkPublished, s.Named, len(s.Published))
	}

	for p := range s.Published {
		for _, d := range cat.DepTree(p, depth) {
			if d.Project != p && !s.Published[d.Project] {
				s.Closure[d.Project] = true
			}
		}
	}
	return s, nil
}

// scopeReport is the answer: the per-platform rows and the union over them.
type scopeReport struct {
	Platforms []platformScope
	// Published is every project any platform holds.
	Published map[string]bool
	// Closure is what some closure requires and NO platform publishes.
	Closure map[string]bool
}

// Total is the number of recipes this scope requires.
func (r scopeReport) Total() int { return len(r.Published) + len(r.Closure) }

// measure reads each platform's catalogue through fetch and unions the result.
//
// ⛔ THE UNION IS THE SCOPE, NOT ANY ONE PLATFORM. A project published only
// for darwin still needs a recipe. Measured 2026-11-14, the union was 91 to
// 154 larger than a single platform's figure, so quoting one understates the
// work by exactly the projects the other platforms carry.
//
// ⛔ AND AN UNREADABLE PLATFORM IS NOT AN EMPTY ONE. The error travels out and
// the caller fails, rather than the union silently shrinking by everything
// that platform alone carried — a scope answered by the network.
func measure(platforms []string, depth int, fetch func(osn, arch string) (artifact.Catalog, error)) (scopeReport, error) {
	r := scopeReport{Published: map[string]bool{}, Closure: map[string]bool{}}
	for _, pl := range platforms {
		pl = strings.TrimSpace(pl)
		if pl == "" {
			continue
		}
		osn, arch, ok := strings.Cut(pl, "/")
		if !ok || osn == "" || arch == "" {
			return scopeReport{}, fmt.Errorf("%q is not os/arch", pl)
		}
		cat, err := fetch(osn, arch)
		if err != nil {
			return scopeReport{}, fmt.Errorf("%s is unreadable, which is not a zero: %w", pl, err)
		}
		s, err := scopeOf(pl, cat, depth)
		if err != nil {
			return scopeReport{}, err
		}
		r.Platforms = append(r.Platforms, s)
		for p := range s.Published {
			r.Published[p] = true
		}
		for p := range s.Closure {
			r.Closure[p] = true
		}
	}
	if len(r.Platforms) == 0 {
		return scopeReport{}, fmt.Errorf("no platform was measured: an empty list cannot answer the scope")
	}
	// A project one platform reaches only through a closure is published
	// outright on another more often than not — across the four platforms
	// measured, 4 of the 5 closure-only names were. It is then already counted.
	for p := range r.Published {
		delete(r.Closure, p)
	}
	return r, nil
}

// sortedKeys is the stable order a report prints a set in.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
