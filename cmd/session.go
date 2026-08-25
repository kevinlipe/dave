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

	// ConfigRequest is set when the user executes the "Config" GUI command.
	// The GUI reloads the config file and clears this flag.
	ConfigRequest bool

	// quitPending is true after the first 'q' when modified files exist, and is
	// cleared by any further change.
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
//
// A modified file is refused once and dropped on the second try, which is sam's
// closeok latch (sam/sam.c:237, trytoclose). The paper ties this directly to
// undo's safety argument: potentially dangerous commands go unguarded because
// undo can take them back, and D is protected precisely because it destroys the
// information undo would need. Guarding it every time, with no way through,
// turns the protection into a dead end. Modifying the file clears the latch, so
// the confirmation returns for the next round of changes.
func (s *Session) RemoveFile(f *file.File, force bool) error {
	if f.Modified() && !force && !f.CloseOK() {
		f.SetCloseOK()
		return fmt.Errorf("%s: modified file", f.Name())
	}
	for i, ff := range s.Files {
		if ff == f {
			s.Files = append(s.Files[:i], s.Files[i+1:]...)
			s.Undo.forget(f)
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

// ---- Command execution context ----------------------------------------------

// execCtx holds state during a single top-level command execution.
// It accumulates changes (transcripts) per file and applies them atomically.
type execCtx struct {
	session *Session
	// perFile records the pending transcript and saved dot for each file touched.
	perFile map[*file.File]*filePending
	// order tracks which files were touched, in order.
	order []*file.File
	// nest counts how many levels of structural commands (x, g, {, …) we're
	// inside. Shell commands only print "!\n" at the top level (nest==0).
	nest int
	// join asks commit to extend the open typing run rather than start a new
	// undo step. Only ApplyEdit sets it.
	join bool
}

type filePending struct {
	transcript *buffer.Transcript

	// State as of the start of this command, captured the first time the
	// command touches the file. This is sam's filemark, which saves prevdot,
	// prevmark and prevmod before the first logged change (sam/file.c:590);
	// undo restores all of it, which is what makes `e file` then `u` put back
	// "the name, dot, and modified bit".
	savedDot     buffer.Range
	savedMark    buffer.Range
	savedName    string
	savedVersion int

	// renamed records that this command changed the file's name, so an entry
	// gets logged even when no text changed. Sam logs a Filename record from
	// getname() for both `e` and `f` (sam/sam.c getname -> logsetname).
	renamed bool

	// resetModified is set by commands whose semantics leave the buffer
	// matching disk (currently just `e`). At commit time, this overrides
	// the default MarkChanged so the file is treated as clean.
	resetModified bool
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
			transcript:   buffer.NewTranscript(),
			savedDot:     f.Dot(),
			savedMark:    f.Mark(),
			savedName:    f.Name(),
			savedVersion: f.Version(),
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

// commit applies all pending transcripts and records the command as one undo
// entry covering every file it touched. Returns the first error encountered.
//
// One command is one entry however many files or mutations it covered, which is
// what sam's shared sequence number buys and what makes an x loop over a hundred
// matches a single `u`. A command that changed nothing records no entry, so the
// undo that follows reaches past it to the last command that did.
//
// If Apply fails partway through a multi-file transaction, the changes recorded
// so far are still logged so `u` can back out what did land. The error is then
// returned to the caller.
func (ctx *execCtx) commit() error {
	entry := histEntry{}
	for _, f := range ctx.order {
		p := ctx.perFile[f]
		var trs []*buffer.Transcript
		if !p.transcript.IsEmpty() {
			inv, err := p.transcript.Apply(f.Buffer())
			if err != nil {
				ctx.session.Undo.record(entry, ctx.join)
				return err
			}
			trs = []*buffer.Transcript{inv}
			f.MarkChanged()
		} else if !p.renamed {
			// Nothing logged for this file. A pending entry can exist without
			// a change — `e` on an empty file that reads back empty — and sam
			// logs nothing in that case either.
			if p.resetModified {
				f.MarkClean()
			}
			continue
		}
		if p.resetModified {
			f.MarkClean()
		}
		entry.changes = append(entry.changes, fileChange{
			f:       f,
			trs:     trs,
			dot:     p.savedDot,
			mark:    p.savedMark,
			name:    p.savedName,
			version: p.savedVersion,
		})
	}
	if len(entry.changes) > 0 {
		// Any change re-arms the quit guard, so a refused q followed by more
		// editing is refused again. Sam clears quitok the same way, from the
		// per-command update (sam/file.c:425).
		ctx.session.quitPending = false
	}
	ctx.session.Undo.record(entry, ctx.join)
	return nil
}

// rollback discards all pending changes without applying them.
func (ctx *execCtx) rollback() {
	ctx.perFile = make(map[*file.File]*filePending)
	ctx.order = nil
}

// ---- Undo execution ---------------------------------------------------------

// ApplyEdit applies a single interactive edit (keyboard typing or deletion) to
// file f, recording it in the undo log. pos is the rune insert position, del
// is the number of runes to delete before inserting, and text is the new text.
// A run of ordinary typing coalesces into one undo step, so backing out a word
// costs one u rather than one per character. Sam gets this from samterm, which
// batches a typing run into one message and flushes it on any non-typing key
// (samterm/main.c flushtyping).
func (s *Session) ApplyEdit(f *file.File, pos, del int, text string) error {
	ctx := newExecCtx(s)
	ctx.join = s.Undo.canJoin(f, pos, del, text)
	if err := ctx.addChange(f, pos, del, text); err != nil {
		ctx.rollback()
		return err
	}
	if err := ctx.commit(); err != nil {
		return err
	}
	s.Undo.noteRun(f, pos, del, text)
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
