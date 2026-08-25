package file

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDetectCRLF(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want bool
		what string
	}{
		{"", false, "empty"},
		{"no newline at all", false, "no line ending to go on"},
		{"a\nb\n", false, "LF"},
		{"a\r\nb\r\n", true, "CRLF"},
		{"\r\n", true, "a single CRLF line ending"},
		{"\n", false, "a single LF line ending"},
		{"a\r\nb\nc\n", true, "mixed, CRLF first"},
		{"a\nb\r\nc\r\n", false, "mixed, LF first — the first line ending decides"},
		{"a\rb\n", false, "a bare \\r that is not a line ending"},
	} {
		if got := detectCRLF(c.raw); got != c.want {
			t.Errorf("detectCRLF(%q) = %v, want %v — %s", c.raw, got, c.want, c.what)
		}
	}
}

// The old rule asked whether \r\n appeared anywhere, so one stray CRLF line
// in an LF file re-encoded the whole file on the next write. This is the case
// that behaviour turned into a whole-file diff.
func TestDecodeContentDoesNotLetOneCRLFLineConvertAnLFFile(t *testing.T) {
	f := New()
	raw := "one\ntwo\nthree\r\nfour\n"
	got := f.DecodeContent(raw)
	if f.CRLF() {
		t.Error("an LF file with one stray CRLF line was flagged as CRLF")
	}
	// Not being a CRLF file, its content is passed through untouched — the
	// stray \r stays a character, which is the Plan 9 answer for it.
	if got != raw {
		t.Errorf("content = %q, want it unchanged (%q)", got, raw)
	}
}

func TestDecodeContentStripsCRLF(t *testing.T) {
	f := New()
	if got, want := f.DecodeContent("a\r\nb\r\n"), "a\nb\n"; got != want {
		t.Errorf("DecodeContent = %q, want %q", got, want)
	}
	if !f.CRLF() {
		t.Error("CRLF() = false, want true")
	}
}

// ---- crlfWriter -------------------------------------------------------------

