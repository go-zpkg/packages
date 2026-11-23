// Command signingkeys derives the PUBLIC half of the two CI signing keys and
// checks the production one against the key clients trust.
//
//	signingkeys --seed FILE --prod FILE [--summary FILE]
//
// Each FILE holds a secret key in the `sign keygen` format (a GitHub secret
// cannot be read back, and a secret key embeds its public half and key id, so
// the public half is recoverable). The files are read, never argv values: the
// secret is not a command-line argument, an environment variable or a log line.
//
// For each key it prints the two-line minisign public-key text and the key id,
// as Markdown to --summary (the job summary) and to stdout. The production key's
// public line must equal artifact.SigningPublicKey; if it does not, or a file is
// empty or not a secret key, it exits 1. A message never quotes a file's content.
//
// ⛔ WHY: the seed key (ARTIFACT_SIGNING_KEY) signs the sovereign seed registry
// and is, by design, not in the default trust set, so a production promotion has
// to be handed it as source_trusted_keys. Its public half was published nowhere
// and the secret cannot be read back (go-zpkg/packages run 45147362828).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-attest/sign"
	"github.com/go-zpkg/artifact"
)

var osExit = os.Exit

// maxKeyFile bounds a read: a secret-key file is a comment line and one base64
// line.
const maxKeyFile = 4096

// Seams for the tests.
var (
	osReadFile   = readBounded
	osAppendFile = appendFile
	trustedKey   = func() string { return artifact.SigningPublicKey }
)

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("signingkeys", flag.ContinueOnError)
	f.SetOutput(stderr)
	seed := f.String("seed", "", "file holding the seed secret key (ARTIFACT_SIGNING_KEY)")
	prod := f.String("prod", "", "file holding the production secret key (ZPKG_SIGNING_KEY)")
	summary := f.String("summary", "", "file the Markdown report is appended to (the job summary)")
	if err := f.Parse(args); err != nil {
		return 2
	}
	if *seed == "" || *prod == "" || f.NArg() != 0 {
		fmt.Fprintln(stderr, "signingkeys: --seed and --prod are required, and the secret is read from them, never argv")
		return 2
	}

	var report strings.Builder
	code := 0
	seedKP, err := load(*seed)
	if err != nil {
		fmt.Fprintf(stderr, "signingkeys: seed key: %v\n", err)
		code = 1
	} else {
		section(&report, "Seed key (ARTIFACT_SIGNING_KEY)", "the non-production smoke and rehearsal key, never trusted by clients; pass its line as `source_trusted_keys` as `seed-b6:<line>`", seedKP)
	}
	prodKP, err := load(*prod)
	if err != nil {
		fmt.Fprintf(stderr, "signingkeys: production key: %v\n", err)
		code = 1
	} else {
		section(&report, "Production key (ZPKG_SIGNING_KEY)", "the key clients trust", prodKP)
		want := trustedKey()
		if got := prodKP.PublicKeyString(); got != want {
			fmt.Fprintln(stderr, "signingkeys: the production secret key does NOT match artifact.SigningPublicKey")
			fmt.Fprintf(&report, "**MISMATCH**: the production secret key's public line differs from `artifact.SigningPublicKey` (`%s`).\n\n", want)
			code = 1
		} else {
			report.WriteString("Matches `artifact.SigningPublicKey`.\n\n")
		}
	}

	fmt.Fprint(stdout, report.String())
	if *summary != "" {
		if err := osAppendFile(*summary, report.String()); err != nil {
			fmt.Fprintf(stderr, "signingkeys: summary: %v\n", err)
			code = 1
		}
	}
	return code
}

// section writes one key's report: the .pub text and the id, nothing else.
func section(w *strings.Builder, title, role string, kp *sign.Keypair) {
	fmt.Fprintf(w, "### %s\n\n%s.\n\n```\n%skey id: %X\n```\n\n", title, role, kp.PublicKeyFile(""), kp.ID)
}

// load reads a secret key file. Errors are fixed phrases: the decoder's error
// could quote the input.
func load(path string) (*sign.Keypair, error) {
	b, err := osReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil, errors.New("empty (is the secret set for this run?)")
	}
	kp, err := sign.LoadSecretKey(string(b))
	if err != nil {
		return nil, errors.New("not a secret key")
	}
	return kp, nil
}

// readBounded reads at most maxKeyFile bytes and refuses a larger file.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot be opened")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxKeyFile+1))
	if err != nil {
		return nil, errors.New("cannot be read")
	}
	if len(b) > maxKeyFile {
		return nil, errors.New("too large to be a secret key")
	}
	return b, nil
}

func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, s)
	return errors.Join(werr, f.Close())
}
