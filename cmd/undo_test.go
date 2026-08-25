package cmd

import (
	"dave/buffer"
	"dave/file"
	"os"
	"strings"
	"testing"
)

// Undo and redo follow sam exactly; these tests pin the parts of that contract
// that are easy to break from a distance. The reference is sam/file.c (the
// delta/epsilon logs), sam/sam.c:451 (undo, which anchors on the current file)
// and sam/xec.c:267 (u_cmd, the signed count). plan9port and 9front agree on
// all of it, so there is one behaviour to match.

// newUndoSession returns a session with one file holding content, dot covering
// it all, and that file current.
func newUndoSession(content string) (*Session, *file.File) {
	var out strings.Builder
	s := NewSession(&out)
	f := file.New()
	f.Buffer().Insert(0, content)
	f.SetDot(dot(0, len([]rune(content))))
	s.AddFile(f)
	return s, f
}

// ---- The count ---------------------------------------------------------------

// The count is parsed the way sam's getnum does (sam/cmd.c:166): a full
// multi-digit number, an optional leading '-' meaning redo, and no blanks
// skipped on either side. A single-digit parser passes u3 but leaves the
// trailing digits of u12 behind as a stray address, which undoes once and then
// reports a bogus out-of-range line — the worst answer available when you are
// backing out a global edit in a hurry.
//
// Running off either end of the history is a silent clamp, not an error: sam's
// undo() returns 0 and the && in u_cmd's loop short-circuits.
func TestUndoCount(t *testing.T) {
	// Twelve appends, so the log holds twelve single-character steps.
	const full = "x!!!!!!!!!!!!"
	tests := []struct {
		cmd  string
		want string
	}{
		{"u1", "x!!!!!!!!!!!"},
		{"u3", "x!!!!!!!!!"},
		{"u9", "x!!!"},
		{"u10", "x!!"},
		{"u12", "x"},
		{"u100", "x"},             // past the end of the history: silent clamp
		{"u99", "x"},              // likewise
		{"u0", full},              // an explicit 0 undoes nothing
		{"u", full[:len(full)-1]}, // no number means 1
		{"u-", full},              // a bare '-' is -1: redo, with nothing to redo
		{"u-1", full},             // likewise
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			s, f := newUndoSession("x")
			for i := 0; i < 12; i++ {
				assertNoError(t, s.ExecString("a/!/"))
			}
			assertNoError(t, s.ExecString(tt.cmd))
			assertContents(t, f.String(), tt.want)
		})
	}
}

// A digit string longer than an int holds must saturate rather than wrap: a
// wrapped count can come out negative, which turns a fat-fingered undo into a
// redo. Checked across lengths because where the wrap lands depends on the
// width of int — 19 nines is the first to go negative on 64-bit, and a 32-bit
// int turns over far sooner.
func TestUndoCountSaturates(t *testing.T) {
	for l := 1; l <= 25; l++ {
		src := "u" + strings.Repeat("9", l)
		c, _, err := ParseCommand(src, 0)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if c.N <= 0 || c.N > maxCount*10 {
			t.Errorf("%s: N = %d, want a positive count no larger than %d",
				src, c.N, maxCount*10)
		}
	}
}

// Sam raises Enoaddr for an address on a command whose defaddr is aNo
// (sam/cmd.c:427). Without this, `,u` silently ignores the address, which reads
// as though the address selected what to undo.
func TestUndoRejectsAddress(t *testing.T) {
	for _, src := range []string{",u", "1u", "0,$u", ".u"} {
		if _, _, err := ParseCommand(src, 0); err == nil {
			t.Errorf("%s: expected an error, got none", src)
		}
	}
}

// ---- Nothing to undo ---------------------------------------------------------

// Undo with an empty history is silent. Sam's undo() returns 0 and u_cmd's loop
// simply ends; nothing is printed and no error is raised. Dave used to report
// "nothing to undo", which is the divergence this pins shut.
func TestUndoEmptyHistoryIsSilent(t *testing.T) {
	s, f := newUndoSession("hello")
	before := f.Dot()

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "hello")
	assertNoError(t, s.ExecString("u5"))
	assertContents(t, f.String(), "hello")
	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, f.String(), "hello")

	if f.Dot() != before {
		t.Errorf("dot = %v, want it untouched at %v", f.Dot(), before)
	}
	if f.Modified() {
		t.Error("file is modified after undoing nothing")
	}
}

