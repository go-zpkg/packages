package main

import (
	"strings"
	"testing"
)

// A second pass (ZPKG_TRUSTED_KEYS non-empty) runs recipe code with no network
// but the registry it reads. The host is darwin and cannot run unshare, so
// these hold the script's text to the contract.
func TestASecondPassRunsInANetworkNamespaceWithOnlyItsRegistry(t *testing.T) {
	s := readText(t, "builder/sovereign-build.sh")
	for _, want := range []string{
		`net_allow="${SOVEREIGN_NET_ALLOW:-}"`,
		`refusing to run recipe code with the host's network`,
		`netflag="--net"`,
		`unshare --mount --pid --fork $netflag`,
		`"$seed_from" "$seed_ledger" "$net_allow" "$seed_ledger_host" "$@"`,
		`netallow="$8"; seedhost="$9"; shift 9`,
		`ip link set lo up`,
		`UNIX-LISTEN:$root/nf/$i.sock`,
		`UNIX-CONNECT:$r/nf/$i.sock`,
		`SOVEREIGN_NET_ALLOW is set and this is not a second pass`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("sovereign-build.sh lost %q", want)
		}
	}
}

func TestTheBuildStepHandsTheNetworkAllowListToTheScript(t *testing.T) {
	if !strings.Contains(readText(t, ".github/workflows/build.yml"), `SOVEREIGN_NET_ALLOW="${NET_ALLOW:-}"`) {
		t.Error("build.yml does not pass inputcheck's NET_ALLOW to sovereign-build.sh")
	}
}

func TestCIRunsTheNetworkNamespaceTestForReal(t *testing.T) {
	ci := readText(t, ".github/workflows/ci.yml")
	for _, want := range []string{"builder/netns-test.sh", "busybox-static", "ubuntu-24.04"} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml lost %q", want)
		}
	}
	sh := readText(t, "builder/netns-test.sh")
	for _, want := range []string{"NETNS-FAIL: reached", "was accepted", "SOVEREIGN_NET_ALLOW=127.0.0.1:5111", "SEED-FAIL: the ledger was written from inside", "SEED-OK"} {
		if !strings.Contains(sh, want) {
			t.Errorf("netns-test.sh lost %q", want)
		}
	}
}

func TestSovereignWorkflowComputesNetAllowForASecondPass(t *testing.T) {
	s := readText(t, ".github/workflows/sovereign.yml")
	for _, want := range []string{`echo "NET_ALLOW=$net_allow"`, `SOVEREIGN_NET_ALLOW="${NET_ALLOW:-}"`, "names a second pass"} {
		if !strings.Contains(s, want) {
			t.Errorf("sovereign.yml lost %q", want)
		}
	}
}
