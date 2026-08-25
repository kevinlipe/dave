// Package file implements Dave's file model: a named buffer with a modified
// bit, a dot (current selection), lazy disk loading, and menu line generation.
//
// The file list (all open files in a session) is managed externally by the
// cmd package; this package defines only the File type.
package file

import (
	"bufio"
	"dave/buffer"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File represents an open file in Dave's buffer list.
type File struct {
	buf  *buffer.Buffer
	name string       // associated filename (may be empty for new buffers)
	dot  buffer.Range // current selection (codepoint offsets)
	mark buffer.Range // 'k' mark position

	// Dirtiness is a position in the edit history, not a bit: version is
	// where the buffer is now, cleanVersion where it was at the last read or
	// write, high the last version issued. Undo restores an old version and a
	// later edit issues a fresh higher one, so the comparison holds for any
	// path through the history. A stored bool cannot: a write changes what
	// clean means for every point, and a bool recorded before it does not
	// know. Sam calls these seq and cleanseq (sam/file.c:59).
	version      int
	cleanVersion int
	high         int

	loaded bool // true if contents have been read from disk
	crlf   bool // true if the file uses CRLF line endings on disk

	// justLoaded is set when EnsureLoaded actually read from disk, for the
	// caller to report. Sam's load() prints the file's menu line before
	// reading, and it is called on demand — by a command reaching a file that
	// has not been read, and by a `"pat"` address selecting one.
	justLoaded bool

	// nulls is set when the last read dropped NUL bytes, for the caller to
	// report once. Sam elides them and warns "null characters elided"
	// (sam/io.c readio); a NUL in the buffer would reach everything downstream.
	nulls bool

	// diskTime is the file's modification time when it was last read or
	// written. Sam keeps the same record (plus dev and qid) so a write can tell
	// that the file changed underneath it — see WriteStale.
	diskTime time.Time

	// closeok is sam's second-chance latch on D (sam/sam.c:237): the first D of
	// a modified file refuses and sets it, the second goes through. Kept in
	// step with dirtiness, so editing again earns a fresh refusal.
	closeok bool
}

// New creates a new, empty, unnamed File.
func New() *File {
	return &File{buf: buffer.New(), loaded: true}
}

// NewNamed creates a new File with the given name. Contents are NOT loaded
// from disk yet (lazy loading). If the file doesn't exist on disk, the buffer
// remains empty and loaded is set to true (treat as new file).
func NewNamed(name string) *File {
	return &File{
		buf:  buffer.New(),
		name: name,
	}
}

// CRLF reports whether the file uses CRLF line endings on disk.
func (f *File) CRLF() bool { return f.crlf }

// DecodeContent strips \r from CRLF content and records the line-ending style.
func (f *File) DecodeContent(raw string) string {
	f.crlf = detectCRLF(raw)
	if f.crlf {
		raw = strings.ReplaceAll(raw, "\r\n", "\n")
	}
	if strings.IndexByte(raw, 0) >= 0 {
		f.nulls = true
		raw = strings.ReplaceAll(raw, "\x00", "")
	}
	return raw
}

// TookNulls reports whether the last read dropped NUL bytes, and clears the
// flag so the warning is given once.
func (f *File) TookNulls() bool {
	n := f.nulls
	f.nulls = false
	return n
}

// noteDiskTime records the file's modification time as of now, which is what a
// later write compares against.
func (f *File) noteDiskTime() {
	if fi, err := os.Stat(f.name); err == nil {
		f.diskTime = fi.ModTime()
	} else {
		f.diskTime = time.Time{}
	}
}

// NoteWritten records that the file on disk is now what the buffer holds.
func (f *File) NoteWritten() { f.noteDiskTime() }

// WriteStale reports whether the file on disk has changed since dave last read
// or wrote it, and records the new time so that asking again says no.
//
// Sam refuses the write in that case and warns "write might change good version
// of" (sam/io.c writef), updating its own record as it goes so a second w goes
// through — the same second chance D and q give. Sam also compares the device
// and inode; dave compares the modification time alone, which is portable and
// catches the case that matters, someone else having written the file.
func (f *File) WriteStale() bool {
	fi, err := os.Stat(f.name)
	if err != nil {
		return false // gone, or never there: writing creates it
	}
	stale := !f.diskTime.IsZero() && fi.ModTime().After(f.diskTime)
	f.diskTime = fi.ModTime()
	return stale
}

// EnsureLoaded reads the file from disk if it hasn't been loaded yet.
// If the file doesn't exist, the buffer remains empty and the file is
// treated as new (no error).
func (f *File) EnsureLoaded() error {
	if f.loaded {
		return nil
	}
	f.loaded = true
	if f.name == "" {
		return nil
	}
	data, err := os.ReadFile(f.name)
	if os.IsNotExist(err) {
		return nil // new file
	}
	if err != nil {
		return fmt.Errorf("file: reading %q: %w", f.name, err)
	}
	f.buf = buffer.NewFromString(f.DecodeContent(string(data)))
	f.MarkClean()
	f.noteDiskTime()
	f.justLoaded = true
	return nil
}

// JustLoaded reports whether the last EnsureLoaded read from disk, and clears
// the flag so the load is announced once.
func (f *File) JustLoaded() bool {
	l := f.justLoaded
	f.justLoaded = false
	return l
}

// Buffer returns the underlying buffer. The caller must call EnsureLoaded first.
func (f *File) Buffer() *buffer.Buffer { return f.buf }

// Name returns the file's associated name.
func (f *File) Name() string { return f.name }

// SetName sets the file's associated name.
func (f *File) SetName(name string) { f.name = name }

// Modified reports whether the file has unsaved changes.
func (f *File) Modified() bool { return f.version != f.cleanVersion }

// MarkChanged advances the file to a new version. Called once per command that
// changed it.
func (f *File) MarkChanged() {
	f.high++
	f.version = f.high
	f.closeok = false
}

// MarkClean records that the buffer now matches disk.
func (f *File) MarkClean() {
	f.cleanVersion = f.version
	f.closeok = true
}

// Version returns the file's position in its edit history.
func (f *File) Version() int { return f.version }

// SetVersion moves the file to a version it held before; undo and redo use it.
func (f *File) SetVersion(v int) {
	f.version = v
	f.closeok = !f.Modified()
}

// CloseOK reports whether this file may be dropped without confirmation.
func (f *File) CloseOK() bool { return f.closeok }

// SetCloseOK records that a D on this file has already been refused once, so
// the next one succeeds.
func (f *File) SetCloseOK() { f.closeok = true }

// Dot returns the current selection.
func (f *File) Dot() buffer.Range { return f.dot }

// SetDot sets the current selection.
func (f *File) SetDot(r buffer.Range) { f.dot = r }

// Mark returns the marked position (set by 'k' command).
func (f *File) Mark() buffer.Range { return f.mark }

// SetMark sets the mark to the current dot.
func (f *File) SetMark() { f.mark = f.dot }

// SetMarkRange sets the mark to an arbitrary range. Undo needs it: sam logs
// the mark alongside dot and restores both, so a `k` that a command overwrote
// comes back with that command (sam/file.c fileunsetmark).
func (f *File) SetMarkRange(r buffer.Range) { f.mark = r }

// Len returns the number of codepoints in the buffer.
// Calls EnsureLoaded implicitly (panics on error — caller should pre-load).
func (f *File) Len() int { return f.buf.Len() }

// String returns the entire file contents as a string.
func (f *File) String() string { return f.buf.String() }

// MenuLine returns the file's single-line status string for the buffer menu.
//
// Format: <modified> <current> <space> <name>
//
// The two status characters are set by the caller via parameters since
// "current" is a session-level concept, and window-open status belongs
// to the rendering layer.
//
// For headless (-d) mode, pass isCurrent=false.
func (f *File) MenuLine(isCurrent, isInPane bool) string {
	// Three independent columns, as sam has them
	// (`dprint("%c%c%c %s\n", " '"[f->mod], "-+"[f->rasp!=0], " ."[f==curfile], …)`).
	// Dave used to fold "modified" and "has a window" into one column, so a
	// modified file open in a pane reported only that it was modified — and
	// since X, Y and the "pat" address match against this line, there was no
	// way to select on having a window either.
	mod, win, cur := ' ', '-', ' '
	if f.Modified() {
		mod = '\''
	}
	if isInPane {
		win = '+'
	}
	if isCurrent {
		cur = '.'
	}
	name := f.name
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%c%c%c %s", mod, win, cur, name)
}

// WriteContent writes the entire file contents to w in the file's own line
// endings, streaming straight out of the buffer without copying it.
//
// It is not io.WriterTo: File has two write paths, this one and WriteRange,
// and they are worth naming for what they write.
func (f *File) WriteContent(w io.Writer) error {
	_, err := f.buf.WriteTo(f.writer(w))
	return err
}

// WriteRange writes the contents of [lo, hi) codepoints to w, in the file's
// own line endings.
func (f *File) WriteRange(w io.Writer, lo, hi int) error {
	_, err := f.buf.WriteRangeTo(f.writer(w), lo, hi-lo)
	return err
}

// Save writes the file to disk using its associated name.
func (f *File) Save() error {
	if f.name == "" {
		return fmt.Errorf("file: no filename to save to")
	}
	if err := WriteAtomic(f.name, f.WriteContent); err != nil {
		return err
	}
	f.MarkClean()
	f.noteDiskTime()
	return nil
}

// ExpandPath resolves a leading `~` or `~/` to the user's home directory.
// Only these two forms are recognized — `~user` is not supported (differs
// per platform, and Sam doesn't do it either). If the home directory can't
// be determined, the path is returned unchanged rather than erroring.
func ExpandPath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	if path != "~" && path[:2] != "~/" {
		return path // ~user or similar — leave alone
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// WriteAtomic writes to path via a temp file and rename, creating parent
// directories as needed. The content is produced by write, which is handed a
// buffered writer — callers stream into it rather than building the whole
// file in memory first. Either the destination is fully replaced or
// it is unchanged; a crash mid-write cannot leave a truncated file at path.
//
// The file is fsynced before the rename, and the containing directory is
// fsynced after, so the update survives a power loss on POSIX filesystems.
// Directory fsync is best-effort — Windows returns an error on directory
// handles, which we ignore since the file itself is already durable.
func WriteAtomic(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("file: creating directory %q: %w", dir, err)
	}
	tmp := path + ".dave-tmp"
	fh, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("file: creating temp file: %w", err)
	}
	// Buffered: the CRLF encoder emits a write per line, and the gap buffer
	// two per file. Neither should be a syscall each.
	bw := bufio.NewWriter(fh)
	if err := write(bw); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("file: writing %q: %w", path, err)
	}
	if err := bw.Flush(); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("file: writing %q: %w", path, err)
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("file: syncing %q: %w", path, err)
	}
	if err := fh.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("file: renaming to %q: %w", path, err)
	}
	// Fsync the directory so the rename metadata reaches disk. Best-effort:
	// on Windows, directory handles reject FlushFileBuffers, and on some
	// filesystems this returns errors we can safely ignore — the file is
	// already synced, so the data is durable regardless.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// SaveAs writes the file to disk under name and updates the file's name.
func (f *File) SaveAs(name string) error {
	f.name = name
	return f.Save()
}

// Replace replaces all file contents with data read from the named path.
// The dot is set to (0,0) and the modified bit is cleared.
func (f *File) Replace(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("file: reading %q: %w", path, err)
	}
	f.buf = buffer.NewFromString(f.DecodeContent(string(data)))
	f.dot = buffer.Range{}
	f.MarkClean()
	f.loaded = true
	return nil
}

// ReadFromPath reads the contents of path and returns it as a string.
// This is used by the 'r' command to insert file contents at dot.
func ReadFromPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file: reading %q: %w", path, err)
	}
	return string(data), nil
}