// A command that changed nothing consumes no sequence number, so the undo that
// follows it reaches past it to the last command that did change something.
// Sam gets this from update(), which only bumps seq `if(anymod)`.
func TestUndoSkipsCommandThatChangedNothing(t *testing.T) {
	s, f := newUndoSession("hello world")

	assertNoError(t, s.ExecString("1,1s/hello/goodbye/"))
	assertContents(t, f.String(), "goodbye world")

	// A print changes nothing.
	assertNoError(t, s.ExecString(",p"))

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "hello world")
}

// ---- Redo --------------------------------------------------------------------

// A negative count redoes, one command per step, and stops silently at the end
// of the redo history just as undo stops at the start of the undo history.
func TestRedo(t *testing.T) {
	s, f := newUndoSession("abc")
	assertNoError(t, s.ExecString(`,s/a/1/`))
	assertNoError(t, s.ExecString(`,s/b/2/`))
	assertNoError(t, s.ExecString(`,s/c/3/`))
	assertContents(t, f.String(), "123")

	assertNoError(t, s.ExecString("u3"))
	assertContents(t, f.String(), "abc")

	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, f.String(), "1bc")

	assertNoError(t, s.ExecString("u-2"))
	assertContents(t, f.String(), "123")

	// Past the end of the redo history: silent, no change.
	assertNoError(t, s.ExecString("u-5"))
	assertContents(t, f.String(), "123")
}

// Undo and redo are one cursor over one history, so they interleave freely.
func TestUndoRedoInterleaved(t *testing.T) {
	s, f := newUndoSession("abcde")
	for _, c := range []string{`,s/a/1/`, `,s/b/2/`, `,s/c/3/`, `,s/d/4/`, `,s/e/5/`} {
		assertNoError(t, s.ExecString(c))
	}
	assertContents(t, f.String(), "12345")

	assertNoError(t, s.ExecString("u3"))
	assertContents(t, f.String(), "12cde")

	assertNoError(t, s.ExecString("u-2"))
	assertContents(t, f.String(), "1234e")

	assertNoError(t, s.ExecString("u1"))
	assertContents(t, f.String(), "123de")

	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, f.String(), "1234e")
}

// A new edit after an undo discards the redo history. This is sam's filemark
// (sam/file.c:590), which opens by clearing epsilon on the first logged change
// of a command. It is the single semantic that decides whether a history is a
// line or a tree, and sam picks the line.
func TestNewEditTruncatesRedo(t *testing.T) {
	s, f := newUndoSession("abc")
	assertNoError(t, s.ExecString(`,s/a/1/`))
	assertNoError(t, s.ExecString(`,s/b/2/`))
	assertContents(t, f.String(), "12c")

	assertNoError(t, s.ExecString("u2"))
	assertContents(t, f.String(), "abc")

	// A new edit here throws away the two steps we could have redone.
	assertNoError(t, s.ExecString(`,s/c/9/`))
	assertContents(t, f.String(), "ab9")

	assertNoError(t, s.ExecString("u-5"))
	assertContents(t, f.String(), "ab9")

	// The new edit is itself undoable, and undoing it does not resurrect the
	// discarded redo entries either.
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "abc")
	assertNoError(t, s.ExecString("u-5"))
	assertContents(t, f.String(), "ab9")
}

// ---- One command is one step -------------------------------------------------

// A global command is one undo step, not one per match. Undo is least useful
// exactly when a composed command did something unexpected across forty places,
// which is precisely when it gets reached for. Sam gets this by stamping every
// record of a command with the same sequence number and unwinding until the
// number changes.
func TestGlobalCommandUndoesAsOneUnit(t *testing.T) {
	t.Run("x with c", func(t *testing.T) {
		s, f := newUndoSession("cat dog cat dog cat")
		assertNoError(t, s.ExecString("x/cat/ c/CAT/"))
		assertContents(t, f.String(), "CAT dog CAT dog CAT")

		assertNoError(t, s.ExecString("u1"))
		assertContents(t, f.String(), "cat dog cat dog cat")

		assertNoError(t, s.ExecString("u-1"))
		assertContents(t, f.String(), "CAT dog CAT dog CAT")
	})

	t.Run("x with d", func(t *testing.T) {
		s, f := newUndoSession("a1b2c3d4e5")
		assertNoError(t, s.ExecString("x/[0-9]/ d"))
		assertContents(t, f.String(), "abcde")

		assertNoError(t, s.ExecString("u1"))
		assertContents(t, f.String(), "a1b2c3d4e5")
	})

	t.Run("brace group", func(t *testing.T) {
		s, f := newUndoSession("hello")
		assertNoError(t, s.ExecString("{\n,s/h/H/\n,s/o/O/\n}"))
		assertContents(t, f.String(), "HellO")

		// The whole group is one step: one u takes back both substitutions.
		assertNoError(t, s.ExecString("u1"))
		assertContents(t, f.String(), "hello")
	})
}

