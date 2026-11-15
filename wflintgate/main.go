// wflintgate runs actionlint over this repository's workflows and fails the
// lane on what it reports — after PROVING, against a control, that the check
// it exists for is actually running.
//
//	go run ./wflintgate            # .github/workflows, from the repository root
//	go run ./wflintgate -dir X     # another tree
//	go run ./wflintgate -no-proof  # skip the controls (for debugging only)
//
// ⛔⛔ WHY IT EXISTS: a `#` comment line written BETWEEN the continued lines of
// a shell command does not comment out one line — the backslash that would
// have continued the command is itself swallowed by the comment, so the
// continuation ENDS there, the rest of the command runs as a separate command,
// and the step dies at 127. Four steps in this repository were carrying that,
// all of them written by a rename pass adding an explanatory note:
//
//	zpkb builder \
//	  --toolchain builder/toolchain.txt \
//	  # `zpkb`/`zpkx` since the rename: …
//	  --zpkb "$(command -v zpkb)" \      ← runs as a COMMAND. 127.
//
// Measured with a positive control on 2026-11-15: the same block without the
// comment passes every word to one command; with it, `echo one` runs alone and
// `--flag: command not found` follows. The four were introduced between
// 2026-11-04 and 2026-11-13 and NOTHING failed, because `build.yml`,
// `sovereign.yml`, `wasip1-c.yml` and `windows-c.yml` are dispatch-and-schedule
// lanes: the last run predated the edits. A defect in a lane nobody triggers is
// invisible until the day it is needed.
//
// ⛔ AND IT IS NOT A LESSON, IT IS A GATE, because the same hand reintroduced
// it one minute after fixing the four — in the note that explained it.
// shellcheck calls the class SC2215 and actionlint surfaces it.
//
// ⛔⛔ THE PROOF IS THE POINT. actionlint runs shellcheck as a SUBPROCESS and
// skips that whole family in silence when shellcheck is not on PATH, so an
// `actionlint` step on an image without it is a green light that checked
// nothing. This asserts shellcheck is present, then feeds actionlint a control
// file carrying the defect and requires SC2215 back; and a second control
// WITHOUT the defect, which must come back clean — a judge that flags
// everything proves nothing either.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// theClass is the finding this gate exists for. It can never be ignored.
const theClass = "SC2215"

// ignored are findings this repository currently accepts, each with the reason
// it is not a defect. It is PRINTED on every run: a reader has to be able to
// see what was forgiven, not only what was found.
//
// ⛔ `theClass` is refused from this list by a test. An ignore list is the part
// of a gate most likely to rot into a rubber stamp, and the one entry that
// would make this gate pointless is the one somebody will reach for.
var ignored = []struct{ code, why string }{
	{"SC2016", "a single-quoted inner script is passed to another shell on purpose: it must NOT expand here"},
	{"SC2094", "the same file is read and written in one pipeline deliberately, with the read completed first"},
	{"SC2012", "`ls` over a directory this repository wrote itself, with no non-alphanumeric names in it"},
	{"SC2010", "`ls | grep` over the same"},
	{"SC2015", "`A && B || C` where C is the intended fallback for both"},
	{"SC2046", "word splitting is what the expansion is for"},
	{"SC2295", "an unquoted expansion inside ${..} whose value is a literal path prefix"},
}

// ⛔ `runner-label` is deliberately NOT on that list. The 15 findings over
// `[self-hosted, incus, amd64]` were silenced by NAMING the labels, in
// .github/actionlint.yaml, which is a statement about this repository rather
// than a hole in the judge. Measured: 15 before the file, 0 after.

// runCmd is the seam a test drives. It yields actionlint's EXIT CODE rather
// than an error, because the codes are the only thing that tells its outcomes
// apart: 0 nothing found, 1 findings FOUND, 2 a bad command line, 3 a path it
// could not read. -1 means the command never started at all.
//
// ⛔⛔ THE FIRST VERSION RETURNED AN `error` AND REFUSED whenever it was
// non-nil with no surviving finding — meaning to catch "actionlint could not
// read the tree". But exit 1 is also what "every finding was forgiven" looks
// like, so the gate refused a CLEAN repository: 17 files linted, 0 findings
// left, exit 2. An error says only "not zero"; a code says which.
var runCmd = func(name string, args ...string) (string, int) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	return string(out), -1
}

// The rest of the seams. mkdirTemp and writeFile exist so that the failure
// arms of `prove` can be ENTERED by a test: this repository's coverage gate is
// at 100 %, and an unreachable error path is one nobody has read.
var (
	lookPath  = exec.LookPath
	osExit    = os.Exit
	mkdirTemp = os.MkdirTemp
	writeFile = os.WriteFile
)

