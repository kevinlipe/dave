// Package file implements Dave's file model: a named buffer with a modified
// bit, a dot (current selection), lazy disk loading, and menu line generation.
//
// The file list (all open files in a session) is managed externally by the
// cmd package; this package defines only the File type.
package file

import (
	"dave/buffer"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// File represents an open file in Dave's buffer list.
type File struct {
	buf      *buffer.Buffer
	name     string       // associated filename (may be empty for new buffers)
	modified bool         // true if contents differ from disk
	dot      buffer.Range // current selection (codepoint offsets)
	mark     buffer.Range // 'k' mark position
	loaded   bool         // true if contents have been read from disk
	crlf     bool         // true if the file uses CRLF line endings on disk
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
	if strings.Contains(raw, "\r\n") {
		f.crlf = true
		return strings.ReplaceAll(raw, "\r\n", "\n")
	}
	f.crlf = false
	return raw
}

// EncodeContent restores \r\n line endings if the file originally used CRLF.
func (f *File) EncodeContent(s string) string {
	if f.crlf {
		return strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
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
	f.modified = false
	return nil
}

// Buffer returns the underlying buffer. The caller must call EnsureLoaded first.
func (f *File) Buffer() *buffer.Buffer { return f.buf }

// Name returns the file's associated name.
func (f *File) Name() string { return f.name }

// SetName sets the file's associated name.
func (f *File) SetName(name string) { f.name = name }

// Modified reports whether the file has unsaved changes.
func (f *File) Modified() bool { return f.modified }

// SetModified sets the modified bit.
func (f *File) SetModified(v bool) { f.modified = v }

// Dot returns the current selection.
func (f *File) Dot() buffer.Range { return f.dot }

// SetDot sets the current selection.
func (f *File) SetDot(r buffer.Range) { f.dot = r }

// Mark returns the marked position (set by 'k' command).
func (f *File) Mark() buffer.Range { return f.mark }

// SetMark sets the mark to the current dot.
func (f *File) SetMark() { f.mark = f.dot }

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
	mod := '-'
	if f.modified {
		mod = '\''
	} else if isInPane {
		mod = '+'
	}
	cur := '-'
	if isCurrent {
		cur = '.'
	}
	name := f.name
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%c%c %s", mod, cur, name)
}

// WriteTo writes the entire file contents to w.
func (f *File) WriteTo(w io.Writer) error {
	_, err := io.WriteString(w, f.buf.String())
	return err
}

// WriteRange writes the contents of [lo, hi) codepoints to w.
func (f *File) WriteRange(w io.Writer, lo, hi int) error {
	s := f.buf.ReadAt(lo, hi-lo)
	_, err := io.WriteString(w, s)
	return err
}

// Save writes the file to disk using its associated name.
func (f *File) Save() error {
	if f.name == "" {
		return fmt.Errorf("file: no filename to save to")
	}
	if err := WriteAtomic(f.name, f.EncodeContent(f.buf.String())); err != nil {
		return err
	}
	f.modified = false
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

// WriteAtomic writes content to path via a temp file and rename, creating
// parent directories as needed. Either the destination is fully replaced or
// it is unchanged; a crash mid-write cannot leave a truncated file at path.
//
// The file is fsynced before the rename, and the containing directory is
// fsynced after, so the update survives a power loss on POSIX filesystems.
// Directory fsync is best-effort — Windows returns an error on directory
// handles, which we ignore since the file itself is already durable.
func WriteAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("file: creating directory %q: %w", dir, err)
	}
	tmp := path + ".dave-tmp"
	fh, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("file: creating temp file: %w", err)
	}
	if _, err := io.WriteString(fh, content); err != nil {
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
	f.modified = false
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
