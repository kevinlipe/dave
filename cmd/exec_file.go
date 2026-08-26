package cmd

import (
	"dave/buffer"
	"dave/file"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func (s *Session) execN() error {
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current, false)
		s.dprint("%s\n", ml)
	}
	return nil
}

// execB makes a file current, reporting its menu line the way sam's b_cmd does
// (sam/xec.c:78, filename(f) at nest == 0). Inside a loop it stays quiet, so an
// X over forty files does not print forty lines.
func (s *Session) execB(arg string, ctx *execCtx) error {
	names := splitPaths(arg)
	for _, name := range names {
		f := s.FindFile(name)
		if f != nil {
			s.Current = f
			s.newCurrent = true
			// Sam's b_cmd reads the file if it has not been read, which
			// announces it, and otherwise reports the menu line itself — one
			// line either way.
			if err := f.EnsureLoaded(); err != nil {
				return err
			}
			if !s.reportLoad(f) {
				s.reportCurrent(f, ctx)
			}
			return nil
		}
	}
	if len(names) > 0 {
		return fmt.Errorf("b: %q not in buffer list", names[0])
	}
	return nil
}

// reportCurrent prints the file's menu line, as sam does when b or B changes
// the current file at top level.
func (s *Session) reportCurrent(f *file.File, ctx *execCtx) {
	if ctx == nil || ctx.nest > 0 {
		return
	}
	s.dprint("%s\n", f.MenuLine(f == s.Current, false))
}

func (s *Session) execBig(arg string, ctx *execCtx) error {
	names := splitPaths(arg)
	for _, name := range names {
		f := s.FindFile(name)
		if f == nil {
			f = file.NewNamed(name)
			s.AddFile(f)
		}
		s.Current = f
		s.newCurrent = true
		s.OpenRequest = f
		if err := f.EnsureLoaded(); err != nil {
			return err
		}
		if !s.reportLoad(f) {
			s.reportCurrent(f, ctx)
		}
	}
	return nil
}

func (s *Session) execD(arg string) error {
	names := splitPaths(arg)
	if len(names) == 0 || (len(names) == 1 && names[0] == "") {
		if s.Current == nil {
			return fmt.Errorf("D: no current file")
		}
		return s.RemoveFile(s.Current, false)
	}
	for _, name := range names {
		f := s.FindFile(name)
		if f == nil {
			return fmt.Errorf("D: %q not in buffer list", name)
		}
		if err := s.RemoveFile(f, false); err != nil {
			return err
		}
	}
	return nil
}

// splitPaths splits arg on whitespace and expands `~` / `~/` in each field.
func splitPaths(arg string) []string {
	fields := strings.Fields(arg)
	for i, s := range fields {
		fields[i] = file.ExpandPath(s)
	}
	return fields
}

func (s *Session) execE(f *file.File, name string, ctx *execCtx) error {
	name = file.ExpandPath(strings.TrimSpace(name))
	if name == "" {
		name = f.Name()
	}
	if name == "" {
		return fmt.Errorf("e: no filename")
	}
	// A file that is not there is an error, not empty content. Sam opens it and
	// raises Eopen if it cannot (`error_r(Eopen, genc)`), leaving the buffer
	// alone; swallowing the case meant a typo in the name emptied the buffer
	// without a word. New files are made with B, which does not read.
	data, err := os.ReadFile(name)
	if err != nil {
		return fmt.Errorf("e: %w", err)
	}
	newContent := f.DecodeContent(string(data))
	if f.TookNulls() {
		s.dprint("?warning: null characters elided\n")
	}
	oldLen := f.Len()
	// Replace all old content with new content via transcript.
	if oldLen > 0 || newContent != "" {
		del := oldLen
		if err := ctx.addChange(f, 0, del, newContent); err != nil {
			return err
		}
	}
	// After commit, the buffer matches disk — clear the modified bit
	// even though we routed the change through the transcript.
	pend := ctx.pending(f)
	pend.resetModified = true
	// Log the rename before it happens, so undo restores the old name along
	// with the old contents. The sam paper is explicit that `e file` then `u`
	// restores "the name, dot, and modified bit"; sam gets there by having
	// e_cmd call getname(f, s, TRUE), which writes a Filename record.
	pend.renamed = true
	f.SetName(name)
	s.sortFiles(f)
	f.SetDot(buffer.Range{})
	// Sam reports e with the menu line and no count: edit() passes -1 to
	// closeio for e, which suppresses the count, then calls filename(f).
	s.dprint("%s\n", f.MenuLine(f == s.Current, false))
	return nil
}

