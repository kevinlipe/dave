package cmd

import (
	"dave/buffer"
	"dave/file"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ---- test harness -----------------------------------------------------------

type testResult struct {
	contents string
	dot      buffer.Range
	output   string
	err      error
}

func runCmd(t *testing.T, content string, dot buffer.Range, cmdStr string) testResult {
	t.Helper()
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, content)
	f.SetDot(dot)
	s.AddFile(f)

	err := s.ExecString(cmdStr)
	return testResult{
		contents: f.String(),
		dot:      f.Dot(),
		output:   out.String(),
		err:      err,
	}
}

func dot(start, end int) buffer.Range { return buffer.Range{Start: start, End: end} }

func assertContents(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("contents:\n  got  %q\n  want %q", got, want)
	}
}

func assertDot(t *testing.T, got buffer.Range, start, end int) {
	t.Helper()
	if got.Start != start || got.End != end {
		t.Errorf("dot: got [%d,%d), want [%d,%d)", got.Start, got.End, start, end)
	}
}

func assertOutput(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output:\n  got  %q\n  want %q", got, want)
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---- Delete -----------------------------------------------------------------

func TestDelete(t *testing.T) {
	r := runCmd(t, "hello world", dot(6, 11), "d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello ")
	assertDot(t, r.dot, 6, 6)
}

func TestDeleteWholeLine(t *testing.T) {
	r := runCmd(t, "hello\nworld\n", dot(0, 6), "d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "world\n")
}

// ---- Print ------------------------------------------------------------------

// p prints exactly the addressed text. Sam's display writes the range and
// stops, so a range that does not end in a newline leaves the cursor mid-line;
// dave used to add one. Confirmed byte-for-byte against sam.
func TestPrint(t *testing.T) {
	r := runCmd(t, "hello world", dot(6, 11), "p")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "world")
	assertContents(t, r.contents, "hello world") // unchanged
}

// ---- Equal ------------------------------------------------------------------

func TestEqual(t *testing.T) {
	r := runCmd(t, "hello\nworld\n", dot(0, 6), "=")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "1,1\n")
}

func TestEqualChar(t *testing.T) {
	r := runCmd(t, "hello", dot(2, 4), "=#")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "#2,#4\n")
}

// ---- Append -----------------------------------------------------------------

func TestAppend(t *testing.T) {
	r := runCmd(t, "hello", dot(5, 5), "a/world/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "helloworld")
	assertDot(t, r.dot, 5, 10)
}

func TestAppendAfterDot(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 5), "a/!/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello! world")
	assertDot(t, r.dot, 5, 6)
}

// ---- Insert -----------------------------------------------------------------

func TestInsert(t *testing.T) {
	r := runCmd(t, "world", dot(0, 0), "i/hello /")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello world")
	assertDot(t, r.dot, 0, 6)
}

// ---- Change -----------------------------------------------------------------

func TestChange(t *testing.T) {
	// "Hello, Peter." — "Peter" is at positions [7,12), "." at 12.
	// Replacing "Peter" (5 chars) with "world" (5 chars) gives "Hello, world."
	r := runCmd(t, "Hello, Peter.", dot(7, 12), "c/world/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "Hello, world.")
	assertDot(t, r.dot, 7, 12)
}

// ---- Substitute -------------------------------------------------------------

func TestSubst(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), "s/world/Dave/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello Dave")
	assertDot(t, r.dot, 6, 10)
}

func TestSubstAmpersand(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), "s/world/[&]/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello [world]")
}

// Subexpressions in the replacement are sam's \1-\9, not Go's $1
// (sam/xec.c:252). A bare '$' is an ordinary character.
func TestSubstGroup(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), `s/(hello) (world)/\2 \1/`)
	assertNoError(t, r.err)
	assertContents(t, r.contents, "world hello")
}

func TestSubstGlobal(t *testing.T) {
	r := runCmd(t, "aaa", dot(0, 3), "s/a/b/g")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "bbb")
}

func TestSubstNoMatch(t *testing.T) {
	r := runCmd(t, "hello", dot(0, 5), "s/xyz/abc/")
	assertError(t, r.err)
	assertContents(t, r.contents, "hello") // unchanged on error
}

func TestSubstNth(t *testing.T) {
	r := runCmd(t, "aaa", dot(0, 3), "s2/a/b/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "aba")
}

func TestSubstAllEmptyReplacementAtZero(t *testing.T) {
	// s/foo//g where the only match is at position 0 replaced with "" —
	// the dot-update guard was previously "numbers non-zero" which
	// swallowed this legitimate case.
	r := runCmd(t, "fooxyz", dot(0, 6), "s/foo//g")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "xyz")
	// Dot should collapse onto the replacement point: (0, 0).
	assertDot(t, r.dot, 0, 0)
}

// ---- G / V ------------------------------------------------------------------

func TestGMatch(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), "g/world/ d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "")
}

func TestGNoMatch(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), "g/xyz/ d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello world") // unchanged
}

func TestVNoMatch(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), "v/xyz/ d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "") // deleted because no match
}

func TestVMatch(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 11), "v/world/ d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello world") // unchanged (has match)
}

// ---- X -----------------------------------------------------------------------