// ---- Dot, mark, name and the modify bit --------------------------------------

// Undo restores dot to where it was before the undone command, and redo puts it
// back where that command left it. Sam logs dot as a record of its own: filemark
// saves prevdot before the command, fileupdate writes it into delta, and
// fileundo records the live dot into epsilon on the way past.
func TestUndoRestoresDot(t *testing.T) {
	s, f := newUndoSession("hello world")
	f.SetDot(dot(0, 5))

	assertNoError(t, s.ExecString("c/goodbye/"))
	assertContents(t, f.String(), "goodbye world")
	after := f.Dot()

	assertNoError(t, s.ExecString("u"))
	if got := f.Dot(); got != dot(0, 5) {
		t.Errorf("dot after undo = %v, want %v (dot before the command)", got, dot(0, 5))
	}

	assertNoError(t, s.ExecString("u-1"))
	if got := f.Dot(); got != after {
		t.Errorf("dot after redo = %v, want %v (dot the command left)", got, after)
	}
}

// Dot is restored at every step of a multi-command undo, not just the last one.
func TestUndoRestoresDotEachStep(t *testing.T) {
	s, f := newUndoSession("abcdef")

	f.SetDot(dot(0, 1))
	assertNoError(t, s.ExecString("c/A/"))
	f.SetDot(dot(2, 3))
	assertNoError(t, s.ExecString("c/C/"))
	f.SetDot(dot(4, 5))
	assertNoError(t, s.ExecString("c/E/"))
	assertContents(t, f.String(), "AbCdEf")

	assertNoError(t, s.ExecString("u1"))
	if got := f.Dot(); got != dot(4, 5) {
		t.Errorf("dot after first undo = %v, want %v", got, dot(4, 5))
	}
	assertNoError(t, s.ExecString("u1"))
	if got := f.Dot(); got != dot(2, 3) {
		t.Errorf("dot after second undo = %v, want %v", got, dot(2, 3))
	}
	assertNoError(t, s.ExecString("u1"))
	if got := f.Dot(); got != dot(0, 1) {
		t.Errorf("dot after third undo = %v, want %v", got, dot(0, 1))
	}
	assertContents(t, f.String(), "abcdef")
}

// The mark is logged and restored alongside dot. Sam writes a Mark record from
// prevmark for the same reason it writes a Dot record, so a `k` that a later
// command overwrote comes back when that command is undone.
//
// A bare `k` is not itself undoable — it logs nothing, so it takes no sequence
// number, in sam as in dave. The mark is only captured by a command that also
// changes text, and it is captured at that command's first change: sam's
// filemark saves prevmark at the first log operation, so a `k` earlier in the
// same brace group has already moved it. The group below is ordered to put the
// change first for exactly that reason.
func TestUndoRestoresMark(t *testing.T) {
	s, f := newUndoSession("hello world")

	f.SetDot(dot(0, 5))
	assertNoError(t, s.ExecString("k"))
	if got := f.Mark(); got != dot(0, 5) {
		t.Fatalf("mark = %v, want %v", got, dot(0, 5))
	}

	// One command that changes text and then moves the mark.
	f.SetDot(dot(6, 11))
	assertNoError(t, s.ExecString("{\nc/there/\nk\n}"))
	assertContents(t, f.String(), "hello there")
	moved := f.Mark()
	if moved == dot(0, 5) {
		t.Fatalf("mark = %v, want the k inside the group to have moved it", moved)
	}

	assertNoError(t, s.ExecString("u"))
	if got := f.Mark(); got != dot(0, 5) {
		t.Errorf("mark after undo = %v, want %v (the mark before the command)", got, dot(0, 5))
	}

	assertNoError(t, s.ExecString("u-1"))
	if got := f.Mark(); got != moved {
		t.Errorf("mark after redo = %v, want %v (the mark the command left)", got, moved)
	}
}

