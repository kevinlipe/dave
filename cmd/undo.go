package cmd

import (
	"dave/buffer"
	"dave/file"
	"fmt"
	"unicode/utf8"
)

// Undo and redo. The reference is 9front (~/src/9front/sys/src/cmd/sam):
// sam/file.c for the log, sam/xec.c:267 for the signed count on `u`.
//
// Sam keeps two logs per file, delta (undo) and epsilon (redo), and every
// record carries the sequence number of the command that wrote it. Undo pops
// delta, applies the records, and pushes their inverses onto epsilon; redo runs
// the same loop with the two swapped, stopping when the sequence number
// changes. That last part is what makes one command — not one buffer mutation —
// one step, and it is the part worth keeping.
//
// Dave does not copy the rest of the shape. Sam's undo is per file: `undo()`
// reads the sequence number off the *current* file and steps only the files
// sharing it (sam/sam.c:451), so editing a, editing b and then making a current
// undoes a's change and leaves b's, and once a's history is spent `u` does
// nothing at all even though b's is not. That is hard to defend on its own
// terms, and it reads against the sam paper's own description ("the undo
// command, u, undoes the last command, no matter how many files were
// affected"). Dave implements the paper: one timeline across all files, `u`
// steps back along it and `u-n` forward, and the grouping that makes a
// multi-file command one step is the entry itself rather than a number shared
// between per-file stacks.
//
// The log lives here in cmd rather than on file.File, keyed by nothing at all,
// so the text engine's layering is untouched.

// fileChange is one file's share of one command. Applying it moves that file
// and yields the change that moves it back.
type fileChange struct {
	f *file.File

	// trs are the text changes, empty for a command that only renamed the file.
	// More than one only for a coalesced typing run. Applied in reverse order,
	// and the results appended in that order, so the reversing change replays
	// the run forwards under the same rule. Applying a transcript returns its
	// own inverse, so the reversing change costs nothing to build.
	trs []*buffer.Transcript

	// State restored when this change is applied: dot, the mark, the name and
	// the position in the edit history that dirtiness is derived from. The sam
	// paper: `e` then `u` restores "the name, dot, and modified bit".
	dot     buffer.Range
	mark    buffer.Range
	name    string
	version int
}

// histEntry is one command: every file it touched, in the order it touched
// them. One entry is one step, however many files or mutations it covered.
type histEntry struct {
	changes []fileChange
}

// UndoLog is the timeline. delta holds what undo consumes, epsilon what redo
// consumes; sam's names are kept even though the shape is not.
type UndoLog struct {
	delta   []histEntry
	epsilon []histEntry

	// An open typing run: the file being typed into and the offset just past
	// the last rune inserted. Anything else — a command, an undo, a
	// non-adjacent or non-typing edit — closes it.
	runFile *file.File
	runEnd  int
}

func newUndoLog() *UndoLog { return &UndoLog{} }

// record pushes one command onto the undo stack and discards the redo stack.
//
// The discard is sam's filemark (sam/file.c:590), which clears epsilon on the
// first logged change of a command: a new edit destroys the redo history, so
// the history is a line rather than a tree. Sam discards per file, since its
// stacks are per file; on one timeline the discard is the whole of it, which is
// what keeps a redo from ever reaching back past a newer edit.
func (u *UndoLog) record(e histEntry, join bool) {
	closing := u.runFile
	u.runFile = nil
	if len(e.changes) == 0 {
		return
	}
	u.epsilon = nil
	// An open run implies the last delta entry is that run's, since stepping
	// the history and dropping a file both close it.
	if join && closing != nil && len(e.changes) == 1 {
		if last := &u.delta[len(u.delta)-1]; len(last.changes) == 1 &&
			last.changes[0].f == e.changes[0].f {
			// Extend the run. Its dot, mark, name and version stay those of
			// the first keystroke, so undo lands where typing began.
			last.changes[0].trs = append(last.changes[0].trs, e.changes[0].trs...)
			return
		}
	}
	u.delta = append(u.delta, e)
}

