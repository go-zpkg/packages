package main

import (
	"strings"
	"testing"
)

const lanDest = "oci://http://10.46.81.1:5111/seed/pass1"

func seedIn() Inputs {
	in := okTo(lanDest)
	in.Flags["bootstrap"] = "seed"
	in.MaxVersions = "1"
	return in
}

func TestASeedPassReadsOneConstantRegistryAndIsNotAHostBootstrap(t *testing.T) {
	r, err := Validate(seedIn())
	if err != nil {
		t.Fatal(err)
	}
	env := envOf(r)
	if env["SEED_FROM"] != "oci://ghcr.io/go-pkgx/packages" {
		t.Errorf("SEED_FROM = %q", env["SEED_FROM"])
	}
	// `seed` must never reach `zpkb factory --bootstrap`, which would build the
	// seed pass from the runner's own tools.
	if env["BOOTSTRAP_HOST"] != "" {
		t.Errorf("BOOTSTRAP_HOST = %q: a seed pass is not a host bootstrap", env["BOOTSTRAP_HOST"])
	}
	if env["PUBLISH_LANE"] != "lan" || env["PUBLISH_TO"] != lanDest {
		t.Errorf("lane/destination: %q %q", env["PUBLISH_LANE"], env["PUBLISH_TO"])
	}
}

func TestOtherRunsHaveNoSeedAndAHostBootstrapKeepsItsFlag(t *testing.T) {
	r, err := Validate(okTo(lanDest))
	if err != nil {
		t.Fatal(err)
	}
	if e := envOf(r); e["SEED_FROM"] != "" || e["BOOTSTRAP_HOST"] != "" {
		t.Errorf("plain run: %v", e)
	}
	in := okTo("oci://127.0.0.1:5000/seed")
	in.Flags["bootstrap"] = "1"
	r, err = Validate(in)
	if err != nil {
		t.Fatal(err)
	}
	if e := envOf(r); e["SEED_FROM"] != "" || e["BOOTSTRAP_HOST"] != "1" {
		t.Errorf("host bootstrap: %v", e)
	}
}

// A seed pass's outputs were built in somebody else's environment. They may be
// pushed to the LAN, signed with an ephemeral key, and nowhere else.
func TestASeedPassNeverPublishesToAHostedRegistry(t *testing.T) {
	const want = "bootstrap=seed publishes only to the LAN"
	for _, dest := range []string{prodDest, smokeDest, "oci://ghcr.io/go-pkgx/packages", "oci://ghcr.io/other/x", "oci://registry.example.org/x"} {
		in := seedIn()
		in.OCITo = dest
		_, err := Validate(in)
		if err == nil {
			t.Errorf("%s: accepted", dest)
		}
	}
	in := seedIn()
	in.OCITo = prodDest
	refused(t, "production", in, want)
	in.OCITo = smokeDest
	refused(t, "smoke", in, want)
	in = seedIn()
	in.OCITo = ""
	refused(t, "no destination", in, "publishing requires an explicit destination")
}

func TestASeedPassIsRefusedWithWhatItCannotBeCombinedWith(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(*Inputs)
		want string
	}{
		{"mirror_from", func(i *Inputs) { i.MirrorFrom = "oci://ghcr.io/x/y"; i.RequireOrigin = "1" }, "one run does not do both"},
		{"seed_dist", func(i *Inputs) { i.OCITo = ""; i.SeedDist = "oci://127.0.0.1:5000/seed" }, "cannot be combined with seed_dist"},
		{"require_origin", func(i *Inputs) { i.RequireOrigin = "1" }, "needs mirror_from"},
		{"source keys", func(i *Inputs) { i.SourceKeys = "k:" + seedKey }, "source_trusted_keys is read only"},
		{"test_only", func(i *Inputs) { i.Flags["test_only"] = "1" }, "pick one"},
		{"sovereign off", func(i *Inputs) { i.Flags["sovereign"] = "0" }, "sovereign=0"},
		{"sovereign false", func(i *Inputs) { i.Flags["sovereign"] = "false" }, "sovereign=0"},
		{"max_versions blank", func(i *Inputs) { i.MaxVersions = "" }, "max_versions blank"},
		{"go-pkgx as dist_from", func(i *Inputs) { i.DistFrom = "oci://ghcr.io/go-pkgx/packages" }, "go-pkgx is refused"},
	} {
		in := seedIn()
		c.mut(&in)
		refused(t, c.name, in, c.want)
	}
}

// F3: the seed is verified against the compiled-in keys, so a seed pass takes
// no extra trusted key at all.
func TestASeedPassRefusesExtraTrustedKeys(t *testing.T) {
	in := seedIn()
	in.ExtraKeys = "lan-1:RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"
	refused(t, "seed with extra keys", in, "compiled-in keys only")
}

// F5: extra_trusted_keys is for a LAN or loopback destination only.
func TestExtraTrustedKeysNeedALanDestination(t *testing.T) {
	const key = "lan-1-1:RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"
	for _, dest := range []string{smokeDest, "oci://ghcr.io/other/x", "oci://registry.example.org/x"} {
		in := okTo(dest)
		in.ExtraKeys = key
		refused(t, dest, in, "LAN or loopback destination")
	}
	in := okTo(lanDest)
	in.DistFrom = lanDest
	in.ExtraKeys = key
	if _, err := Validate(in); err != nil {
		t.Errorf("a LAN destination was refused: %v", err)
	}
}