// The modify bit is restored, so undoing back to the last saved state leaves the
// file clean. Sam logs it on every record (u.mod) and derives dirtiness from
// seq != cleanseq; the old comment in dave claimed sam left the bit set, which
// was simply wrong.
func TestUndoRestoresModifiedBit(t *testing.T) {
	s, f := newUndoSession("hello")
	if f.Modified() {
		t.Fatal("a fresh file is already modified")
	}

	assertNoError(t, s.ExecString(",s/hello/goodbye/"))
	if !f.Modified() {
		t.Fatal("file is not modified after an edit")
	}

	assertNoError(t, s.ExecString("u"))
	if f.Modified() {
		t.Error("file is still modified after undoing the only edit")
	}

	assertNoError(t, s.ExecString("u-1"))
	if !f.Modified() {
		t.Error("file is not modified after redoing the edit")
	}
}

// Undoing past a write leaves the file dirty: the disk holds the written text
// and the buffer no longer does. A modify bit stored per entry gets this wrong,
// reporting clean and letting q exit without a word, because a write changes
// what clean means for every earlier point in the history.
func TestUndoPastAWriteIsDirty(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/m.txt"
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed(path)
	s.AddFile(f)
	if err := f.EnsureLoaded(); err != nil {
		t.Fatal(err)
	}

	assertNoError(t, s.ExecString(",s/original/one/"))
	assertNoError(t, s.ExecString(",s/one/two/"))
	assertNoError(t, s.ExecString("w"))
	if f.Modified() {
		t.Fatal("modified straight after a write")
	}

	assertNoError(t, s.ExecString("u"))
	if !f.Modified() {
		t.Error("clean after undoing one step back from the write")
	}
	assertNoError(t, s.ExecString("u"))
	if !f.Modified() {
		t.Error("clean after undoing past the write, but the disk holds \"two\"")
	}

	// Forward again to the written state: clean once more.
	assertNoError(t, s.ExecString("u-2"))
	assertContents(t, f.String(), "two\n")
	if f.Modified() {
		t.Error("modified after redoing back to the written state")
	}
}

// `f newname` is undoable, and undo puts the old name back. Sam's f_cmd routes
// through getname(f, s, TRUE), which writes a Filename record; without one, the
// rename leaves no entry at all and the following u reaches past it and undoes
// whatever came before, which is worse than not restoring the name.
func TestUndoRestoresNameAfterF(t *testing.T) {
	s, f := newUndoSession("hello")
	f.SetName("old.txt")

	assertNoError(t, s.ExecString(",s/hello/goodbye/"))
	assertNoError(t, s.ExecString("f new.txt"))
	if f.Name() != "new.txt" {
		t.Fatalf("name = %q, want new.txt", f.Name())
	}

	// The rename is its own step: this u takes back the name, not the edit.
	assertNoError(t, s.ExecString("u"))
	if f.Name() != "old.txt" {
		t.Errorf("name after undo = %q, want old.txt", f.Name())
	}
	assertContents(t, f.String(), "goodbye")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "hello")

	assertNoError(t, s.ExecString("u-2"))
	if f.Name() != "new.txt" {
		t.Errorf("name after redo = %q, want new.txt", f.Name())
	}
	assertContents(t, f.String(), "goodbye")
}