func TestCRLFWriter(t *testing.T) {
	for _, c := range []struct{ in, want, what string }{
		{"", "", "nothing"},
		{"abc", "abc", "no line endings"},
		{"a\nb\n", "a\r\nb\r\n", "every \\n is encoded"},
		{"\n", "\r\n", "a lone newline"},
		{"\n\n\n", "\r\n\r\n\r\n", "consecutive newlines"},
		{"a\n", "a\r\n", "trailing newline"},
		{"\na", "\r\na", "leading newline"},
	} {
		var out bytes.Buffer
		w := crlfWriter{&out}
		n, err := w.Write([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if out.String() != c.want {
			t.Errorf("%s: got %q, want %q", c.what, out.String(), c.want)
		}
		// io.Writer's contract: n counts bytes of p consumed, not emitted.
		if n != len(c.in) {
			t.Errorf("%s: n = %d, want %d (input consumed, not output written)",
				c.what, n, len(c.in))
		}
	}
}

// The writer sees whatever chunks the buffer hands it, so a line ending can
// arrive split across two calls — or one call can end exactly on a \n.
func TestCRLFWriterAcrossWriteCalls(t *testing.T) {
	var out bytes.Buffer
	w := crlfWriter{&out}
	for _, chunk := range []string{"one\n", "two", "\nthree\n", "", "four"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("writing %q: %v", chunk, err)
		}
	}
	if want := "one\r\ntwo\r\nthree\r\nfour"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

// crlfWriter and detectCRLF both search for a single byte, which is safe only
// because UTF-8 is self-synchronising: every byte of a multibyte sequence has
// its high bit set, so 0x0A and 0x0D can never occur inside one. These pin
// that — including runes like ␊ (U+240A, "symbol for line feed", E2 90 8A)
// that are named after the byte being searched for.
func TestCRLFWriterIsUTF8Safe(t *testing.T) {
	for _, in := range []string{
		"héllo wörld\n",
		"aé€😀\nb\n",
		"␊\n␍\n", // runes named after LF and CR
		"日本語\nテキスト\n",
		"\u00e9\n\u0301\n", // é and a lone combining acute
	} {
		var out bytes.Buffer
		if _, err := (crlfWriter{&out}).Write([]byte(in)); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		want := strings.ReplaceAll(in, "\n", "\r\n")
		if out.String() != want {
			t.Errorf("got % x, want % x", out.Bytes(), want)
		}
		if !utf8.Valid(out.Bytes()) {
			t.Errorf("%q: output is not valid UTF-8: % x", in, out.Bytes())
		}
		// The rune count must be unchanged apart from the \r characters.
		if got, want := utf8.RuneCountInString(out.String()),
			utf8.RuneCountInString(in)+strings.Count(in, "\n"); got != want {
			t.Errorf("%q: %d runes out, want %d", in, got, want)
		}
	}
}

// A multibyte rune landing on a chunk boundary must not confuse the scan.
func TestCRLFWriterSplitsMultibyteRuneAcrossWrites(t *testing.T) {
	const text = "é😀\n日本\n"
	raw := []byte(text)
	for split := 0; split <= len(raw); split++ {
		var out bytes.Buffer
		w := crlfWriter{&out}
		if _, err := w.Write(raw[:split]); err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if _, err := w.Write(raw[split:]); err != nil {
			t.Fatalf("split %d: %v", split, err)
		}
		if want := strings.ReplaceAll(text, "\n", "\r\n"); out.String() != want {
			t.Errorf("split at byte %d: got % x, want % x", split, out.Bytes(), want)
		}
	}
}

func TestDetectCRLFIsUTF8Safe(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want bool
	}{
		{"héllo\r\nwörld\r\n", true},
		{"héllo\nwörld\n", false},
		{"日本語\r\n", true},
		{"😀\n", false},
		{"␊\r\n", true}, // a rune named after LF, then a real CRLF
		{"␊\n", false},  // a rune named after LF, then a real LF
	} {
		if got := detectCRLF(c.raw); got != c.want {
			t.Errorf("detectCRLF(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// ---- the boundary as a whole ------------------------------------------------

// A CRLF file's content is LF inside the buffer and CRLF again on the way
// out; nothing between those two points has to know.
func TestWriteContentRestoresLineEndings(t *testing.T) {
	for _, c := range []struct{ raw, want, what string }{
		{"a\r\nb\r\n", "a\r\nb\r\n", "CRLF round trips"},
		{"a\nb\n", "a\nb\n", "LF is left alone"},
	} {
		f := New()
		f.Buffer().Insert(0, f.DecodeContent(c.raw))
		if got := f.Buffer().String(); strings.Contains(got, "\r") {
			t.Errorf("%s: buffer holds %q — it must be LF-only", c.what, got)
		}
		var out bytes.Buffer
		if err := f.WriteContent(&out); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if out.String() != c.want {
			t.Errorf("%s: wrote %q, want %q", c.what, out.String(), c.want)
		}
	}
}

func TestWriteRangeRestoresLineEndings(t *testing.T) {
	f := New()
	f.Buffer().Insert(0, f.DecodeContent("one\r\ntwo\r\nthree\r\n"))
	var out bytes.Buffer
	// "two\n" — codepoints 4..8 of the decoded, LF-only content.
	if err := f.WriteRange(&out, 4, 8); err != nil {
		t.Fatalf("WriteRange: %v", err)
	}
	if want := "two\r\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

// Save and WriteContent must agree, since they are now the same path.
func TestSaveRoundTripsCRLF(t *testing.T) {
	path := t.TempDir() + "/crlf.txt"
	if err := WriteAtomic(path, writeString("a\r\nb\r\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	f := NewNamed(path)
	if err := f.EnsureLoaded(); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	f.Buffer().Insert(0, "x")
	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := readFile(t, path)
	if want := "xa\r\nb\r\n"; got != want {
		t.Errorf("saved %q, want %q", got, want)
	}
}

// readFile is a small helper so the round-trip tests read as one thought.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// The full boundary, on text that is not ASCII: decode, edit at a codepoint
// offset, encode. Codepoint offsets must still address runes, not bytes.
func TestBoundaryRoundTripsUnicode(t *testing.T) {
	const raw = "héllo wörld\r\n日本語\r\n😀\r\n"
	f := New()
	f.Buffer().Insert(0, f.DecodeContent(raw))

	// Rune 6 is 'w'; inserting there proves offsets are codepoints. A byte
	// offset of 6 would land inside "wörld" and corrupt it.
	f.Buffer().Insert(6, "W")

	var out bytes.Buffer
	if err := f.WriteContent(&out); err != nil {
		t.Fatalf("WriteContent: %v", err)
	}
	if want := "héllo Wwörld\r\n日本語\r\n😀\r\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	if !utf8.Valid(out.Bytes()) {
		t.Errorf("output is not valid UTF-8: % x", out.Bytes())
	}
}
