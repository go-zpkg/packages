package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// realHTTPGet is the shipped seam, captured before any test replaces the var.
//
// Go initialises a package-level variable after the ones it reads, so this
// holds the function literal from main.go however the tests reorder
// themselves — which matters, because every other test in this package
// assigns to httpGet.
var realHTTPGet = httpGet

// ⛔ THE SEAM ITSELF WAS NEVER EXERCISED, AND THE GATE COULD NOT SAY SO.
// `go tool cover -func` attributes a block to the enclosing function
// DECLARATION; the body of `var httpGet = func(…)` encloses none, so its
// seven statements were dropped from the per-function list and from the total
// the 100 % gate compared against. See #458. They are the only code in this
// program that opens a socket, which is the part a reader most wants covered.
func TestTheRealHTTPGetReadsAResponseAndReportsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"latest":"1.2.3"}`))
	}))
	defer srv.Close()

	code, body, err := realHTTPGet(srv.URL)
	if err != nil {
		t.Fatalf("a served response was an error: %v", err)
	}
	if code != http.StatusTeapot {
		t.Errorf("code = %d, want %d — the status is reported, not just the body", code, http.StatusTeapot)
	}
	if !strings.Contains(string(body), "1.2.3") {
		t.Errorf("body = %q, want the served document", body)
	}

	// And the other half: a request that cannot be made is an error, not an
	// empty body with a zero status, which `run` would read as "answered, and
	// said nothing".
	srv.Close()
	code, body, err = realHTTPGet(srv.URL)
	if err == nil {
		t.Fatal("a refused connection was not an error")
	}
	if code != 0 || body != nil {
		t.Errorf("a failed request returned code %d and %d byte(s); it must return neither", code, len(body))
	}
}

// The read is bounded: an endpoint that answers with more than the limit is
// truncated rather than taken into memory whole. The limit is 1 MiB, so this
// serves more than that and asks for exactly the limit back.
func TestTheRealHTTPGetBoundsTheBodyItReads(t *testing.T) {
	const limit = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, limit+4096))
	}))
	defer srv.Close()

	_, body, err := realHTTPGet(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != limit {
		t.Errorf("read %d byte(s), want the %d-byte limit: an unbounded read of whatever answers is how a redirect becomes this process's memory",
			len(body), limit)
	}
}