func main() { osExit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("wflintgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", filepath.Join(".github", "workflows"), "the workflow directory to lint")
	noProof := fs.Bool("no-proof", false, "skip the controls (debugging only)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	if _, err := lookPath("shellcheck"); err != nil {
		fmt.Fprintln(stderr, "wflintgate: shellcheck is not on PATH — actionlint would skip every shellcheck rule IN SILENCE, including "+theClass)
		return 2
	}
	if _, err := lookPath("actionlint"); err != nil {
		fmt.Fprintln(stderr, "wflintgate: actionlint is not on PATH:", err)
		return 2
	}

	if !*noProof {
		if code := prove(stdout, stderr); code != 0 {
			return code
		}
	}

	fmt.Fprintln(stdout, "forgiven on purpose (each with its reason):")
	for _, ig := range ignored {
		fmt.Fprintf(stdout, "  %-12s %s\n", ig.code, ig.why)
	}

	files, err := filesIn(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "wflintgate:", err)
		return 2
	}

	out, code := runCmd("actionlint", files...)
	kept := keep(out)
	// ⛔⛔ THE COUNT OF FILES IS PART OF THE VERDICT. The first version of this
	// passed `.github/workflows` — a DIRECTORY — to actionlint, which answers
	// `could not read ".github/workflows": is a directory` and exits 3. The
	// gate printed "0 finding(s)" and exited 0 over a tree it had never read,
	// and the synthetic control above still passed, because that control is a
	// single FILE. Caught by replaying the gate against the four workflows as
	// they were BEFORE the fix: 0 findings where there had to be 4.
	fmt.Fprintf(stdout, "\n%s: %d file(s) linted, %d finding(s) after the list above\n", *dir, len(files), len(kept))
	// ⛔ 0 and 1 are VERDICTS; anything else is actionlint declining to judge,
	// and must never read as a pass. That is the half the first version got
	// backwards in both directions at once: it refused on exit 1 (findings,
	// all forgiven — a legitimate pass) and would have accepted exit 3 (a path
	// it could not read) as long as nothing was printed.
	if code != 0 && code != 1 {
		fmt.Fprintf(stderr, "wflintgate: actionlint exited %d — it did not judge this tree:\n%s\n", code, out)
		return 2
	}
	if len(kept) == 0 {
		return 0
	}
	for _, l := range kept {
		fmt.Fprintln(stderr, "  "+l)
	}
	return 1
}

// filesIn names the workflow files to lint. actionlint takes FILES, not a
// directory, and an empty list is a refusal rather than a pass.
func filesIn(dir string) ([]string, error) {
	var files []string
	for _, pat := range []string{"*.yml", "*.yaml"} {
		m, err := filepath.Glob(filepath.Join(dir, pat))
		if err != nil {
			return nil, err
		}
		files = append(files, m...)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no workflow file under %s — a gate with nothing to read is not a pass", dir)
	}
	sort.Strings(files)
	return files, nil
}

// prove feeds actionlint a workflow carrying the defect and one without it.
// The first must come back flagged and the second clean, or this gate is not
// measuring anything.
func prove(stdout, stderr io.Writer) int {
	tmp, err := mkdirTemp("", "wflintgate")
	if err != nil {
		fmt.Fprintln(stderr, "wflintgate:", err)
		return 2
	}
	defer os.RemoveAll(tmp)

	for _, c := range []struct {
		name    string
		body    string
		flagged bool
	}{
		{"positive.yml", controlWorkflow(true), true},
		{"negative.yml", controlWorkflow(false), false},
	} {
		p := filepath.Join(tmp, c.name)
		if err := writeFile(p, []byte(c.body), 0o644); err != nil {
			fmt.Fprintln(stderr, "wflintgate:", err)
			return 2
		}
		out, code := runCmd("actionlint", p)
		if code != 0 && code != 1 {
			fmt.Fprintf(stderr, "wflintgate: actionlint exited %d on the control %s — it did not read it:\n%s\n", code, c.name, out)
			return 2
		}
		got := strings.Contains(out, theClass)
		if got != c.flagged {
			fmt.Fprintf(stderr, "wflintgate: the control %s should %shave been flagged %s, and was not — this gate is not measuring anything.\nactionlint said:\n%s\n",
				c.name, map[bool]string{true: "", false: "NOT "}[c.flagged], theClass, out)
			return 2
		}
		fmt.Fprintf(stdout, "control %-12s %s as expected\n", c.name,
			map[bool]string{true: theClass, false: "clean"}[c.flagged])
	}
	return 0
}

// controlWorkflow is one step whose comment sits inside a continued command
// (defect) or above it (correct). Nothing else differs between the two.
func controlWorkflow(defect bool) string {
	const head = `name: control
on: { push: { branches: [main] } }
jobs:
  c:
    runs-on: ubuntu-latest
    steps:
      - name: a continued command with a note
        run: |
`
	if defect {
		return head + `          echo one \
            --flag two \
            # the note, written between the continued lines
            --other three
`
	}
	return head + `          # the note, written above the command
          echo one \
            --flag two \
            --other three
`
}

// findingLine is `path:line:col: message`, which is how actionlint opens every
// finding.
//
// ⛔ NOT "a line that does not start with a space". actionlint prints the
// offending source under each finding as a `  |` rule, a `^~~~` marker and a
// NUMBERED line — and the number sits at column 0 (`481 |         run: |`), so
// an indentation test counted that excerpt as a finding of its own. Caught by
// the test, not by reading.
var findingLine = regexp.MustCompile(`^[^\s:]+:\d+:\d+: `)

// keep drops the lines of actionlint's report that belong to a forgiven code,
// along with the source excerpt actionlint prints under each finding.
func keep(out string) []string {
	var kept []string
	for _, l := range strings.Split(out, "\n") {
		if !findingLine.MatchString(l) {
			continue
		}
		forgiven := false
		for _, ig := range ignored {
			if strings.Contains(l, ig.code) {
				forgiven = true
				break
			}
		}
		if forgiven {
			continue
		}
		kept = append(kept, l)
	}
	return kept
}