func (s *Session) execR(f *file.File, dot buffer.Range, name string, ctx *execCtx) error {
	name = file.ExpandPath(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("r: no filename")
	}
	content, err := file.ReadFromPath(name)
	if err != nil {
		return err
	}
	if err := ctx.addChange(f, dot.Start, dot.Len(), content); err != nil {
		return err
	}
	n := utf8.RuneCountInString(content)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	// Just the count: sam's readio prints nothing, and closeio prints #n. The
	// name belongs to w, whose writef prints it before creating the file.
	s.dprint("#%d\n", n)
	return nil
}

func (s *Session) execW(f *file.File, dot buffer.Range, name string, ctx *execCtx) error {
	// Changes are applied at the end of the command, so a w in the same command
	// that changed the file would write the text as it was before. Sam refuses
	// on the same test — fseq == seq, meaning this command has already logged
	// something for this file — and raises Ewseq, "can't write while changing"
	// (sam/xec.c:288). A rename counts, as it does in sam.
	if _, ok := ctx.perFile[f]; ok {
		return fmt.Errorf("w: can't write while changing: %s", f.Name())
	}
	name = file.ExpandPath(strings.TrimSpace(name))
	if name == "" {
		name = f.Name()
	}
	if name == "" {
		return fmt.Errorf("w: no filename")
	}
	// Refuse to write over a file that changed since it was read. Sam warns
	// and returns without writing, having updated its record, so a second w
	// goes through (sam/io.c writef).
	if name == f.Name() && f.WriteStale() {
		// A warning, not an error: sam prints it and returns without writing,
		// leaving a loop that reaches other files to carry on.
		s.dprint("?warning: write might change good version of %s\n", name)
		return nil
	}
	// The addressed range streams straight out of the buffer; WriteRange
	// restores the file's original line endings on the way.
	_, statErr := os.Stat(name)
	isNew := os.IsNotExist(statErr)
	if err := file.WriteAtomic(name, func(w io.Writer) error {
		return f.WriteRange(w, dot.Start, dot.End)
	}); err != nil {
		return fmt.Errorf("w: %w", err)
	}
	if name == f.Name() {
		f.MarkClean()
		f.NoteWritten()
	}
	s.dprint("%s: ", name)
	if isNew {
		s.dprint("(new file) ")
	}
	if dot.Len() > 0 && f.Buffer().RuneAt(dot.End-1) != '\n' {
		s.dprint("?warning: last char not newline\n")
	}
	s.dprint("#%d\n", dot.Len())
	return nil
}

// execF renames the file. It is undoable: sam's f_cmd routes through
// getname(f, s, TRUE), which logs a Filename record, so `f newname` is a step
// `u` takes back. A bare `f` only reports the name and logs nothing.
func (s *Session) execF(f *file.File, name string, ctx *execCtx) error {
	name = file.ExpandPath(strings.TrimSpace(name))
	if name != "" {
		ctx.pending(f).renamed = true
		f.SetName(name)
		s.sortFiles(f)
	}
	s.dprint("%s\n", f.MenuLine(f == s.Current, false))
	return nil
}

// execCd changes the working directory and rewrites the open files' names so
// they still point where they did. Sam does the same (sam/sam.c cd): a relative
// name is made absolute against the directory being left, and a name under the
// new directory is shortened to be relative to it.
//
// Without the rewrite, `cd` would quietly repoint every relative name at a
// different file — which matters most for w.
func (s *Session) execCd(arg string) error {
	dir := strings.TrimSpace(arg)
	if dir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cd: %w", err)
		}
		dir = h
	}
	dir = file.ExpandPath(dir)
	old, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cd: %w", err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("cd: %w", err)
	}
	s.dprint("!\n")

	now, err := os.Getwd()
	if err != nil {
		return nil
	}
	for _, f := range s.Files {
		name := f.Name()
		if name == "" {
			continue
		}
		abs := name
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(old, abs)
		}
		if rel, err := filepath.Rel(now, abs); err == nil && !strings.HasPrefix(rel, "..") {
			f.SetName(rel)
		} else {
			f.SetName(abs)
		}
	}
	s.sortFiles(nil)
	return nil
}
