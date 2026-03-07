// Package cmd implements Dave's command language parser and executor.
//
// The Session type is the top-level object that holds the file list and
// undo history. Commands are parsed from strings and executed against
// the session state.
package cmd

import (
	"dave/buffer"
	"dave/file"
	"fmt"
	"io"
	"os"
	"strings"
)

// Session holds all state for a running Dave session.
type Session struct {
	Files   []*file.File
	Current *file.File
	Output  io.Writer // where p, =, n, etc. write their output
	Undo    *UndoLog

	// OpenRequest is set by the B command to signal the GUI to open this file
	// in a pane when no pane is currently open. The GUI clears it after handling.
	OpenRequest *file.File

	// quitPending is true after the first 'q' when modified files exist.
	quitPending bool
}

// NewSession creates an empty session.
func NewSession(out io.Writer) *Session {
	if out == nil {
		out = os.Stdout
	}
	return &Session{Output: out, Undo: newUndoLog()}
}

// AddFile adds f to the session's file list and makes it current.
func (s *Session) AddFile(f *file.File) {
	s.Files = append(s.Files, f)
	s.Current = f
}

// RemoveFile removes f from the file list.
// Returns an error if f is modified and force is false.
func (s *Session) RemoveFile(f *file.File, force bool) error {
	if f.Modified() && !force {
		return fmt.Errorf("%s: modified file", f.Name())
	}
	for i, ff := range s.Files {
		if ff == f {
			s.Files = append(s.Files[:i], s.Files[i+1:]...)
			if s.Current == f {
				if len(s.Files) > 0 {
					s.Current = s.Files[0]
				} else {
					s.Current = nil
				}
			}
			return nil
		}
	}
	return fmt.Errorf("file not in list")
}

// FindFile finds the file with the given name.
func (s *Session) FindFile(name string) *file.File {
	for _, f := range s.Files {
		if f.Name() == name {
			return f
		}
	}
	return nil
}

// modifiedFiles returns all files with unsaved changes.
func (s *Session) modifiedFiles() []*file.File {
	var result []*file.File
	for _, f := range s.Files {
		if f.Modified() {
			result = append(result, f)
		}
	}
	return result
}

// printf writes a formatted string to the session output.
func (s *Session) printf(format string, args ...any) {
	fmt.Fprintf(s.Output, format, args...)
}

// ---- UndoLog ----------------------------------------------------------------

// undoEntry records one command's worth of changes across all affected files.
type undoEntry struct {
	changes []fileUndo
}

type fileUndo struct {
	f          *file.File
	inverse    *buffer.Transcript
	savedDot   buffer.Range
}

// UndoLog is a stack of undo entries.
type UndoLog struct {
	entries []undoEntry
}

func newUndoLog() *UndoLog { return &UndoLog{} }

func (u *UndoLog) push(e undoEntry) { u.entries = append(u.entries, e) }

func (u *UndoLog) pop() (undoEntry, bool) {
	if len(u.entries) == 0 {
		return undoEntry{}, false
	}
	e := u.entries[len(u.entries)-1]
	u.entries = u.entries[:len(u.entries)-1]
	return e, true
}

func (u *UndoLog) len() int { return len(u.entries) }

// ---- Command execution context ----------------------------------------------

// execCtx holds state during a single top-level command execution.
// It accumulates changes (transcripts) per file and applies them atomically.
type execCtx struct {
	session    *Session
	// perFile records the pending transcript and saved dot for each file touched.
	perFile    map[*file.File]*filePending
	// order tracks which files were touched, in order.
	order      []*file.File
}

type filePending struct {
	transcript *buffer.Transcript
	savedDot   buffer.Range  // dot before this command
}

func newExecCtx(s *Session) *execCtx {
	return &execCtx{
		session: s,
		perFile: make(map[*file.File]*filePending),
	}
}

// pending returns (creating if necessary) the pending state for file f.
func (ctx *execCtx) pending(f *file.File) *filePending {
	p, ok := ctx.perFile[f]
	if !ok {
		p = &filePending{
			transcript: buffer.NewTranscript(),
			savedDot:   f.Dot(),
		}
		ctx.perFile[f] = p
		ctx.order = append(ctx.order, f)
	}
	return p
}

// addChange records a change to file f in its pending transcript.
func (ctx *execCtx) addChange(f *file.File, pos, del int, text string) error {
	return ctx.pending(f).transcript.Add(buffer.Change{Pos: pos, Del: del, Text: text})
}

// commit applies all pending transcripts, saves inverse for undo, and
// updates dots. Returns the first error encountered.
func (ctx *execCtx) commit() error {
	entry := undoEntry{}
	for _, f := range ctx.order {
		p := ctx.perFile[f]
		if p.transcript.IsEmpty() {
			continue
		}
		inv, err := p.transcript.Apply(f.Buffer())
		if err != nil {
			return err
		}
		f.SetModified(true)
		entry.changes = append(entry.changes, fileUndo{
			f:        f,
			inverse:  inv,
			savedDot: p.savedDot,
		})
	}
	if len(entry.changes) > 0 {
		ctx.session.Undo.push(entry)
	}
	return nil
}

// rollback discards all pending changes without applying them.
func (ctx *execCtx) rollback() {
	ctx.perFile = make(map[*file.File]*filePending)
	ctx.order = nil
}

// ---- Undo execution ---------------------------------------------------------

// DoUndo undoes the last command. It is a convenience wrapper for menu/UI use.
func (s *Session) DoUndo() error { return s.doUndo(1) }

// ApplyEdit applies a single interactive edit (keyboard typing or deletion) to
// file f, recording it in the undo log. pos is the rune insert position, del
// is the number of runes to delete before inserting, and text is the new text.
func (s *Session) ApplyEdit(f *file.File, pos, del int, text string) error {
	ctx := newExecCtx(s)
	if err := ctx.addChange(f, pos, del, text); err != nil {
		ctx.rollback()
		return err
	}
	return ctx.commit()
}

// doUndo undoes the last n commands.
func (s *Session) doUndo(n int) error {
	for i := 0; i < n; i++ {
		entry, ok := s.Undo.pop()
		if !ok {
			if i == 0 {
				return fmt.Errorf("nothing to undo")
			}
			break
		}
		// Apply inverse changes in reverse order.
		for j := len(entry.changes) - 1; j >= 0; j-- {
			fu := entry.changes[j]
			inv := fu.inverse
			// We need to undo: apply the inverse transcript.
			_, err := inv.Apply(fu.f.Buffer())
			if err != nil {
				return fmt.Errorf("undo: %w", err)
			}
			// If the buffer is now back to original, clear modified.
			// We don't track this precisely; the modified bit stays set.
			// (Sam's behavior: undo does not clear modified unless written.)
			fu.f.SetDot(fu.savedDot)
		}
	}
	return nil
}

// ---- Quit -------------------------------------------------------------------

// ErrQuit is returned when the session should exit.
var ErrQuit = fmt.Errorf("quit")

// tryQuit attempts to quit. If there are modified files, it prints their names
// and sets quitPending. If quitPending is already set or force is true, quits.
func (s *Session) tryQuit(force bool) error {
	if force || s.quitPending {
		return ErrQuit
	}
	modified := s.modifiedFiles()
	if len(modified) == 0 {
		return ErrQuit
	}
	names := make([]string, len(modified))
	for i, f := range modified {
		names[i] = f.Name()
	}
	fmt.Fprintf(s.Output, "changed files: %s\n", strings.Join(names, ", "))
	s.quitPending = true
	return nil
}
