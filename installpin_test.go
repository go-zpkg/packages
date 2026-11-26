package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// ⛔⛔ A PIN THAT CANNOT INSTALL STOPS EVERY BUILD BEFORE ANY RECIPE RUNS.
//
// go-zpkg/packages#487 pinned `go install github.com/go-zpkg/zpkx@v0.39.0` in
// build.yml and sovereign.yml. Every zpkx tag up to v0.39.0 declares
// `module github.com/go-zpkg/zpkgx` (the repository was renamed, the module
// line was not), so Go refuses the install:
//
//	version constraints conflict: module declares its path as:
//	github.com/go-zpkg/zpkgx but was required as: github.com/go-zpkg/zpkx
//
// The smoke run died in "build zpkb, pkgx and pkgm (static)", before a single
// recipe. zpkbpin_test.go only judges zpkb's version against a floor; nothing
// asked the question this file asks, for every pinned `go install`: does the
// go.mod AT THAT VERSION declare the path we are installing from? Go verifies
// that path itself, so the only way to know before merging is to read it.
//
// `@main` is not judged here (it has no version to resolve and is by
// definition the module's current declaration); only `@vX.Y.Z` pins are.
//
// The network is the point: the question is about what the module proxy
// serves for a tag, which no file in this tree can answer. The test runs in
// the CI test job (hosted runner, network) and is skipped only by -short.

// installPinRE matches `go install <path>@vX.Y.Z`, the path possibly quoted.
var installPinRE = regexp.MustCompile(`go install "?([A-Za-z0-9][A-Za-z0-9._/-]*)@(v[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9.+-]*)`)

// installPinsExpected is the denominator, asserted so a respelling of these
// lines cannot turn the scan into one that reads nothing and passes.
// 12: build.yml (zpkx, zpkb, zpkm, and zpkb again in its publish job),
// sovereign.yml (zpkb, zpkx, zpkm, and zpkb again in its publish job),
// wasip1-c.yml (zpkb), windows-c.yml (zpkb), ci.yml (vulngate, actionlint).
// 13 → 17 with windows.yml and windows-rust.yml (zpkb in the build job's lan
// lane and again in the hosted publish job, each).
const installPinsExpected = 20

type installPin struct {
	file    string
	pkg     string // package path as written after `go install`
	version string
}

// scanInstallPins reads every workflow under dir.
func scanInstallPins(dir string) ([]installPin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var pins []installPin
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for _, m := range installPinRE.FindAllStringSubmatch(string(body), -1) {
			pins = append(pins, installPin{file: p, pkg: m[1], version: m[2]})
		}
	}
	return pins, nil
}

// modFetcher returns the go.mod of module@version, or ok=false when that
// module path has no such version (a 404/410 from the proxy).
type modFetcher func(modulePath, version string) (data []byte, ok bool, err error)

// checkInstallPin finds the module that provides pkg at version, trying the
// longest path prefix first, and returns an error when no prefix resolves or
// when the go.mod it serves declares a different path.
func checkInstallPin(fetch modFetcher, pin installPin) error {
	for p := pin.pkg; p != "." && p != "/" && strings.Contains(p, "/"); p = path.Dir(p) {
		data, ok, err := fetch(p, pin.version)
		if err != nil {
			return fmt.Errorf("resolving %s@%s: %w", p, pin.version, err)
		}
		if !ok {
			continue
		}
		declared := modfile.ModulePath(data)
		if declared != p {
			return fmt.Errorf("`go install %s@%s` cannot install: the go.mod at that version declares "+
				"`module %s` but it is required as %s (go: version constraints conflict) — pin a tag whose go.mod "+
				"declares the path, or release one", pin.pkg, pin.version, declared, p)
		}
		return nil
	}
	return fmt.Errorf("`go install %s@%s`: no module on the proxy provides that package at that version",
		pin.pkg, pin.version)
}

// proxyFetcher reads <base>/<escaped path>/@v/<version>.mod, retrying server
// errors: a flaky proxy must not redden a pull request as if a pin were wrong,
// but an unanswered question is an error, never a pass.
func proxyFetcher(base string) modFetcher {
	return func(modulePath, version string) ([]byte, bool, error) {
		esc, err := module.EscapePath(modulePath)
		if err != nil {
			return nil, false, err
		}
		escV, err := module.EscapeVersion(version)
		if err != nil {
			return nil, false, err
		}
		url := base + "/" + esc + "/@v/" + escV + ".mod"
		var last error
		for attempt := 0; attempt < 4; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
			resp, err := http.Get(url)
			if err != nil {
				last = err
				continue
			}
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK && rerr == nil:
				return body, true, nil
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				return nil, false, nil
			default:
				last = fmt.Errorf("GET %s: HTTP %d (read: %v)", url, resp.StatusCode, rerr)
			}
		}
		return nil, false, last
	}
}

func TestEveryPinnedInstallDeclaresTheModulePathItIsInstalledFrom(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the module proxy")
	}
	pins, err := scanInstallPins(filepath.Join(".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != installPinsExpected {
		t.Errorf("found %d pinned `go install` line(s), expected %d — if a lane was added, removed or respelt, "+
			"move installPinsExpected deliberately", len(pins), installPinsExpected)
	}
	fetch := proxyFetcher("https://proxy.golang.org")
	seen := map[string]error{}
	for _, pin := range pins {
		key := pin.pkg + "@" + pin.version
		if _, done := seen[key]; !done {
			seen[key] = checkInstallPin(fetch, pin)
		}
		if err := seen[key]; err != nil {
			t.Errorf("%s: %v", pin.file, err)
		}
	}
}

// The offline half: the logic of the gate, against a proxy that serves the
// exact shape of the failure.
func TestInstallPinCheckAgainstAFakeProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github.com/go-zpkg/zpkx/@v/v0.39.0.mod":
			fmt.Fprint(w, "module github.com/go-zpkg/zpkgx\n\ngo 1.25\n")
		case "/github.com/go-zpkg/zpkx/@v/v0.40.0.mod":
			fmt.Fprint(w, "module github.com/go-zpkg/zpkx\n\ngo 1.25\n")
		case "/github.com/go-zpkg/zpkb/@v/v0.100.0.mod":
			fmt.Fprint(w, "module github.com/go-zpkg/zpkb\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	fetch := proxyFetcher(srv.URL)

	cases := []struct {
		pkg, ver string
		wantErr  string // substring; "" = pass
	}{
		{"github.com/go-zpkg/zpkx", "v0.39.0", "declares `module github.com/go-zpkg/zpkgx`"},
		{"github.com/go-zpkg/zpkx", "v0.40.0", ""},
		{"github.com/go-zpkg/zpkb/cmd/zpkb", "v0.100.0", ""},
		{"github.com/go-zpkg/zpkx", "v9.9.9", "no module on the proxy"},
	}
	for _, c := range cases {
		err := checkInstallPin(fetch, installPin{file: "x.yml", pkg: c.pkg, version: c.ver})
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s@%s: unexpected error %v", c.pkg, c.ver, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s@%s: error %v, want one containing %q", c.pkg, c.ver, err, c.wantErr)
		}
	}
}