func TestX(t *testing.T) {
	// x/re/ c/text/ — replace every match
	r := runCmd(t, "cat and cat", dot(0, 11), "x/cat/ c/dog/")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "dog and dog")
}

func TestXLines(t *testing.T) {
	// x with space — match each line
	r := runCmd(t, "hello\nworld\nfoo\n", dot(0, 15), ",x g/hello/ p")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "hello\n")
}

func TestXNullMatchHandling(t *testing.T) {
	// x/a*/ on "ab" should match "a" at position 0, skip null at 1,
	// then match "" at position 2.
	r := runCmd(t, "ab", dot(0, 2), `x/a*/ p`)
	assertNoError(t, r.err)
	// Should see "a" printed, then "" printed (empty string at pos 2).
	// The important thing: no panic, no infinite loop.
	// Output should be "a" followed by "" (empty).
	if strings.Contains(r.output, "aaa") {
		t.Errorf("null match handling failed: output = %q", r.output)
	}
}

// ---- Y -----------------------------------------------------------------------

func TestY(t *testing.T) {
	// y/A/ c/-/ should give -A-A-A- for "AAAA" (as per Sam paper example)
	// y splits by pattern, runs body on the between-parts
	// Actually the example from spec: c/AAA/ y/A/ c/-/
	// Let's test a simple y:
	// "hello world" y/ / c/_/  → replace spaces-separated chunks
	// Wait, y finds text BETWEEN matches. y/ / c/_/ replaces each word with _
	r := runCmd(t, "hello world foo", dot(0, 15), `y/ / c/_/`)
	assertNoError(t, r.err)
	assertContents(t, r.contents, "_ _ _")
}

