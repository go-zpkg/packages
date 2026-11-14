package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/go-zpkg/artifact"
)

// published is a project the registry holds. The version string is arbitrary;
// what matters to every assertion below is that the list is NOT empty.
func published(name string, deps ...string) artifact.CatalogProject {
	return artifact.CatalogProject{Project: name, Versions: []string{"1.0.0"}, Deps: deps}
}

// namedOnly is a project the catalogue names and the registry does not hold —
// the legal, ordinary state this measurement exists to tell apart from the
// other one.
func namedOnly(name string, deps ...string) artifact.CatalogProject {
	return artifact.CatalogProject{Project: name, Deps: deps}
}

func cat(ps ...artifact.CatalogProject) artifact.Catalog {
	return artifact.Catalog{Generated: "2026-11-14T00:00:00Z", Projects: ps}
}

// from serves a fixed catalogue per platform, and an error for a platform it
// was given none for.
func from(m map[string]artifact.Catalog) func(string, string) (artifact.Catalog, error) {
	return func(osn, arch string) (artifact.Catalog, error) {
		c, ok := m[osn+"/"+arch]
		if !ok {
			return artifact.Catalog{}, fmt.Errorf("no catalogue for %s/%s", osn, arch)
		}
		return c, nil
	}
}

// ⛔ A NAMED PROJECT IS NOT A PUBLISHED ONE. This is the defect that produced
// the first answer to #431 item 2: 1966 "projects", against 1411 the registry
// held and 1932 recipes in the whole upstream pantry — a scope larger than the
// corpus it was a subset of. Mutating `len(p.Versions) > 0` to `true` turns
// this red.
func TestANamedProjectWithNoVersionsIsNotPublished(t *testing.T) {
	s, err := scopeOf("linux/x86-64", cat(
		published("a.org"),
		namedOnly("b.org"),
		namedOnly("c.org"),
	), 8)
	if err != nil {
		t.Fatal(err)
	}
	if s.Named != 3 {
		t.Errorf("named = %d, want 3: the catalogue names all three", s.Named)
	}
	if len(s.Published) != 1 || !s.Published["a.org"] {
		t.Errorf("published = %v, want only a.org: a project with no versions is published nowhere", sortedKeys(s.Published))
	}
}