// The ledger digest a second pass compares installed bytes against is a
// canonical sha256, belongs only to a pass that has extra keys, and reaches the
// environment under the name the pass-2 step reads.
func TestPass1LedgerDigestIsValidatedAndExported(t *testing.T) {
	const key = "lan-1-1:RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"
	good := "sha256:" + strings.Repeat("ab", 32)
	for _, bad := range []string{"abc", strings.Repeat("ab", 32), "sha256:" + strings.Repeat("AB", 32), "sha256:" + strings.Repeat("ab", 31), good + "x", "xsha256:" + strings.Repeat("ab", 32)} {
		in := okTo(lanDest)
		in.DistFrom = lanDest
		in.ExtraKeys = key
		in.Pass1Ledger = bad
		refused(t, "ledger "+bad, in, "pass1_ledger_sha256 must be sha256:")
	}
	in := okTo(lanDest)
	in.Pass1Ledger = good
	refused(t, "ledger without keys", in, "needs extra_trusted_keys")
	in.DistFrom = lanDest
	in.ExtraKeys = key
	r, err := Validate(in)
	if err != nil {
		t.Fatalf("a canonical digest was refused: %v", err)
	}
	got := ""
	for _, p := range r.Env {
		if p[0] == "PASS1_LEDGER_SHA256" {
			got = p[1]
		}
	}
	if got != good {
		t.Errorf("PASS1_LEDGER_SHA256 = %q, want %q", got, good)
	}
}

// F5: a key is trusted only under the label of the run that minted it, and
// that run is an earlier one than the run that trusts it.
func TestAnExtraKeyMustNameTheEarlierRunThatMintedIt(t *testing.T) {
	const pub = "RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"
	cases := []struct{ name, label, run, want string }{
		{"a free-form label", "seed", "1000", "is not lan-<pass-1 run id>-<attempt>"},
		{"no attempt", "lan-5", "1000", "is not lan-"},
		{"a non-numeric run", "lan-x-1", "1000", "is not lan-"},
		{"an empty attempt", "lan-5-", "1000", "is not lan-"},
		{"the run itself", "lan-1000-1", "1000", "earlier run"},
		{"a later run", "lan-1001-1", "1000", "earlier run"},
		{"run zero", "lan-0-1", "1000", "earlier run"},
		{"this run is unknown", "lan-5-1", "", "run's id"},
		{"this run is not a number", "lan-5-1", "12x", "run's id"},
	}
	for _, c := range cases {
		in := okTo(lanDest)
		in.DistFrom = lanDest
		in.RunID = c.run
		in.ExtraKeys = c.label + ":" + pub
		refused(t, c.name, in, c.want)
	}
	in := okTo(lanDest)
	in.DistFrom = lanDest
	in.RunID = "1000"
	in.ExtraKeys = "lan-999-3:" + pub
	if _, err := Validate(in); err != nil {
		t.Errorf("the label of an earlier run was refused: %v", err)
	}
	netAllow := func(r Result) string {
		for _, p := range r.Env {
			if p[0] == "NET_ALLOW" {
				return p[1]
			}
		}
		return "missing"
	}
	if r, _ := Validate(in); netAllow(r) != "10.46.81.1:5111" {
		t.Errorf("NET_ALLOW = %q, want the pass-1 registry and nothing else", netAllow(r))
	}
	in.OCITo = "oci://127.0.0.1:5000/stage"
	if r, _ := Validate(in); netAllow(r) != "127.0.0.1:5000,10.46.81.1:5111" {
		t.Errorf("NET_ALLOW = %q, want the staging registry then pass-1's", netAllow(r))
	}
	in.OCITo = lanDest
	// A pass that trusts a pass-1 key reads nothing but LAN registries, named
	// by an IPv4 address and a port.
	for name, d := range map[string]string{
		"hosted":  "oci://ghcr.io/go-zpkg/packages",
		"no port": "oci://127.0.0.1/seed",
		"a name":  "oci://localhost.example:5000/seed",
		"port 0":  "oci://127.0.0.1:0/seed",
		"go-zpkg": "oci://ghcr.io/go-zpkg/smoke",
	} {
		bad := in
		bad.DistFrom = d
		refused(t, name, bad, "second pass")
	}
	// A name is refused even for localhost: the namespace is given an address.
	local := in
	local.DistFrom = "oci://http://localhost:5111/seed"
	refused(t, "localhost", local, "LAN or loopback registry")
	// A registry named twice is one endpoint.
	twice := in
	twice.OCITo = "oci://http://10.46.81.1:5111/stage"
	if r, err := Validate(twice); err != nil || netAllow(r) != "10.46.81.1:5111" {
		t.Errorf("one registry twice: NET_ALLOW = %q, err = %v, want it once", netAllow(r), err)
	}
	// A public IPv4 literal is well-formed and still not a LAN registry.
	pub4 := in
	pub4.DistFrom = "oci://http://8.8.8.8:5000/seed"
	refused(t, "a public address", pub4, "LAN or loopback registry")
	in.ExtraKeys = ""
	if r, _ := Validate(in); netAllow(r) != "" {
		t.Errorf("NET_ALLOW = %q for a run that trusts no extra key, want blank", netAllow(r))
	}
	in.ExtraKeys = ""
	if _, err := Validate(in); err != nil {
		t.Errorf("no extra key must not need a label: %v", err)
	}
}