// The sam paper's example: "e filename / u restores the state of the file
// completely, including its name, dot, and modified bit."
func TestUndoRestoresStateAfterE(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/other.txt"
	if err := os.WriteFile(path, []byte("replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, f := newUndoSession("original\n")
	f.SetName("original.txt")
	f.SetDot(dot(2, 5))
	f.MarkChanged()

	assertNoError(t, s.ExecString("e "+path))
	assertContents(t, f.String(), "replacement\n")
	if f.Name() != path {
		t.Fatalf("name = %q, want %q", f.Name(), path)
	}
	if f.Modified() {
		t.Fatal("file is modified straight after e, which leaves it matching disk")
	}

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "original\n")
	if f.Name() != "original.txt" {
		t.Errorf("name after undo = %q, want original.txt", f.Name())
	}
	if got := f.Dot(); got != dot(2, 5) {
		t.Errorf("dot after undo = %v, want %v", got, dot(2, 5))
	}
	if !f.Modified() {
		t.Error("modify bit not restored by undo")
	}
}

// ---- Freshly opened files ----------------------------------------------------

// Undo immediately after opening a file, with no edits made, does nothing at
// all — and in particular does not disturb the buffer, dot, or the modify bit.
// Sam guards this twice: filemark returns early for an unread file, and fileload
// panics outright if it is asked to load into a file with history.
func TestUndoOnFreshFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/fresh.txt"
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	s := NewSession(&out)
	f := file.NewNamed(path)
	s.AddFile(f)
	if err := f.EnsureLoaded(); err != nil {
		t.Fatal(err)
	}

	assertNoError(t, s.ExecString("u"))
	assertNoError(t, s.ExecString("u10"))
	assertNoError(t, s.ExecString("u-1"))

	assertContents(t, f.String(), "one\ntwo\nthree\n")
	if f.Modified() {
		t.Error("a freshly opened file is modified after undoing nothing")
	}
	if got := f.Dot(); got != (buffer.Range{}) {
		t.Errorf("dot = %v, want the zero range", got)
	}
}

// ---- More than one file ------------------------------------------------------

// One command spanning several files is one step: the paper's "u undoes the last
// command, no matter how many files were affected". Sam gets this by stepping
// every file whose top record carries the current file's sequence number.
func TestUndoAcrossFilesIsOneStep(t *testing.T) {
	var out strings.Builder
	s := NewSession(&out)
	var files []*file.File
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		f := file.NewNamed(name)
		f.Buffer().Insert(0, "foo bar\n")
		f.SetDot(dot(0, 8))
		s.AddFile(f)
		files = append(files, f)
	}

	// X runs the body in every file whose name matches.
	assertNoError(t, s.ExecString("X/\\.txt/ ,s/foo/FOO/"))
	for _, f := range files {
		assertContents(t, f.String(), "FOO bar\n")
	}

	assertNoError(t, s.ExecString("u1"))
	for _, f := range files {
		assertContents(t, f.String(), "foo bar\n")
	}

	assertNoError(t, s.ExecString("u-1"))
	for _, f := range files {
		assertContents(t, f.String(), "FOO bar\n")
	}
}

// Undo runs on one timeline across all files: it takes back the last command,
// whichever file that command touched. This is the sam paper's description ("the
// undo command, u, undoes the last command, no matter how many files were
// affected") rather than sam's code, which reads the sequence number off the
// current file and would leave b's change standing here (sam/sam.c:451). Dave
// deliberately does not reproduce that.
func TestUndoFollowsOneTimeline(t *testing.T) {
	s, a, b := twoFileSession(t)

	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	s.Current = b
	assertNoError(t, s.ExecString(",s/bbb/BBB/"))

	// Back to a. The last command was b's, and that is what u takes back —
	// being on a does not hide it.
	s.Current = a
	assertNoError(t, s.ExecString("u"))
	assertContents(t, a.String(), "AAA")
	assertContents(t, b.String(), "bbb")

	// The next step back is a's, still without changing the current file.
	assertNoError(t, s.ExecString("u"))
	assertContents(t, a.String(), "aaa")
	assertContents(t, b.String(), "bbb")

	// And forward again, in the order they were made.
	assertNoError(t, s.ExecString("u-2"))
	assertContents(t, a.String(), "AAA")
	assertContents(t, b.String(), "BBB")
}

// Truncation covers the whole timeline, not one file's share of it. A redo that
// survived an unrelated file's edit could be replayed on top of a newer change,
// which would make the history a tree; there is one line, so a new edit anywhere
// ends every pending redo.
func TestNewEditTruncatesRedoEverywhere(t *testing.T) {
	s, a, b := twoFileSession(t)

	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	assertNoError(t, s.ExecString("u"))
	assertContents(t, a.String(), "aaa")

	// An edit in b, with a's redo still pending.
	s.Current = b
	assertNoError(t, s.ExecString(",s/bbb/BBB/"))

	s.Current = a
	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, a.String(), "aaa")

	// b's edit is still the last command, and still undoable.
	assertNoError(t, s.ExecString("u"))
	assertContents(t, b.String(), "bbb")
}

