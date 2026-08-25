package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tests filling in coverage gaps enumerated in TODO.md. Each block is
// prefixed with the specific gap it closes.

// ---- r: read file into buffer ----------------------------------------------

func TestR_InsertAtNullDot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("INSERTED"), 0o644); err != nil {
		t.Fatal(err)
	}
	// dot at position 3 inside "before" → insert INSERTED there.
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "before")
	f.SetDot(dot(3, 3))
	s.AddFile(f)

	assertNoError(t, s.ExecString("r "+src))
	assertContents(t, f.String(), "befINSERTEDore")
	// Dot should span the newly inserted text: [3, 3+8) = [3, 11).
	assertDot(t, f.Dot(), 3, 11)
}

func TestR_ReplacesDotRange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "hello world")
	f.SetDot(dot(6, 11)) // "world"
	s.AddFile(f)

	assertNoError(t, s.ExecString("r "+src))
	assertContents(t, f.String(), "hello NEW")
	assertDot(t, f.Dot(), 6, 9)
}

// ---- k / ' : set mark and use it as address --------------------------------

func TestKAndMarkAddress(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "hello\nworld\n")
	f.SetDot(dot(0, 5)) // "hello"
	s.AddFile(f)

	// Mark the current dot.
	assertNoError(t, s.ExecString("k"))
	// Move dot elsewhere.
	f.SetDot(dot(6, 11)) // "world"
	// Print via the mark address.
	assertNoError(t, s.ExecString("'p"))
	assertOutput(t, out.String(), "hello")
}

// ---- Shell commands: <, >, | ----------------------------------------------

// Skip shell tests on Windows — sh -c isn't guaranteed, and PowerShell
// command-line escaping is fiddly enough to be its own test surface.
func skipIfWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command tests are POSIX-only")
	}
}

func TestShellIn_ReplacesDotWithOutput(t *testing.T) {
	skipIfWindows(t)
	// < cmd replaces dot with the command's stdout.
	r := runCmd(t, "before", dot(0, 6), "<echo hello")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello\n")
}

func TestShellOut_SendsDotAsStdin(t *testing.T) {
	skipIfWindows(t)
	// > cmd sends dot as stdin; buffer unchanged; output goes to session output.
	r := runCmd(t, "hello\nworld\n", dot(0, 12), ">wc -l")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello\nworld\n")
	// wc -l should print "2" somewhere in the output.
	if !strings.Contains(r.output, "2") {
		t.Errorf("expected '2' in wc output, got %q", r.output)
	}
}

func TestShellPipe_ReplacesDotWithProcessedOutput(t *testing.T) {
	skipIfWindows(t)
	// | cmd pipes dot through the shell command and replaces dot with output.
	r := runCmd(t, "hello", dot(0, 5), "|tr a-z A-Z")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "HELLO")
}

// ---- Undo restores an entire x loop as one unit ----------------------------

func TestUndoRestoresEntireXLoop(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "cat dog cat dog cat")
	f.SetDot(dot(0, 19))
	s.AddFile(f)

	assertNoError(t, s.ExecString("x/cat/ c/CAT/"))
	assertContents(t, f.String(), "CAT dog CAT dog CAT")

	// A single u reverses the whole x loop, not just the last replacement.
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "cat dog cat dog cat")
}

// ---- Structural commands inside { ... } ------------------------------------

func TestStructuralInsideBraceGroup(t *testing.T) {
	// x inside a brace group: iterate lines and print each.
	r := runCmd(t, "alpha\nbeta\ngamma\n", dot(0, 17), `{ x/^.*\n/ p }`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "alpha\nbeta\ngamma\n")
}

func TestGuardInsideBraceGroup(t *testing.T) {
	// g inside a brace group.
	r := runCmd(t, "hello world", dot(0, 11), "{ g/world/ p }")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "hello world")
}

// ---- Parser errors: unclosed brace -----------------------------------------

func TestParseErrorUnclosedBrace(t *testing.T) {
	r := runCmd(t, "hello", dot(0, 5), "{ p")
	assertError(t, r.err)
}

// Regression for the #-comment collision that started this whole
// investigation: `#0,#4 p` used to be swallowed as a comment before
// it could parse as a char-offset compound address. Dave now has no
// comment syntax (matching Sam), so this must parse straight through.
func TestParseCommands_HashDigitIsAddress(t *testing.T) {
	cmds, err := ParseCommands("#0,#4 p")
	if err != nil {
		t.Fatalf("ParseCommands: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	if cmds[0].Op != 'p' {
		t.Errorf("op = %q, want %q", cmds[0].Op, 'p')
	}
	if cmds[0].Addr == nil {
		t.Errorf("addr = nil, want #0,#4")
	}
}

// ---- Empty-pattern reuse in the cmd path -----------------------------------

func TestEmptyPatternReuse(t *testing.T) {
	// Reset the package-level last-pattern so this test doesn't inherit
	// state from any other test that ran first.
	dregs.ResetLastPattern()

	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "foo bar foo baz foo")
	f.SetDot(dot(0, 19))
	s.AddFile(f)

	// First command sets the last pattern (and moves dot to the last match).
	assertNoError(t, s.ExecString(",x/foo/ p"))
	out.Reset()
	// Second command reuses the pattern; reset dot to whole file explicitly.
	assertNoError(t, s.ExecString(",x// p"))
	assertOutput(t, out.String(), "foofoofoo")
}

// ---- Backward-search ?re? as an address ------------------------------------

func TestBackwardSearchAddress(t *testing.T) {
	// Search backward from dot for "foo"; the rightmost match before dot
	// should be found. dot at end → last "foo" in the buffer.
	r := runCmd(t, "foo bar foo baz", dot(15, 15), "?foo?p")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "foo")
}

// ---- s/re/repl\nmore/ with a literal newline in the replacement ------------

func TestSubstReplacementWithNewline(t *testing.T) {
	r := runCmd(t, "hello", dot(0, 5), `s/hello/one\ntwo/`)
	assertNoError(t, r.err)
	assertContents(t, r.contents, "one\ntwo")
}

// ---- Non-ASCII through = and #n addresses ----------------------------------

func TestEqualOnNonASCIILines(t *testing.T) {
	// "café" is 4 runes / 5 bytes. Line 1 spans runes [0, 5) including \n.
	// dot on line 1 → = should print "1,1".
	r := runCmd(t, "café\nnaïve\n", dot(0, 5), "=")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "1,1\n")
}

func TestCharAddressCountsCodepointsNotBytes(t *testing.T) {
	// #4 is codepoint 4, which is the position AFTER "café". Range #0,#4
	// selects the first four codepoints — which is "café", not the first
	// four bytes ("caf\xc3").
	r := runCmd(t, "café bar", dot(0, 0), "#0,#4 p")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "café")
}

func TestCharAddressReportOnNonASCII(t *testing.T) {
	// =# should report character offsets — codepoints, not bytes.
	// dot=[0,4) selects "café", so =# prints #0,#4 (not #0,#5).
	r := runCmd(t, "café", dot(0, 4), "=#")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "#0,#4\n")
}

// Silence unused-import warnings when a test in this file gets deleted.
var _ = buffer.Range{}