// ⛔ THE UNION IS THE SCOPE. A project published only for darwin still needs a
// recipe. Mutating measure to return the LAST platform's sets instead of the
// union turns this red, which a sum-free assertion on one platform would not.
func TestTheUnionIsLargerThanAnyOnePlatform(t *testing.T) {
	r, err := measure([]string{"linux/x86-64", "darwin/aarch64"}, 8, from(map[string]artifact.Catalog{
		"linux/x86-64":   cat(published("shared.org"), published("linux-only.org")),
		"darwin/aarch64": cat(published("shared.org"), published("darwin-only.org")),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Published); got != 3 {
		t.Errorf("union published = %d (%v), want 3", got, sortedKeys(r.Published))
	}
	for _, s := range r.Platforms {
		if len(r.Published) <= len(s.Published) {
			t.Errorf("union (%d) is not larger than %s (%d): a per-platform figure was reported as the scope",
				len(r.Published), s.Platform, len(s.Published))
		}
	}
	if r.Total() != 3 {
		t.Errorf("total = %d, want 3", r.Total())
	}
}

// ⛔ AN UNREADABLE PLATFORM IS NOT AN EMPTY ONE. Swallowing the error would
// leave a smaller, plausible, wrong number — a scope decided by the network.
// Mutating the `return` in measure's fetch branch to `continue` turns this red.
func TestAnUnreadablePlatformFailsInsteadOfShrinkingTheUnion(t *testing.T) {
	_, err := measure([]string{"linux/x86-64", "darwin/aarch64"}, 8, from(map[string]artifact.Catalog{
		"linux/x86-64": cat(published("a.org")),
	}))
	if err == nil {
		t.Fatal("a platform that could not be read was counted as holding nothing")
	}
	if !strings.Contains(err.Error(), "darwin/aarch64") {
		t.Errorf("error %q does not name the platform that failed", err)
	}
}

// ⛔ AN EMPTY PLATFORM LIST CANNOT ANSWER THE QUESTION. Without this, a typo in
// --platforms reports TOTAL 0 and exits 0.
func TestNoPlatformIsRefused(t *testing.T) {
	if _, err := measure([]string{"", "  "}, 8, from(nil)); err == nil {
		t.Fatal("an empty platform list produced a scope")
	}
	if _, err := measure([]string{"linux"}, 8, from(nil)); err == nil {
		t.Fatal(`"linux" is not os/arch and was accepted`)
	}
}

// A closure name that ANOTHER platform publishes is already counted; counting
// it again would inflate the scope. Four of the five closure-only names in the
// real measurement were of this kind.
func TestAClosureNameAnotherPlatformPublishesIsNotCountedTwice(t *testing.T) {
	r, err := measure([]string{"linux/x86-64", "darwin/aarch64"}, 8, from(map[string]artifact.Catalog{
		"linux/x86-64":   cat(published("app.org", "lib.org"), namedOnly("lib.org")),
		"darwin/aarch64": cat(published("lib.org")),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Closure) != 0 {
		t.Errorf("closure = %v, want empty: darwin publishes lib.org", sortedKeys(r.Closure))
	}
	if r.Total() != 2 {
		t.Errorf("total = %d, want 2 (app.org, lib.org counted once)", r.Total())
	}
}

// ...and one published NOWHERE is the only kind that adds to the scope.
func TestAClosureNamePublishedNowhereIsCounted(t *testing.T) {
	r, err := measure([]string{"linux/x86-64"}, 8, from(map[string]artifact.Catalog{
		"linux/x86-64": cat(published("app.org", "lib.org"), namedOnly("lib.org")),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Closure) != 1 || !r.Closure["lib.org"] {
		t.Errorf("closure = %v, want [lib.org]", sortedKeys(r.Closure))
	}
	if r.Total() != 2 {
		t.Errorf("total = %d, want 2", r.Total())
	}
}

// ⛔ THE JUDGE AND THE SUBJECT MUST COUNT ALIKE. Catalog.Projects is the list;
// the tree a browser walks must reach the same projects. A duplicate entry is
// the cheapest way to make them disagree, and the measurement must refuse
// rather than publish whichever number it happened to compute.
func TestTheTreeAndTheSliceMustCountAlike(t *testing.T) {
	_, err := scopeOf("linux/x86-64", cat(
		published("a.org"),
		published("a.org"),
	), 8)
	if err == nil {
		t.Fatal("the tree and the slice disagreed and a scope was reported anyway")
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Errorf("error %q does not say the two instruments disagree", err)
	}
}

// --at-least makes this a gate for #440: the republished namespace has a
// figure to reach, and one holding fewer projects has lost some.
func TestAtLeastRefusesAScopeThatShrank(t *testing.T) {
	fetch := from(map[string]artifact.Catalog{
		"linux/x86-64": cat(published("a.org"), published("b.org")),
	})
	var buf bytes.Buffer
	if err := run(&buf, []string{"linux/x86-64"}, 8, 2, fetch); err != nil {
		t.Fatalf("a scope that MET the floor was refused: %v", err)
	}
	buf.Reset()
	err := run(&buf, []string{"linux/x86-64"}, 8, 3, fetch)
	if err == nil {
		t.Fatal("a scope below the floor was accepted")
	}
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "3") {
		t.Errorf("error %q names neither the scope nor the floor", err)
	}
}

func TestRunReportsEveryPlatformAndTheUnion(t *testing.T) {
	var buf bytes.Buffer
	err := run(&buf, []string{"linux/x86-64", "darwin/aarch64"}, 8, 0, from(map[string]artifact.Catalog{
		"linux/x86-64":   cat(published("a.org"), namedOnly("n.org")),
		"darwin/aarch64": cat(published("b.org")),
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"linux/x86-64", "darwin/aarch64", "UNION", "TOTAL 2 recipe(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}