// A file dropped from the session takes its changes with it, so a later undo
// cannot reach a file that is no longer in the list. Adding and deleting files
// is not undoable in sam ("nor may any command that adds or deletes files").
// Entries left empty by the removal go too, so they do not consume a step.
func TestDeletedFileLosesItsHistory(t *testing.T) {
	s, a, b := twoFileSession(t)

	s.Current = a
	assertNoError(t, s.ExecString(",s/aaa/AAA/"))
	s.Current = b
	assertNoError(t, s.ExecString(",s/bbb/BBB/"))

	assertNoError(t, s.RemoveFile(b, true))
	if n := len(s.Undo.delta); n != 1 {
		t.Errorf("timeline holds %d entries, want 1 (b's should be gone)", n)
	}

	// The one remaining step is a's, and it is reachable without walking past
	// an empty entry left behind by b.
	s.Current = a
	assertNoError(t, s.ExecString("u"))
	assertContents(t, a.String(), "aaa")
}

// twoFileSession returns a session holding a.txt ("aaa") and b.txt ("bbb"),
// each with dot covering its contents.
func twoFileSession(t *testing.T) (*Session, *file.File, *file.File) {
	t.Helper()
	var out strings.Builder
	s := NewSession(&out)
	mk := func(name, content string) *file.File {
		f := file.NewNamed(name)
		f.Buffer().Insert(0, content)
		f.SetDot(dot(0, len([]rune(content))))
		s.AddFile(f)
		return f
	}
	return s, mk("a.txt", "aaa"), mk("b.txt", "bbb")
}

// ---- Typing runs -------------------------------------------------------------

// type_ feeds text through ApplyEdit one rune at a time, as the keyboard does.
func type_(t *testing.T, s *Session, f *file.File, text string) {
	t.Helper()
	for _, r := range text {
		pos := f.Dot().Start
		if err := s.ApplyEdit(f, pos, 0, string(r)); err != nil {
			t.Fatal(err)
		}
		f.SetDot(dot(pos+1, pos+1))
	}
}

// A run of typing is one undo step, so backing out a word costs one u rather
// than one per character.
func TestTypingRunIsOneStep(t *testing.T) {
	s, f := newUndoSession("")
	type_(t, s, f, "hello")
	assertContents(t, f.String(), "hello")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "")

	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, f.String(), "hello")
}

// Undo lands where typing began, not where it ended.
func TestTypingRunRestoresDotAtItsStart(t *testing.T) {
	s, f := newUndoSession("ab")
	f.SetDot(dot(1, 1))
	type_(t, s, f, "XYZ")
	assertContents(t, f.String(), "aXYZb")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "ab")
	if got := f.Dot(); got != dot(1, 1) {
		t.Errorf("dot after undo = %v, want %v", got, dot(1, 1))
	}
}

// A newline ends the run, so a paragraph comes back a line at a time. This is
// deliberately not sam, which would carry on through the newline.
func TestNewlineBreaksTheRun(t *testing.T) {
	s, f := newUndoSession("")
	type_(t, s, f, "one")
	type_(t, s, f, "\n")
	type_(t, s, f, "two")
	assertContents(t, f.String(), "one\ntwo")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "one\n")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "one")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "")
}

// Moving the cursor ends the run: two words typed in different places are two
// steps, not one.
func TestNonAdjacentEditBreaksTheRun(t *testing.T) {
	s, f := newUndoSession("....")
	f.SetDot(dot(0, 0))
	type_(t, s, f, "ab")
	f.SetDot(dot(4, 4)) // cursor moved
	type_(t, s, f, "cd")
	assertContents(t, f.String(), "ab..cd..")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "ab....")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "....")
}

// A deletion ends the run, and does not join the one before it — including a
// delete that lands exactly where the run left off, which adjacency alone would
// let through.
func TestDeletionBreaksTheRun(t *testing.T) {
	t.Run("backspace", func(t *testing.T) {
		s, f := newUndoSession("")
		type_(t, s, f, "abc")
		assertNoError(t, s.ApplyEdit(f, 2, 1, ""))
		f.SetDot(dot(2, 2))
		assertContents(t, f.String(), "ab")

		assertNoError(t, s.ExecString("u"))
		assertContents(t, f.String(), "abc")
		assertNoError(t, s.ExecString("u"))
		assertContents(t, f.String(), "")
	})

	t.Run("forward delete at the run's end", func(t *testing.T) {
		s, f := newUndoSession("XYZ")
		f.SetDot(dot(0, 0))
		type_(t, s, f, "abc") // run ends at offset 3
		assertNoError(t, s.ApplyEdit(f, 3, 1, ""))
		assertContents(t, f.String(), "abcYZ")

		assertNoError(t, s.ExecString("u"))
		assertContents(t, f.String(), "abcXYZ")
		assertNoError(t, s.ExecString("u"))
		assertContents(t, f.String(), "XYZ")
	})
}