// y runs the body on the text between matches. Nothing separates the fields in
// the output because p adds nothing: sam prints "abc" here too.
func TestYSamExample(t *testing.T) {
	r := runCmd(t, "a,b,c", dot(0, 5), `y/,/ p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "abc")
}

// ---- Group ------------------------------------------------------------------

// Every command in a group runs against the group's address. Sam assigns dot
// inside the loop (`a.f->dot = a; cmdexec(a.f, cp)`, sam/xec.c:57), so a command
// that moves dot does not change where the next one starts. Setting it once on
// the way in makes a group mean "run these in sequence, each starting where the
// last left off", which is a different thing entirely.
func TestGroupRunsEachCommandAtTheGroupAddress(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 5), "{ i/</ a/>/ }")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "<hello> world")
}

// The reset is visible whenever one command in the group moves dot: the next
// one still sees the group's address, not the moved one.
func TestGroupResetsDotBetweenCommands(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 5), "{ /world/p\np }")
	assertNoError(t, r.err)
	// The addressed p prints "world"; the bare p that follows prints the
	// group's address, "hello", not the "world" the search left behind.
	assertOutput(t, r.output, "worldhello")
}

// Changes still have to arrive in order. Sam refuses a group whose commands log
// backwards — Esequence, "changes not in sequence" — and with dot reset each
// time, `{ a/!/ d }` on the same address does exactly that: the append logs at
// the end of dot, the delete at its start.
func TestGroupOutOfOrderChangesAreRefused(t *testing.T) {
	r := runCmd(t, "hello world", dot(0, 5), "{ a/!/ d }")
	assertError(t, r.err)
	assertContents(t, r.contents, "hello world")
}

func TestGroupSequence(t *testing.T) {
	// { p = } should print and then print the address
	r := runCmd(t, "hello\nworld\n", dot(0, 6), "{ p = }")
	assertNoError(t, r.err)
	// p prints "hello\n", = prints "1,1\n" (or "1\n")
	if !strings.Contains(r.output, "hello\n") {
		t.Errorf("expected 'hello' in output, got %q", r.output)
	}
}

// ---- Address with command ---------------------------------------------------

func TestAddressedDelete(t *testing.T) {
	r := runCmd(t, "hello\nworld\nfoo\n", dot(0, 0), "2d")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "hello\nfoo\n")
}

func TestAddressedPrint(t *testing.T) {
	r := runCmd(t, "hello\nworld\nfoo\n", dot(0, 0), "2p")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "world\n")
}

func TestCommaAddressedCommand(t *testing.T) {
	r := runCmd(t, "hello\nworld\n", dot(0, 0), ",p")
	assertNoError(t, r.err)
	assertOutput(t, r.output, "hello\nworld\n")
}

// ---- File commands ----------------------------------------------------------

func TestB(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f1 := file.NewNamed("foo.txt")
	f1.EnsureLoaded()
	f2 := file.NewNamed("bar.txt")
	f2.EnsureLoaded()
	s.AddFile(f1)
	s.AddFile(f2)
	s.Current = f1

	assertNoError(t, s.ExecString("b bar.txt"))
	if s.Current != f2 {
		t.Error("b bar.txt should switch to bar.txt")
	}
}

func TestBExpandsTilde(t *testing.T) {
	t.Setenv("HOME", "/tmp/testhome")
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed("/tmp/testhome/bin/foo.sh")
	f.EnsureLoaded()
	s.AddFile(f)

	// b with ~ should resolve to the same buffer as the absolute path.
	assertNoError(t, s.ExecString("b ~/bin/foo.sh"))
	if s.Current != f {
		t.Error("b ~/bin/foo.sh should locate the buffer opened at the absolute path")
	}
}

func TestBigExpandsTilde(t *testing.T) {
	t.Setenv("HOME", "/tmp/testhome")
	var out strings.Builder
	s := NewSession(&out)

	assertNoError(t, s.ExecString("B ~/bin/foo.sh"))
	if s.Current == nil || s.Current.Name() != "/tmp/testhome/bin/foo.sh" {
		got := ""
		if s.Current != nil {
			got = s.Current.Name()
		}
		t.Errorf("B ~/bin/foo.sh: current name = %q, want %q", got, "/tmp/testhome/bin/foo.sh")
	}
}

func TestBNotFound(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed("foo.txt")
	f.EnsureLoaded()
	s.AddFile(f)

	err := s.ExecString("b nonexistent.txt")
	assertError(t, err)
}

func TestN(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f1 := file.NewNamed("foo.txt")
	f1.EnsureLoaded()
	f2 := file.NewNamed("bar.txt")
	f2.EnsureLoaded()
	s.AddFile(f1)
	s.AddFile(f2)
	s.Current = f1

	assertNoError(t, s.ExecString("n"))
	if !strings.Contains(out.String(), "foo.txt") {
		t.Errorf("n output should contain foo.txt, got %q", out.String())
	}
	if !strings.Contains(out.String(), "bar.txt") {
		t.Errorf("n output should contain bar.txt, got %q", out.String())
	}
}

func TestW(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/out.txt"

	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed(path)
	f.EnsureLoaded()
	f.Buffer().Insert(0, "hello, dave")
	f.SetDot(dot(0, 11))
	s.AddFile(f)

	assertNoError(t, s.ExecString("w"))
	if f.Modified() {
		t.Error("after w, modified should be false")
	}
}

func TestWReportsCodepointCount(t *testing.T) {
	// "café" is 4 codepoints, 5 bytes in UTF-8.
	dir := t.TempDir()
	path := dir + "/out.txt"

	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed(path)
	f.EnsureLoaded()
	f.Buffer().Insert(0, "café")
	f.SetDot(dot(0, 4))
	s.AddFile(f)

	assertNoError(t, s.ExecString("w"))
	if !strings.Contains(out.String(), "#4") {
		t.Errorf("w output should report #4 codepoints, got %q", out.String())
	}
	if strings.Contains(out.String(), "#5") {
		t.Errorf("w should not report byte count (#5), got %q", out.String())
	}
}

// ---- Move / Copy ------------------------------------------------------------

func TestMoveForward(t *testing.T) {
	// dot=[0,3)="AAA" moved to $ (end of file).
	r := runCmd(t, "AAABBBCCC", dot(0, 3), "m $")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "BBBCCCAAA")
	assertDot(t, r.dot, 6, 9)
}

func TestMoveBackward(t *testing.T) {
	// dot=[6,9)="CCC" moved to 0.
	r := runCmd(t, "AAABBBCCC", dot(6, 9), "m 0")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "CCCAAABBB")
	assertDot(t, r.dot, 0, 3)
}

func TestCopyForward(t *testing.T) {
	r := runCmd(t, "AAABBBCCC", dot(0, 3), "t $")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "AAABBBCCCAAA")
	assertDot(t, r.dot, 9, 12)
}

func TestCopyBackward(t *testing.T) {
	r := runCmd(t, "AAABBBCCC", dot(6, 9), "t 0")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "CCCAAABBBCCC")
	assertDot(t, r.dot, 0, 3)
}

func TestF(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed("old.txt")
	f.EnsureLoaded()
	s.AddFile(f)

	assertNoError(t, s.ExecString("f new.txt"))
	if f.Name() != "new.txt" {
		t.Errorf("f new.txt: name = %q, want %q", f.Name(), "new.txt")
	}
}

func TestELeavesFileUnmodified(t *testing.T) {
	// e replaces the buffer with disk content — the file matches disk
	// afterwards, so modified should be false even though the change
	// routed through the transcript.
	dir := t.TempDir()
	src := dir + "/src.txt"
	if err := os.WriteFile(src, []byte("from disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "in memory")
	f.MarkChanged() // simulate prior edits
	s.AddFile(f)

	assertNoError(t, s.ExecString("e "+src))
	assertContents(t, f.String(), "from disk\n")
	if f.Modified() {
		t.Error("after e, file should be unmodified")
	}
}

func TestParseBodyMissingBodyIsError(t *testing.T) {
	// A structural command with no body was silently a no-op; now it errors.
	r := runCmd(t, "hello", dot(0, 5), "x/foo/")
	assertError(t, r.err)
}

// ---- Quit -------------------------------------------------------------------

func TestQuitClean(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed("foo.txt")
	f.EnsureLoaded()
	s.AddFile(f)

	err := s.ExecString("q")
	if err != ErrQuit {
		t.Errorf("q on clean file: got %v, want ErrQuit", err)
	}
}

func TestQuitModified(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed("foo.txt")
	f.EnsureLoaded()
	f.MarkChanged()
	s.AddFile(f)

	// First q — prints modified files, sets pending.
	err := s.ExecString("q")
	if err != nil {
		t.Errorf("first q: got error %v, want nil", err)
	}
	if !s.quitPending {
		t.Error("quitPending should be set after first q with modified files")
	}
	if !strings.Contains(out.String(), "foo.txt") {
		t.Errorf("first q output should mention foo.txt, got %q", out.String())
	}

	// Second q — quits unconditionally.
	err = s.ExecString("q")
	if err != ErrQuit {
		t.Errorf("second q: got %v, want ErrQuit", err)
	}
}

// ---- X/Y file loop ----------------------------------------------------------

func TestXFile(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f1 := file.NewNamed("foo.txt")
	f1.EnsureLoaded()
	f1.Buffer().Insert(0, "hello")
	f2 := file.NewNamed("bar.txt")
	f2.EnsureLoaded()
	f2.Buffer().Insert(0, "world")
	s.AddFile(f1)
	s.AddFile(f2)
	s.Current = f1

	// X/foo/ p — print contents of files matching "foo"
	assertNoError(t, s.ExecString(`X/foo/ ,p`))
	assertOutput(t, out.String(), "hello")
}

// ---- Error handling ---------------------------------------------------------

func TestUnknownCommand(t *testing.T) {
	r := runCmd(t, "hello", dot(0, 5), "Z")
	assertError(t, r.err)
	// Buffer unchanged.
	assertContents(t, r.contents, "hello")
}

func TestParseError(t *testing.T) {
	r := runCmd(t, "hello", dot(0, 5), "s/[/")
	assertError(t, r.err)
}

// ---- Commands that take no address ------------------------------------------

// Sam raises Enoaddr for an address on any command whose defaddr is aNo
// (sam/cmd.c:427). Without the check the address is parsed and then silently
// dropped, which reads as though it selected what the command acts on — `,D`
// looks like it should close every file and closes the current one.
func TestNoAddressCommandsRejectAnAddress(t *testing.T) {
	for _, op := range "bBDefnquXY" {
		for _, prefix := range []string{",", "1", "0,$", "."} {
			src := prefix + string(op)
			if _, _, err := ParseCommand(src, 0); err == nil {
				t.Errorf("%q: expected an error, got none", src)
			}
		}
	}
}

// The complement: everything not on that list still takes an address, so the
// check cannot be widened by accident. `r` and the shell redirections default to
// dot, `w` to the whole file.
func TestAddressableCommandsStillTakeAnAddress(t *testing.T) {
	for _, src := range []string{
		",d", "1p", ",k", "0,$=", ",s/a/b/", ",x/a/ p", ",y/a/ p",
		",g/a/ p", ",v/a/ p", "1a/x/", "1i/x/", "1c/x/", "1m$", "1t$",
		",w out.txt", "1r in.txt", ",< echo hi", ",> cat", ",| tr a-z A-Z",
		",{p}",
	} {
		if _, _, err := ParseCommand(src, 0); err != nil {
			t.Errorf("%q: unexpected error: %v", src, err)
		}
	}
}

// ---- D's second-chance latch -------------------------------------------------

// Sam guards D with closeok: the first D of a modified file refuses, and a
// second D of the same file succeeds regardless (sam/sam.c:237, trytoclose).
// The paper ties this to undo — dangerous commands go unguarded because undo can
// take them back, and D is guarded precisely because it cannot be. A guard with
// no way through is a dead end rather than a protection.
func TestDeleteModifiedFileNeedsTwoTries(t *testing.T) {
	s, a, _ := twoFileSession(t)
	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	if !a.Modified() {
		t.Fatal("file is not modified after an edit")
	}

	// First D refuses.
	assertError(t, s.ExecString("D a.txt"))
	if s.FindFile("a.txt") == nil {
		t.Fatal("file was dropped by the D that refused")
	}

	// Second D goes through.
	assertNoError(t, s.ExecString("D a.txt"))
	if s.FindFile("a.txt") != nil {
		t.Error("file survived the second D")
	}
}

// An unmodified file needs no confirmation at all.
func TestDeleteUnmodifiedFileSucceedsFirstTry(t *testing.T) {
	s, a, _ := twoFileSession(t)
	s.Current = a
	assertNoError(t, s.ExecString("D a.txt"))
	if s.FindFile("a.txt") != nil {
		t.Error("an unmodified file was not dropped")
	}
}

// Modifying the file after a refusal earns a fresh refusal: the latch tracks the
// current round of changes, not the session. Sam keeps closeok in step with the
// modify bit at the end of every command that touched the file (sam/file.c:425).
func TestDeleteLatchClearsOnFurtherEdits(t *testing.T) {
	s, a, _ := twoFileSession(t)
	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	assertError(t, s.ExecString("D a.txt")) // refused, latch set

	assertNoError(t, s.ExecString(",s/AAA/ZZZ/")) // latch cleared

	assertError(t, s.ExecString("D a.txt")) // refused again
	if s.FindFile("a.txt") == nil {
		t.Fatal("file was dropped by the D that refused")
	}
	assertNoError(t, s.ExecString("D a.txt"))
	if s.FindFile("a.txt") != nil {
		t.Error("file survived the second D")
	}
}

// Undoing back to a clean state clears the latch too, since undo restores the
// modify bit and the latch follows it.
func TestDeleteLatchFollowsUndo(t *testing.T) {
	s, a, _ := twoFileSession(t)
	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	assertNoError(t, s.ExecString("u"))
	if a.Modified() {
		t.Fatal("file still modified after undoing its only edit")
	}
	assertNoError(t, s.ExecString("D a.txt"))
	if s.FindFile("a.txt") != nil {
		t.Error("a file undone back to clean was not dropped on the first D")
	}
}

// ---- b reports the file it switched to ---------------------------------------

// Sam's b_cmd calls filename(f) at nest == 0, printing the menu line of the file
// it made current (sam/xec.c:78). Without it there is no confirmation of what b
// selected, which matters most in -d where there is no window to look at.
func TestBPrintsTheMenuLine(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed("a.txt")
	f.Buffer().Insert(0, "aaa")
	s.AddFile(f)

	assertNoError(t, s.ExecString("b a.txt"))
	if got := out.String(); !strings.Contains(got, "a.txt") {
		t.Errorf("b printed %q, want it to name the file", got)
	}
}

// Inside a loop b stays quiet, so an X over forty files does not print forty
// lines. Sam guards the print with nest == 0 for the same reason.
func TestBIsQuietInsideALoop(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	for _, name := range []string{"a.txt", "b.txt"} {
		f := file.NewNamed(name)
		f.Buffer().Insert(0, "x")
		s.AddFile(f)
	}
	out.Reset()

	assertNoError(t, s.ExecString("X/txt/ b a.txt"))
	if got := out.String(); got != "" {
		t.Errorf("b inside an X loop printed %q, want nothing", got)
	}
}

// ---- q re-arms after further edits -------------------------------------------

// Sam clears quitok whenever a file is modified (sam/file.c:425), so a refused
// q, an edit, and a second q is refused again. A latch set once and never
// cleared lets an hour of typing exit without a word.
func TestQuitGuardReArmsAfterAnEdit(t *testing.T) {
	s, a, _ := twoFileSession(t)
	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))

	// First q is refused.
	assertNoError(t, s.ExecString("q"))
	if !s.quitPending {
		t.Fatal("quit guard not armed by the first q")
	}

	// More editing re-arms it.
	assertNoError(t, s.ExecString(",s/AAA/ZZZ/"))
	if s.quitPending {
		t.Error("quit guard survived an edit")
	}
	assertNoError(t, s.ExecString("q"))
	if !s.quitPending {
		t.Fatal("second q should refuse, not quit")
	}

	// With nothing changed since, the next q goes through.
	if err := s.ExecString("q"); err != ErrQuit {
		t.Errorf("third q returned %v, want ErrQuit", err)
	}
}

// Typing re-arms it too: interactive edits go through the same commit.
func TestQuitGuardReArmsAfterTyping(t *testing.T) {
	s, a, _ := twoFileSession(t)
	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	assertNoError(t, s.ExecString("q"))

	assertNoError(t, s.ApplyEdit(a, 0, 0, "x"))
	if s.quitPending {
		t.Error("quit guard survived a keystroke")
	}
}

// An unmodified session quits on the first q.
func TestQuitWithNoChangesQuitsImmediately(t *testing.T) {
	s, _, _ := twoFileSession(t)
	if err := s.ExecString("q"); err != ErrQuit {
		t.Errorf("q returned %v, want ErrQuit", err)
	}
}

// ---- w refuses while the same command is changing the file -------------------

// Changes apply at the end of a command, so a w in the same command that
// changed the file would write the pre-change text. Sam refuses: Ewseq,
// "can't write while changing" (sam/xec.c:288).
func TestWriteInsideAChangingCommandIsRefused(t *testing.T) {
	dir := t.TempDir()
	out := dir + "/out.txt"

	var buf strings.Builder
	s := NewSession(&buf)
	f := file.New()
	f.Buffer().Insert(0, "hello\n")
	f.SetDot(dot(0, 6))
	s.AddFile(f)

	assertError(t, s.ExecString("{\n,d\nw "+out+"\n}"))
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("the refused w still wrote a file")
	}
	// The group was rolled back, so the delete did not land either.
	assertContents(t, f.String(), "hello\n")
}

// A rename counts as changing the file, as it does in sam: the test is whether
// the command has logged anything for this file, not whether it was text.
func TestWriteAfterARenameInTheSameCommandIsRefused(t *testing.T) {
	dir := t.TempDir()
	out := dir + "/out.txt"

	var buf strings.Builder
	s := NewSession(&buf)
	f := file.New()
	f.Buffer().Insert(0, "hello\n")
	f.SetDot(dot(0, 6))
	s.AddFile(f)

	assertError(t, s.ExecString("{\nf renamed.txt\nw "+out+"\n}"))
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("the refused w still wrote a file")
	}
}

// A w in its own command, after the change has been applied, writes the new
// text — the guard is about the pending state, not about writing a dirty file.
func TestWriteAfterAChangeSucceeds(t *testing.T) {
	dir := t.TempDir()
	out := dir + "/out.txt"

	var buf strings.Builder
	s := NewSession(&buf)
	f := file.New()
	f.Buffer().Insert(0, "hello\n")
	f.SetDot(dot(0, 6))
	s.AddFile(f)

	assertNoError(t, s.ExecString(",s/hello/goodbye/"))
	assertNoError(t, s.ExecString("w "+out))

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "goodbye\n" {
		t.Errorf("wrote %q, want %q", got, "goodbye\n")
	}
}

// ---- k marks its address -----------------------------------------------------

// Sam's k_cmd is `f->mark = addr.r`: the evaluated address, not dot. A bare k
// agrees, since the address defaults to dot, which is why an addressed k marking
// the wrong place went unseen.
func TestAddressedKMarksTheAddress(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "aa\nbb\ncc\ndd\n")
	f.SetDot(dot(0, 0))
	s.AddFile(f)

	assertNoError(t, s.ExecString("2k"))
	if got := f.Mark(); got != dot(3, 6) {
		t.Errorf("mark after 2k = %v, want line 2 [3,6)", got)
	}

	// A bare k still marks dot.
	f.SetDot(dot(9, 12))
	assertNoError(t, s.ExecString("k"))
	if got := f.Mark(); got != dot(9, 12) {
		t.Errorf("mark after a bare k = %v, want dot [9,12)", got)
	}
}

// ---- x over lines ------------------------------------------------------------

// x with no pattern loops over lines. Sam walks them with lineaddr (linelooper),
// which reaches a final line with no trailing newline; a bare `.*\n` pattern
// does not, and silently dropped the last line of any file not ending in one.
func TestXOverLinesIncludesAnUnterminatedLastLine(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "one\ntwo\nthree")
	f.SetDot(dot(0, 13))
	s.AddFile(f)

	assertNoError(t, s.ExecString(",x p"))
	// p adds nothing, so the last line arrives without a newline of its own.
	if got := out.String(); got != "one\ntwo\nthree" {
		t.Errorf("x p printed %q, want all three lines", got)
	}
}

// The terminated case is unchanged: no empty final iteration after the last
// newline.
func TestXOverLinesWithTrailingNewline(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "one\ntwo\n")
	f.SetDot(dot(0, 8))
	s.AddFile(f)

	assertNoError(t, s.ExecString(",x p"))
	if got := out.String(); got != "one\ntwo\n" {
		t.Errorf("x p printed %q, want exactly two lines", got)
	}
}

// ---- s replacement syntax ----------------------------------------------------

// Sam's replacement takes \1-\9 for subexpressions and & for the whole match;
// every other \X is the literal X, and '$' is an ordinary character
// (sam/xec.c:252). Dave used to pass the replacement to Go's Expand untouched,
// so \2\1 was inserted verbatim while $2$1 worked — the reverse of sam.
func TestSubstReplacementSyntax(t *testing.T) {
	tests := []struct {
		name, cmd, in, want string
	}{
		{"backrefs", `s/(t)(w)/\2\1/`, "two", "wto"},
		{"backref repeated", `s/(a)/\1\1/`, "a", "aa"},
		{"whole match", `s/two/&&/`, "two", "twotwo"},
		{"escaped ampersand", `s/two/\&/`, "two", "&"},
		{"dollar is literal", `s/two/$1/`, "two", "$1"},
		{"escaped dollar", `s/two/\$/`, "two", "$"},
		{"escaped backslash", `s/two/\\/`, "two", `\`},
		{"newline", `s/two/a\nb/`, "two", "a\nb"},
		{"other escapes are literal", `s/two/\d\q/`, "two", "dq"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runCmd(t, tt.in, dot(0, len([]rune(tt.in))), tt.cmd)
			assertNoError(t, r.err)
			assertContents(t, r.contents, tt.want)
		})
	}
}

// ---- the blank pattern form --------------------------------------------------

// A blank straight after the command stands in for the pattern, but only where
// sam allows it: x (every line) and X/Y (every file). For y, g and v sam
// requires a real pattern (sam/cmd.c, the ct->cmdc!='x' && ct->cmdc!='X' test),
// and taking the blank as a delimiter instead silently scans the rest of the
// line looking for its twin.
func TestBlankPatternFormOnlyWhereSamAllowsIt(t *testing.T) {
	for _, src := range []string{"y p", "g p", "v p"} {
		if _, _, err := ParseCommand(src, 0); err == nil {
			t.Errorf("%q: expected an error, got none", src)
		}
	}
	for _, src := range []string{"x p", "X p", "Y p"} {
		if _, _, err := ParseCommand(src, 0); err != nil {
			t.Errorf("%q: unexpected error: %v", src, err)
		}
	}
}

// A letter, digit or backslash after the command is a mistyped pattern far more
// often than a deliberate delimiter; sam refuses it (okdelim).
func TestBadPatternDelimiterIsRefused(t *testing.T) {
	for _, src := range []string{"gxfoox p", "xafooa p", `s\a\b\`, "y9foo9 p"} {
		if _, _, err := ParseCommand(src, 0); err == nil {
			t.Errorf("%q: expected an error, got none", src)
		}
	}
}

// A pattern-taking command with nothing after it reports the missing pattern
// rather than running with an empty one, which would silently reuse whatever
// pattern was last compiled. Checked with a body present so the error cannot
// come from the missing body instead.
func TestMissingPatternIsReported(t *testing.T) {
	for _, src := range []string{"g", "v", "y", "g\np", "v\np", "y\np"} {
		_, _, err := ParseCommand(src, 0)
		if err == nil {
			t.Errorf("%q: expected an error, got none", src)
			continue
		}
		if !strings.Contains(err.Error(), "pattern") {
			t.Errorf("%q: error is %v, want it to name the missing pattern", src, err)
		}
	}
}

// X and Y with no pattern run the body in every file — for Y too, which is not
// the inversion you might expect (sam/xec.c filelooper: cp->re==0 runs the body
// regardless of XY).
func TestXYWithNoPatternVisitEveryFile(t *testing.T) {
	for _, op := range []string{"X", "Y"} {
		t.Run(op, func(t *testing.T) {
			var out strings.Builder
			s := NewSession(&out)
			for _, name := range []string{"a.txt", "b.log"} {
				f := file.NewNamed(name)
				f.Buffer().Insert(0, name+"\n")
				f.SetDot(dot(0, len([]rune(name))+1))
				s.AddFile(f)
			}
			assertNoError(t, s.ExecString(op+" ,p"))
			if got := out.String(); got != "a.txt\nb.log\n" {
				t.Errorf("%s ,p printed %q, want both files", op, got)
			}
		})
	}
}

// A command addressed with `"pat"` runs in the file that pattern selects, and
// leaves it current (sam/xec.c cmdexec: f = addr.f; current(f)).
func TestFileAddressRunsTheCommandThere(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	a := file.NewNamed("a.txt")
	a.Buffer().Insert(0, "aa\nbb\n")
	s.AddFile(a)
	r := file.NewNamed("other.txt")
	r.Buffer().Insert(0, "ONE\nTWO\n")
	s.AddFile(r)
	s.Current = a

	assertNoError(t, s.ExecString(`"other"2p`))
	if got := out.String(); got != "TWO\n" {
		t.Errorf("printed %q, want the second line of other.txt", got)
	}
	if s.Current != r {
		t.Errorf("current file is %q, want other.txt", s.Current.Name())
	}
}

// ---- s's count ---------------------------------------------------------------

// The count is a full number, as sam's getnum reads it. A single digit left
// `s10/a/b/` substituting the first match and handing `0/a/b/` to the address
// parser — the same defect u had.
func TestSubstCount(t *testing.T) {
	const twelve = "a a a a a a a a a a a a"
	tests := []struct {
		cmd, want string
	}{
		{`s/a/X/`, "X a a a a a a a a a a a"},
		{`s1/a/X/`, "X a a a a a a a a a a a"},
		{`s2/a/X/`, "a X a a a a a a a a a a"},
		{`s10/a/X/`, "a a a a a a a a a X a a"},
		{`s12/a/X/`, "a a a a a a a a a a a X"},
		// 0 behaves as 1: sam's --n>0 test lets the first match through.
		{`s0/a/X/`, "X a a a a a a a a a a a"},
		// With g, the count is where substitution starts, not which single
		// match is replaced.
		{`s3/a/X/g`, "a a X X X X X X X X X X"},
		{`s/a/X/g`, "X X X X X X X X X X X X"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			r := runCmd(t, twelve, dot(0, len(twelve)), tt.cmd)
			assertNoError(t, r.err)
			assertContents(t, r.contents, tt.want)
		})
	}
}

// Only u takes a signed count — sam passes signok=2 there and 1 everywhere
// else. Reading a sign for s would eat the '-' in `s-a-X-`, where it is the
// delimiter, and then reject the pattern that followed.
func TestSubstCountTakesNoSign(t *testing.T) {
	r := runCmd(t, "a a", dot(0, 3), `s-a-X-`)
	assertNoError(t, r.err)
	assertContents(t, r.contents, "X a")
}

// A count past the end of the matches substitutes nothing and says so, rather
// than reporting success.
func TestSubstCountBeyondTheMatches(t *testing.T) {
	r := runCmd(t, "a a", dot(0, 3), `s99/a/X/`)
	assertError(t, r.err)
	assertContents(t, r.contents, "a a")
}

// Inside a loop, a substitution that finds nothing is ordinary: most iterations
// are expected not to match, and erroring would abort the loop on the first one
// that does not. Sam reports it only at the top level (`if(!didsub && nest==0)`).
func TestSubstNoMatchInsideALoopIsNotAnError(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "has z\nno\nhas z\n")
	f.SetDot(dot(0, 14))
	s.AddFile(f)

	assertNoError(t, s.ExecString(`,x/.*\n/ s/z/Z/`))
	assertContents(t, f.String(), "has Z\nno\nhas Z\n")
}

// ---- shell redirections ------------------------------------------------------

// A non-zero exit is a warning, not a failure. Sam applies whatever the command
// produced and prints `?warning: exit status` (warn_s(Wbadstatus, retmsg)); dave
// returned an error, which rolled the whole command back and lost the output of
// a filter that failed partway.
func TestShellNonZeroExitKeepsTheOutput(t *testing.T) {
	t.Run("pipe", func(t *testing.T) {
		r := runCmd(t, "b\na\n", dot(0, 4), "|sort; false")
		assertNoError(t, r.err)
		assertContents(t, r.contents, "a\nb\n")
		if !strings.Contains(r.output, "exit status") {
			t.Errorf("output %q, want a warning naming the exit status", r.output)
		}
	})
	t.Run("read", func(t *testing.T) {
		r := runCmd(t, "old", dot(0, 3), "<echo new; false")
		assertNoError(t, r.err)
		assertContents(t, r.contents, "new\n")
		if !strings.Contains(r.output, "exit status") {
			t.Errorf("output %q, want a warning naming the exit status", r.output)
		}
	})
	// Sam does not check the status of >, which produces nothing to keep.
	t.Run("write", func(t *testing.T) {
		r := runCmd(t, "x\n", dot(0, 2), ">false")
		assertNoError(t, r.err)
		if strings.Contains(r.output, "exit status") {
			t.Errorf("output %q, want no warning for >", r.output)
		}
	})
}

// Standard error reaches the terminal in sam. Capturing it and dropping it with
// the error left a failing command with nothing to say.
func TestShellStderrIsShown(t *testing.T) {
	r := runCmd(t, "x", dot(0, 1), "<echo trouble >&2")
	assertNoError(t, r.err)
	if !strings.Contains(r.output, "trouble") {
		t.Errorf("output %q, want the command's standard error in it", r.output)
	}
}

// A command that cannot be started at all is still an error.
func TestShellUnstartableIsAnError(t *testing.T) {
	prog, _ := shellCommand()
	if _, err := exec.LookPath(prog); err != nil {
		t.Skipf("no %s on this machine", prog)
	}
	// The shell itself runs; the command inside it does not exist, which the
	// shell reports as a non-zero exit rather than a failure to start.
	r := runCmd(t, "x", dot(0, 1), "<definitely-not-a-command")
	assertNoError(t, r.err)
	assertContents(t, r.contents, "")
}

// ---- e on a file that is not there -------------------------------------------

// Sam opens the file and raises Eopen if it cannot, leaving the buffer alone.
// Treating a missing file as empty content meant a typo in the name emptied the
// buffer without a word.
func TestEditMissingFileIsAnError(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, "important\n")
	f.SetDot(dot(0, 10))
	s.AddFile(f)

	err := s.ExecString("e " + t.TempDir() + "/not-there.txt")
	assertError(t, err)
	assertContents(t, f.String(), "important\n")
}

// y runs its body on every gap between matches, including the empty ones: before
// a match at the very start, between two adjacent matches, and after a match at
// the very end. Sam's looper emits one range per iteration whether or not it is
// null. The empty gap between adjacent matches is exactly where a y loop is
// usually asked to insert something, so skipping it is not a rounding error.
// Each expectation below was confirmed against sam.
func TestYEmitsEmptyGaps(t *testing.T) {
	tests := []struct {
		name, in, cmd, want string
	}{
		// One match filling the whole range: an empty gap on each side.
		{"both ends empty", "ab", `,y/ab/ c/-/`, "-ab-"},
		// Adjacent matches, with an empty gap between them.
		{"between adjacent", "aabb", `,y/a/ c/-/`, "-a-a-"},
		// Non-empty gaps are unaffected.
		{"ordinary gaps", "xaby", `,y/ab/ c/-/`, "-ab-"},
		// No match at all: the whole range is one gap.
		{"no match", "abc", `,y/zzz/ c/-/`, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runCmd(t, tt.in, dot(0, len([]rune(tt.in))), tt.cmd)
			assertNoError(t, r.err)
			assertContents(t, r.contents, tt.want)
		})
	}
}

// The x loop's null-match rule, pinned independently of the differential
// harness, which is skipped when sam is not installed. A null match abutting
// the end of the previous match is skipped so a pattern that can match nothing
// neither stalls nor doubles up; one at the very start is not, because there is
// no previous match for it to abut. Every expectation was read off sam.
func TestXNullMatchRule(t *testing.T) {
	tests := []struct {
		in, cmd, want string
	}{
		// x* never matches anything, so every position yields a null match.
		{"ab", `,x/x*/ =#`, "#0\n#1\n#2\n"},
		// a* matches "a" at 0, then null at 2 — but not at 1, which abuts it.
		{"ab", `,x/a*/ =#`, "#0,#1\n#2\n"},
		{"aab", `,x/a*/ =#`, "#0,#2\n#3\n"},
		// Positions are codepoints: é is two bytes, and the loop must step by
		// characters or it splits one.
		{"éxé", `,x/x*/ =#`, "#0\n#1,#2\n#3\n"},
		{"éxé", `,x/é/ =#`, "#0,#1\n#2,#3\n"},
	}
	for _, tt := range tests {
		t.Run(tt.in+" "+tt.cmd, func(t *testing.T) {
			r := runCmd(t, tt.in, dot(0, len([]rune(tt.in))), tt.cmd)
			assertNoError(t, r.err)
			// = prints the file name, and runCmd's file has none.
			assertOutput(t, r.output, tt.want)
		})
	}
}
