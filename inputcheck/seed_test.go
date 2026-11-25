package main

import "testing"

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

// The extra keys of a seed pass are how the second pass believes the first
// one's ephemeral signatures; they stay allowed for a LAN destination.
func TestASeedPassMayTrustAnExtraKey(t *testing.T) {
	in := seedIn()
	in.ExtraKeys = "lan-1:" + seedKey
	if _, err := Validate(in); err != nil {
		t.Fatal(err)
	}
}
