package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dave/file"
)

// sdave's three seams — processFile, processReader and process — all take an
// io.Writer instead of reaching for os.Stdout, so the whole pipeline (load,
// run the script, dump the buffer) can be run in-process. What is *not*
// covered here is main() itself: flag parsing, the "first positional argument
// is the script" rule and the os.Exit paths still have no test.

// run executes script against content in a fresh file, returning what was
// written, what the buffer ended up holding, and any error.
func run(t *testing.T, script, content string, suppress bool) (out, buf string, err error) {
	t.Helper()
	var w strings.Builder
	f := file.New()
	f.Buffer().Insert(0, content)
	err = process(&w, f, script, suppress)
	return w.String(), f.String(), err
}

// write creates a file under a temp dir and returns its path.
func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

// ---- process ----------------------------------------------------------------

// The initial dot is the whole file, which is what lets an unaddressed
// s/re/repl/ work at all — the ssam behaviour the comment on process promises.
func TestProcessSetsDotToWholeFile(t *testing.T) {
	// "=" reports dot as a line range; over a 3-line file that is 1,3.
	out, _, err := run(t, "=", "a\nb\nc\n", true)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if out != "1,3\n" {
		t.Errorf("dot at start = %q, want %q (the whole file)", out, "1,3\n")
	}
}

func TestProcessRunsScriptAndWritesResult(t *testing.T) {
	cases := []struct {
		script, content, want string
		what                  string
	}{
		{"s/quick/slow/", "the quick fox\n", "the slow fox\n", "unaddressed substitution"},
		{"s/o/0/g", "foo boo\n", "f00 b00\n", "global substitution"},
		{",d", "abc\n", "", "deleting everything leaves an empty buffer"},
		{"", "abc\n", "abc\n", "an empty script is a copy"},
		{"s/b/B/", "abc", "aBc", "a file with no trailing newline keeps having none"},
	}
	for _, c := range cases {
		out, buf, err := run(t, c.script, c.content, false)
		if err != nil {
			t.Errorf("%s: process: %v", c.what, err)
			continue
		}
		if out != c.want {
			t.Errorf("%s: wrote %q, want %q", c.what, out, c.want)
		}
		if buf != c.want {
			t.Errorf("%s: buffer holds %q, want %q", c.what, buf, c.want)
		}
	}
}

// -n suppresses the buffer dump only. The script still runs, and anything it
// prints itself still reaches the writer — that is the whole point of sdave -n.
func TestProcessSuppressSkipsOnlyTheBufferDump(t *testing.T) {
	out, buf, err := run(t, "s/quick/slow/", "the quick fox\n", true)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if out != "" {
		t.Errorf("suppressed run wrote %q, want nothing", out)
	}
	if want := "the slow fox\n"; buf != want {
		t.Errorf("buffer = %q, want %q — the script must still run under -n", buf, want)
	}

	out, _, err = run(t, "x/o/p", "foo boo\n", true)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "o\no\no\no\n"; out != want {
		t.Errorf("p under -n wrote %q, want %q", out, want)
	}
}