// A command between keystrokes ends the run, so the typing before it and the
// typing after it are separate steps.
func TestCommandBreaksTheRun(t *testing.T) {
	s, f := newUndoSession("")
	type_(t, s, f, "ab")
	assertNoError(t, s.ExecString("#0"))
	f.SetDot(dot(2, 2))
	type_(t, s, f, "cd")
	assertContents(t, f.String(), "abcd")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "ab")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "")
}

// Undo and redo end the run, so typing afterwards starts a fresh step rather
// than extending the entry just moved. The redo case is the one adjacency alone
// does not cover: the run's entry is back on the undo stack and the cursor is
// back where the run ended.
func TestUndoBreaksTheRun(t *testing.T) {
	s, f := newUndoSession("XXXX")
	f.SetDot(dot(0, 0))
	type_(t, s, f, "ab")
	assertContents(t, f.String(), "abXXXX")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "XXXX")
	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, f.String(), "abXXXX")

	// Typing resumes exactly where the run ended, but is its own step.
	f.SetDot(dot(2, 2))
	type_(t, s, f, "c")
	assertContents(t, f.String(), "abcXXXX")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "abXXXX")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "XXXX")
}

// The GUI undo button calls DoUndo directly, without a command to close the run
// on its way past. Stepping the history has to close it itself.
func TestDoUndoBreaksTheRun(t *testing.T) {
	s, f := newUndoSession("XXXX")
	f.SetDot(dot(0, 0))
	type_(t, s, f, "ab")
	assertNoError(t, s.DoUndo())
	assertContents(t, f.String(), "XXXX")

	// Typing resumes where the run ended; it must not rejoin the entry that
	// DoUndo moved onto the redo stack.
	f.SetDot(dot(2, 2))
	type_(t, s, f, "c")
	assertContents(t, f.String(), "XXcXX")

	assertNoError(t, s.DoUndo())
	assertContents(t, f.String(), "XXXX")
}

// Dropping a file mid-run closes the run. A pane can still hold the file after
// D, so an edit can arrive with the run open and its entry already forgotten.
func TestDroppingAFileBreaksTheRun(t *testing.T) {
	s, f := newUndoSession("XXXX")
	f.SetName("a.txt")
	f.SetDot(dot(0, 0))
	type_(t, s, f, "ab")

	assertNoError(t, s.RemoveFile(f, true))

	f.SetDot(dot(2, 2))
	type_(t, s, f, "c")
	assertContents(t, f.String(), "abcXXXX")

	assertNoError(t, s.DoUndo())
	assertContents(t, f.String(), "abXXXX")
}

// A multi-rune insertion — a paste — is its own step and never joins a run.
func TestPasteIsItsOwnStep(t *testing.T) {
	s, f := newUndoSession("")
	type_(t, s, f, "ab")
	assertNoError(t, s.ApplyEdit(f, 2, 0, "PASTED"))
	assertContents(t, f.String(), "abPASTED")

	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "ab")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "")
}

// A long run redoes in order, not reversed: the reversing list is applied by
// the same rule that built it.
func TestTypingRunRedoesInOrder(t *testing.T) {
	s, f := newUndoSession("")
	type_(t, s, f, "abcdefghij")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "")
	assertNoError(t, s.ExecString("u-1"))
	assertContents(t, f.String(), "abcdefghij")
	assertNoError(t, s.ExecString("u"))
	assertContents(t, f.String(), "")
}

// Dirtiness follows a coalesced run as a unit.
func TestTypingRunUndoesToClean(t *testing.T) {
	s, f := newUndoSession("")
	f.MarkClean()
	type_(t, s, f, "hello")
	if !f.Modified() {
		t.Fatal("not modified after typing")
	}
	assertNoError(t, s.ExecString("u"))
	if f.Modified() {
		t.Error("still modified after undoing the whole run")
	}
}
