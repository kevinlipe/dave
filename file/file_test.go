package file

import (
	"dave/buffer"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExpandPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", `C:\Users\test`)
	} else {
		t.Setenv("HOME", "/tmp/testhome")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct{ in, want string }{
		{"", ""},
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"~/bin/cleanscan.sh", filepath.Join(home, "bin/cleanscan.sh")},
		{"/abs/path", "/abs/path"},
		{"./rel", "./rel"},
		{"plain.txt", "plain.txt"},
		{"~user", "~user"}, // only ~ or ~/ expands
		{"~otheruser/x", "~otheruser/x"},
		{"foo~bar", "foo~bar"}, // ~ not at start: untouched
	}
	for _, c := range cases {
		if got := ExpandPath(c.in); got != c.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// writeString adapts a plain string to WriteAtomic's streaming signature.
func writeString(s string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	// Nested path — MkdirAll should create sub/dir.
	path := filepath.Join(dir, "sub", "dir", "out.txt")
	if err := WriteAtomic(path, writeString("hello dave")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello dave" {
		t.Errorf("contents = %q, want %q", got, "hello dave")
	}
	// No leftover temp file after a successful write.
	if _, err := os.Stat(path + ".dave-tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should not exist after success; stat err = %v", err)
	}
}

func TestWriteAtomicReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, writeString("replaced")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replaced" {
		t.Errorf("contents = %q, want %q", got, "replaced")
	}
}

func TestExpandPathNoHome(t *testing.T) {
	// If UserHomeDir can't determine home, ExpandPath should leave ~ alone.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "")
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	} else {
		t.Setenv("HOME", "")
	}
	if got := ExpandPath("~/foo"); got != "~/foo" {
		t.Errorf("ExpandPath with no home = %q, want %q", got, "~/foo")
	}
}

func TestNewFile(t *testing.T) {
	f := New()
	if f.Len() != 0 {
		t.Errorf("new file Len() = %d, want 0", f.Len())
	}
	if f.Modified() {
		t.Error("new file should not be modified")
	}
	if f.Name() != "" {
		t.Errorf("new file name = %q, want empty", f.Name())
	}
}

func TestSetName(t *testing.T) {
	f := New()
	f.SetName("foo.txt")
	if f.Name() != "foo.txt" {
		t.Errorf("Name() = %q, want %q", f.Name(), "foo.txt")
	}
}

func TestDot(t *testing.T) {
	f := New()
	r := buffer.Range{Start: 2, End: 5}
	f.SetDot(r)
	if f.Dot() != r {
		t.Errorf("Dot() = %v, want %v", f.Dot(), r)
	}
}

func TestMark(t *testing.T) {
	f := New()
	f.SetDot(buffer.Range{Start: 3, End: 7})
	f.SetMark()
	if f.Mark() != f.Dot() {
		t.Errorf("Mark() = %v, want %v", f.Mark(), f.Dot())
	}
}

func TestModifiedBit(t *testing.T) {
	f := New()
	f.SetModified(true)
	if !f.Modified() {
		t.Error("expected modified = true")
	}
	f.SetModified(false)
	if f.Modified() {
		t.Error("expected modified = false")
	}
}

func TestMenuLineUnmodifiedNotCurrent(t *testing.T) {
	f := NewNamed("foo.txt")
	ml := f.MenuLine(false, false)
	if ml != "-- foo.txt" {
		t.Errorf("MenuLine = %q, want %q", ml, "-- foo.txt")
	}
}

func TestMenuLineModifiedCurrent(t *testing.T) {
	f := NewNamed("foo.txt")
	f.SetModified(true)
	ml := f.MenuLine(true, false)
	if ml != "'. foo.txt" {
		t.Errorf("MenuLine = %q, want %q", ml, "'. foo.txt")
	}
}

func TestMenuLineUnnamed(t *testing.T) {
	f := New()
	ml := f.MenuLine(false, false)
	if ml != "-- (unnamed)" {
		t.Errorf("MenuLine = %q, want %q", ml, "-- (unnamed)")
	}
}

func TestLazyLoading(t *testing.T) {
	// Write a temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := NewNamed(path)
	// Not loaded yet.
	if f.loaded {
		t.Error("should not be loaded yet")
	}
	// After EnsureLoaded, contents should be available.
	if err := f.EnsureLoaded(); err != nil {
		t.Fatal(err)
	}
	if f.String() != "hello" {
		t.Errorf("String() = %q, want %q", f.String(), "hello")
	}
}

func TestLazyLoadNonExistent(t *testing.T) {
	f := NewNamed("/nonexistent/path/file.txt")
	// Should not error — treat as new empty file.
	if err := f.EnsureLoaded(); err != nil {
		t.Errorf("EnsureLoaded for nonexistent file: %v", err)
	}
	if f.Len() != 0 {
		t.Errorf("nonexistent file Len() = %d, want 0", f.Len())
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	f := NewNamed(path)
	f.loaded = true
	f.buf = buffer.NewFromString("hello, dave")

	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if f.Modified() {
		t.Error("after save, modified should be false")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello, dave" {
		t.Errorf("saved content = %q, want %q", data, "hello, dave")
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	os.WriteFile(path, []byte("replaced content"), 0o644)

	f := New()
	f.buf = buffer.NewFromString("original")
	f.SetDot(buffer.Range{Start: 1, End: 3})

	if err := f.Replace(path); err != nil {
		t.Fatal(err)
	}
	if f.String() != "replaced content" {
		t.Errorf("String() = %q, want %q", f.String(), "replaced content")
	}
	if f.Dot().Start != 0 || f.Dot().End != 0 {
		t.Errorf("after Replace, dot should be (0,0), got %v", f.Dot())
	}
	if f.Modified() {
		t.Error("after Replace, modified should be false")
	}
}