// Both the script's own output and the buffer dump go to the same writer, so
// their order is observable: everything the script prints comes first.
func TestProcessCommandOutputPrecedesBufferDump(t *testing.T) {
	out, _, err := run(t, "x/o/p", "foo boo\n", false)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "o\no\no\no\nfoo boo\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// Positions are codepoint offsets everywhere in dave; a multibyte file must
// not shift them.
func TestProcessIsCodepointOriented(t *testing.T) {
	out, _, err := run(t, "s/ö/o/", "héllo wörld\n", false)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if want := "héllo world\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// A failing script is reported, and nothing is written — no half-edited buffer
// gets dumped on the way out.
func TestProcessReturnsScriptErrors(t *testing.T) {
	for _, c := range []struct{ script, wantErr, what string }{
		{"s/[/x/", "missing closing ]", "malformed regexp"},
		{"Z", `unknown command "Z"`, "unknown command"},
		{"s/zzz/x/", "no match", "a substitution that matches nothing"},
	} {
		out, buf, err := run(t, c.script, "abc\n", false)
		if err == nil {
			t.Errorf("%s: process succeeded, want an error", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", c.what, err, c.wantErr)
		}
		if out != "" {
			t.Errorf("%s: wrote %q on failure, want nothing", c.what, out)
		}
		if buf != "abc\n" {
			t.Errorf("%s: buffer = %q, want it untouched", c.what, buf)
		}
	}
}

// ---- processReader ----------------------------------------------------------

func TestProcessReader(t *testing.T) {
	for _, c := range []struct{ in, script, want, what string }{
		{"x\ny\n", "s/x/X/", "X\ny\n", "stdin round trip"},
		{"", "", "", "empty input"},
		{"one", "s/one/1/", "1", "no trailing newline"},
		{"x\r\ny\r\n", "s/x/X/", "X\r\ny\r\n", "CRLF on stdin round trips"},
	} {
		var w strings.Builder
		if err := processReader(&w, strings.NewReader(c.in), c.script, false); err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if w.String() != c.want {
			t.Errorf("%s: got %q, want %q", c.what, w.String(), c.want)
		}
	}
}

// ---- processFile ------------------------------------------------------------

func TestProcessFileReadsFromDiskAndWritesToTheWriter(t *testing.T) {
	p := write(t, "in.txt", "the quick fox\n")
	var w strings.Builder
	if err := processFile(&w, p, "s/quick/slow/", false); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if want := "the slow fox\n"; w.String() != want {
		t.Errorf("wrote %q, want %q", w.String(), want)
	}
	// sdave is a stream editor: the input file is never rewritten.
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("re-reading input: %v", err)
	}
	if want := "the quick fox\n"; string(after) != want {
		t.Errorf("input file on disk = %q, want %q — sdave must not edit in place", after, want)
	}
}

// Several files run through one writer append to it, one after another, each
// in its own session.
func TestProcessFileAppendsPerFile(t *testing.T) {
	dir := t.TempDir()
	var w strings.Builder
	for _, f := range []struct{ name, content string }{
		{"1.txt", "one\n"},
		{"2.txt", "two\n"},
	} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
		if err := processFile(&w, p, "s/^/> /", false); err != nil {
			t.Fatalf("processFile %s: %v", f.name, err)
		}
	}
	if want := "> one\n> two\n"; w.String() != want {
		t.Errorf("got %q, want %q", w.String(), want)
	}
}

// EnsureLoaded treats a missing file as a new, empty one rather than an error,
// so sdave prints nothing for it instead of failing.
func TestProcessFileMissingFileIsEmptyNotAnError(t *testing.T) {
	var w strings.Builder
	missing := filepath.Join(t.TempDir(), "nope.txt")
	if err := processFile(&w, missing, "", false); err != nil {
		t.Errorf("processFile on a missing file: %v, want no error", err)
	}
	if w.String() != "" {
		t.Errorf("wrote %q for a missing file, want nothing", w.String())
	}
}

func TestProcessFileReportsUnreadableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 is still readable")
	}
	p := write(t, "bad.txt", "x")
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	var w strings.Builder
	err := processFile(&w, p, "", false)
	if err == nil {
		t.Fatal("processFile on an unreadable file succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %v, want it to mention permission denied", err)
	}
}

// A stream editor preserves the line endings it was given: a CRLF file in is a
// CRLF file out. Inside the buffer the content is always LF — DecodeContent
// strips the \r on the way in — so this only works if the write path encodes
// it back, which is what file.WriteContent does for sdave.
func TestProcessFilePreservesCRLFLineEndings(t *testing.T) {
	p := write(t, "crlf.txt", "a\r\nb\r\n")
	var w strings.Builder
	if err := processFile(&w, p, "s/a/A/", false); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if got, want := w.String(), "A\r\nb\r\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An LF file must not acquire \r on the way out.
func TestProcessFileLeavesLFAlone(t *testing.T) {
	p := write(t, "lf.txt", "a\nb\n")
	var w strings.Builder
	if err := processFile(&w, p, "s/a/A/", false); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	if got, want := w.String(), "A\nb\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
