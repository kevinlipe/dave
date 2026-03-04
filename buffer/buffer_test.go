package buffer

import (
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func newBuf(s string) *Buffer { return NewFromString(s) }

func checkContents(t *testing.T, b *Buffer, want string) {
	t.Helper()
	got := b.String()
	if got != want {
		t.Errorf("contents:\n  got  %q\n  want %q", got, want)
	}
}

// ---- Insert -----------------------------------------------------------------

func TestInsertAtStart(t *testing.T) {
	b := newBuf("world")
	b.Insert(0, "hello ")
	checkContents(t, b, "hello world")
}

func TestInsertAtEnd(t *testing.T) {
	b := newBuf("hello")
	b.Insert(5, " world")
	checkContents(t, b, "hello world")
}

func TestInsertAtMiddle(t *testing.T) {
	b := newBuf("helo")
	b.Insert(3, "l")
	checkContents(t, b, "hello")
}

func TestInsertEmpty(t *testing.T) {
	b := newBuf("hello")
	b.Insert(2, "")
	checkContents(t, b, "hello")
}

func TestInsertMultibyte(t *testing.T) {
	// "café" — 'é' is 2 bytes (U+00E9)
	b := newBuf("caf")
	b.Insert(3, "é")
	checkContents(t, b, "café")
	if b.Len() != 4 {
		t.Errorf("Len() = %d, want 4", b.Len())
	}
}

func TestInsertEmDash(t *testing.T) {
	// em-dash is 3 bytes (U+2014)
	b := newBuf("ab")
	b.Insert(1, "—")
	checkContents(t, b, "a—b")
	if b.Len() != 3 {
		t.Errorf("Len() = %d, want 3", b.Len())
	}
}

func TestInsertMultipleRound(t *testing.T) {
	b := New()
	b.Insert(0, "c")
	b.Insert(0, "a")
	b.Insert(1, "b")
	checkContents(t, b, "abc")
}

// ---- Delete -----------------------------------------------------------------

func TestDeleteAtStart(t *testing.T) {
	b := newBuf("hello world")
	b.Delete(0, 6)
	checkContents(t, b, "world")
}

func TestDeleteAtEnd(t *testing.T) {
	b := newBuf("hello world")
	b.Delete(5, 6)
	checkContents(t, b, "hello")
}

func TestDeleteMiddle(t *testing.T) {
	b := newBuf("hello world")
	b.Delete(5, 1)
	checkContents(t, b, "helloworld")
}

func TestDeleteEntireBuffer(t *testing.T) {
	b := newBuf("hello")
	b.Delete(0, 5)
	checkContents(t, b, "")
	if b.Len() != 0 {
		t.Errorf("Len() = %d, want 0", b.Len())
	}
}

func TestDeleteZeroLength(t *testing.T) {
	b := newBuf("hello")
	b.Delete(2, 0)
	checkContents(t, b, "hello")
}

func TestDeleteMultibyte(t *testing.T) {
	// "café" — deleting 'é' (position 3, 1 codepoint)
	b := newBuf("café")
	b.Delete(3, 1)
	checkContents(t, b, "caf")
	if b.Len() != 3 {
		t.Errorf("Len() = %d, want 3", b.Len())
	}
}

func TestDeleteAcrossMultibyte(t *testing.T) {
	// "a—b" — em-dash at position 1; delete it
	b := newBuf("a—b")
	b.Delete(1, 1)
	checkContents(t, b, "ab")
	if b.Len() != 2 {
		t.Errorf("Len() = %d, want 2", b.Len())
	}
}

// ---- ReadAt -----------------------------------------------------------------

func TestReadAtArbitraryOffsets(t *testing.T) {
	b := newBuf("hello world")
	got := b.ReadAt(6, 5)
	if got != "world" {
		t.Errorf("ReadAt(6,5) = %q, want %q", got, "world")
	}
}

func TestReadAtStart(t *testing.T) {
	b := newBuf("hello")
	got := b.ReadAt(0, 5)
	if got != "hello" {
		t.Errorf("ReadAt(0,5) = %q, want %q", got, "hello")
	}
}

func TestReadAtEnd(t *testing.T) {
	b := newBuf("hello world")
	n := b.Len()
	got := b.ReadAt(n, 0)
	if got != "" {
		t.Errorf("ReadAt(n,0) = %q, want empty", got)
	}
}

func TestReadAtEmpty(t *testing.T) {
	b := newBuf("hello")
	got := b.ReadAt(2, 0)
	if got != "" {
		t.Errorf("ReadAt(2,0) = %q, want empty", got)
	}
}

func TestReadAtAcrossGap(t *testing.T) {
	// Force the gap to be in the middle by inserting then reading.
	b := newBuf("hello")
	b.Insert(3, "XY") // gap moves to after 'XY'
	// Now buffer is "helXYlo", gap just after position 5
	b.moveGap(0) // force gap to start — so pre-gap is empty, post-gap has everything
	got := b.ReadAt(3, 2)
	if got != "XY" {
		t.Errorf("ReadAt across gap = %q, want %q", got, "XY")
	}
}

func TestReadAtMultibyte(t *testing.T) {
	b := newBuf("café")
	if b.ReadAt(3, 1) != "é" {
		t.Errorf("ReadAt(3,1) on café = %q, want %q", b.ReadAt(3, 1), "é")
	}
	if b.ReadAt(0, 4) != "café" {
		t.Errorf("ReadAt(0,4) on café = %q", b.ReadAt(0, 4))
	}
}

// ---- Len and String ---------------------------------------------------------

func TestLen(t *testing.T) {
	cases := []struct {
		s string
		n int
	}{
		{"", 0},
		{"hello", 5},
		{"café", 4},   // 4 codepoints, 5 bytes
		{"a—b", 3},   // 3 codepoints, 5 bytes
		{"😀", 1},
	}
	for _, c := range cases {
		b := newBuf(c.s)
		if b.Len() != c.n {
			t.Errorf("Len(%q) = %d, want %d", c.s, b.Len(), c.n)
		}
	}
}

func TestString(t *testing.T) {
	s := "hello, 世界"
	b := newBuf(s)
	if b.String() != s {
		t.Errorf("String() = %q, want %q", b.String(), s)
	}
}

// ---- Transcript / two-pass evaluation ----------------------------------------

func TestTranscriptApply(t *testing.T) {
	b := newBuf("hello world")
	tr := NewTranscript()
	// Replace "world" (pos 6, len 5) with "Dave"
	if err := tr.Add(Change{Pos: 6, Del: 5, Text: "Dave"}); err != nil {
		t.Fatal(err)
	}
	_, err := tr.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	checkContents(t, b, "hello Dave")
}

func TestTranscriptMonotonicOrder(t *testing.T) {
	tr := NewTranscript()
	tr.Add(Change{Pos: 5, Del: 0, Text: "x"})
	err := tr.Add(Change{Pos: 3, Del: 0, Text: "y"})
	if err == nil {
		t.Error("expected error for out-of-order change")
	}
}

func TestTranscriptTwoChanges(t *testing.T) {
	// "The cat sat" -> "The dog lay"
	b := newBuf("The cat sat")
	tr := NewTranscript()
	tr.Add(Change{Pos: 4, Del: 3, Text: "dog"}) // cat -> dog
	tr.Add(Change{Pos: 8, Del: 3, Text: "lay"}) // sat -> lay
	_, err := tr.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	checkContents(t, b, "The dog lay")
}

func TestTranscriptChangesOriginalCoords(t *testing.T) {
	// Both changes reference ORIGINAL positions, not post-change positions.
	// Original: "abcde" (positions 0-4)
	// Change 1: insert "XX" at 1 → "aXXbcde" (but positions still in original)
	// Change 2: insert "YY" at 3 → position 3 in original = 'd'
	b := newBuf("abcde")
	tr := NewTranscript()
	tr.Add(Change{Pos: 1, Del: 0, Text: "XX"})
	tr.Add(Change{Pos: 3, Del: 0, Text: "YY"})
	_, err := tr.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	checkContents(t, b, "aXXbcYYde")
}

func TestTranscriptInverse(t *testing.T) {
	b := newBuf("hello world")
	tr := NewTranscript()
	tr.Add(Change{Pos: 6, Del: 5, Text: "Dave"})
	inv, err := tr.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	checkContents(t, b, "hello Dave")

	// Apply the inverse to undo.
	_, err = inv.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	checkContents(t, b, "hello world")
}

func TestTranscriptRollback(t *testing.T) {
	// A sealed (applied) transcript cannot accept more changes.
	b := newBuf("hello")
	tr := NewTranscript()
	tr.Apply(b)
	err := tr.Add(Change{Pos: 0, Del: 0, Text: "x"})
	if err == nil {
		t.Error("expected error when adding to sealed transcript")
	}
}

// ---- UTF-8 codepoint correctness --------------------------------------------

func TestCodepointVsByteOffset(t *testing.T) {
	// "café": c=0, a=1, f=2, é=3 (2 bytes)
	// Inserting at codepoint position 3 should be before 'é', not in the middle of its bytes.
	b := newBuf("café")
	b.Insert(3, "X")
	checkContents(t, b, "cafXé")
}

func TestDeleteCodepointVsByte(t *testing.T) {
	// "café": deleting at codepoint 2 for 1 should remove 'f', not a byte of 'é'.
	b := newBuf("café")
	b.Delete(2, 1)
	checkContents(t, b, "caé")
}

func TestSmartQuotes(t *testing.T) {
	// Smart quotes: U+201C, U+201D (3 bytes each)
	s := "\u201Chello\u201D"
	b := newBuf(s)
	if b.Len() != 7 {
		t.Errorf("Len() = %d, want 7", b.Len())
	}
	// Delete the opening quote (position 0)
	b.Delete(0, 1)
	checkContents(t, b, "hello\u201D")
}

// ---- Large insert (gap expansion) -------------------------------------------

func TestLargeInsert(t *testing.T) {
	b := New()
	big := strings.Repeat("x", 100000)
	b.Insert(0, big)
	if b.Len() != 100000 {
		t.Errorf("Len() = %d after large insert", b.Len())
	}
	checkContents(t, b, big)
}