// typed reports whether an edit is one keystroke of ordinary typing: a single
// rune inserted, deleting nothing. A newline ends a run rather than extending
// it — sam would carry on (samterm's nontypingkey does not list it), but a
// paragraph is the unit worth taking back in prose.
func typed(del int, text string) bool {
	return del == 0 && utf8.RuneCountInString(text) == 1 && text != "\n"
}

// canJoin reports whether this edit continues the open run.
func (u *UndoLog) canJoin(f *file.File, pos, del int, text string) bool {
	return u.runFile == f && u.runEnd == pos && typed(del, text)
}

// noteRun opens or closes the typing run after an edit is committed.
func (u *UndoLog) noteRun(f *file.File, pos, del int, text string) {
	if typed(del, text) {
		u.runFile, u.runEnd = f, pos+1
		return
	}
	u.runFile = nil
}

// forget drops every trace of a file from the timeline, and any entry left
// empty by its removal. Adding and deleting files is not undoable in sam ("nor
// may any command that adds or deletes files"), so a file that leaves the
// session must not leave changes behind that name it.
func (u *UndoLog) forget(f *file.File) {
	if u.runFile == f {
		u.runFile = nil
	}
	u.delta = forgetIn(u.delta, f)
	u.epsilon = forgetIn(u.epsilon, f)
}

func forgetIn(entries []histEntry, f *file.File) []histEntry {
	out := entries[:0]
	for _, e := range entries {
		kept := e.changes[:0]
		for _, c := range e.changes {
			if c.f != f {
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			out = append(out, histEntry{changes: kept})
		}
	}
	return out
}

// step moves one command in the given direction and reports whether it moved
// anything. Every file the command touched moves together.
func (s *Session) step(isundo bool) (bool, error) {
	s.Undo.runFile = nil
	src, dst := &s.Undo.delta, &s.Undo.epsilon
	if !isundo {
		src, dst = dst, src
	}
	if len(*src) == 0 {
		return false, nil
	}
	e := (*src)[len(*src)-1]

	renamed := false
	back := histEntry{changes: make([]fileChange, 0, len(e.changes))}
	for _, c := range e.changes {
		// Capture the state this change is about to overwrite, so the reverse
		// direction restores it. Sam reads it live off the file for the same
		// reason (fileunsetdot(f, epsilon, f->dot.r) before assigning dot).
		b := fileChange{
			f:       c.f,
			dot:     c.f.Dot(),
			mark:    c.f.Mark(),
			name:    c.f.Name(),
			version: c.f.Version(),
		}
		for i := len(c.trs) - 1; i >= 0; i-- {
			fwd, err := c.trs[i].Apply(c.f.Buffer())
			if err != nil {
				return true, fmt.Errorf("undo: %w", err)
			}
			b.trs = append(b.trs, fwd)
		}
		back.changes = append(back.changes, b)

		c.f.SetDot(c.dot)
		c.f.SetMarkRange(c.mark)
		c.f.SetName(c.name)
		renamed = true
		c.f.SetVersion(c.version)
	}

	*src = (*src)[:len(*src)-1]
	*dst = append(*dst, back)
	if renamed {
		// A restored name can change where the file sorts.
		s.sortFiles(nil)
	}
	return true, nil
}

// doUndo runs the count on `u`. A non-negative count undoes that many commands,
// a negative one redoes (sam/xec.c:267):
//
//	if(n >= 0) while(n-- && undo(TRUE)) ;
//	else       while(n++ && undo(FALSE)) ;
//
// so u0 is a no-op, a bare u is 1, a bare u- is -1, and running past the end of
// the history in either direction stops silently rather than reporting anything.
func (s *Session) doUndo(n int) error {
	for n > 0 {
		moved, err := s.step(true)
		if err != nil {
			return err
		}
		if !moved {
			break
		}
		n--
	}
	for n < 0 {
		moved, err := s.step(false)
		if err != nil {
			return err
		}
		if !moved {
			break
		}
		n++
	}
	return nil
}

// DoUndo undoes the last command. It is a convenience wrapper for menu/UI use.
func (s *Session) DoUndo() error { return s.doUndo(1) }
