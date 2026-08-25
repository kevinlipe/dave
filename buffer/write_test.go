package buffer

import (
	"bytes"
	"errors"
	"testing"
	"unicode/utf8"
)

// withGapAt returns a buffer holding text whose gap sits at codepoint gapAt,
// so that the two-segment write paths can be exercised on both sides of it
// and across it. Insert moves the gap to the edit, then deleting the inserted
// rune leaves it there.
func withGapAt(t *testing.T, text string, gapAt int) *Buffer {
	t.Helper()
	b := NewFromString(text)
	b.Insert(gapAt, "¶")
	b.Delete(gapAt, 1)
	if got := b.String(); got != text {
		t.Fatalf("setup: buffer = %q, want %q", got, text)
	}
	return b
}

func TestWriteToMatchesString(t *testing.T) {
	const text = "the quick\nbrown fox\njumps\n"
	for _, gapAt := range []int{0, 1, 9, 13, len([]rune(text))} {
		b := withGapAt(t, text, gapAt)
		var w bytes.Buffer
		n, err := b.WriteTo(&w)
		if err != nil {
			t.Fatalf("gap at %d: WriteTo: %v", gapAt, err)
		}
		if w.String() != text {
			t.Errorf("gap at %d: wrote %q, want %q", gapAt, w.String(), text)
		}
		if want := int64(len(text)); n != want {
			t.Errorf("gap at %d: wrote %d bytes, want %d", gapAt, n, want)
		}
	}
}

func TestWriteToEmptyBuffer(t *testing.T) {
	var w bytes.Buffer
	n, err := New().WriteTo(&w)
	if err != nil || n != 0 || w.Len() != 0 {
		t.Errorf("empty buffer: wrote %q (%d bytes), err %v", w.String(), n, err)
	}
}

// The gap always begins and ends on a rune boundary, so writing the two
// segments raw cannot split a codepoint. This walks the gap through every
// position of a string of 1-, 2-, 3- and 4-byte runes and demands the bytes
// come back out exactly, which is what would fail if a segment boundary ever
// landed mid-rune.
func TestWriteToNeverSplitsARune(t *testing.T) {
	const text = "aé€😀b\né😀\n"
	for gapAt := 0; gapAt <= len([]rune(text)); gapAt++ {
		b := withGapAt(t, text, gapAt)
		var w bytes.Buffer
		if _, err := b.WriteTo(&w); err != nil {
			t.Fatalf("gap at rune %d: %v", gapAt, err)
		}
		if w.String() != text {
			t.Errorf("gap at rune %d: wrote % x, want % x", gapAt, w.Bytes(), text)
		}
		if !utf8.Valid(w.Bytes()) {
			t.Errorf("gap at rune %d: output is not valid UTF-8: % x", gapAt, w.Bytes())
		}
	}
}

// WriteRangeTo must agree with ReadAt for every range, whichever side of the
// gap the range falls on — including spanning it.
func TestWriteRangeToAgreesWithReadAt(t *testing.T) {
	const text = "héllo wörld\né😀\nsecond line\n"
	runes := len([]rune(text))
	// 1, 2, 8 and 13 sit immediately after a multibyte rune.
	for _, gapAt := range []int{0, 1, 2, 3, 7, 8, 12, 13, 14, runes} {
		b := withGapAt(t, text, gapAt)
		for lo := 0; lo <= runes; lo++ {
			for hi := lo; hi <= runes; hi++ {
				var w bytes.Buffer
				n, err := b.WriteRangeTo(&w, lo, hi-lo)
				if err != nil {
					t.Fatalf("gap %d, [%d,%d): %v", gapAt, lo, hi, err)
				}
				want := b.ReadAt(lo, hi-lo)
				if w.String() != want {
					t.Fatalf("gap %d, [%d,%d): wrote %q, want %q",
						gapAt, lo, hi, w.String(), want)
				}
				if n != int64(len(want)) {
					t.Fatalf("gap %d, [%d,%d): n = %d, want %d",
						gapAt, lo, hi, n, len(want))
				}
			}
		}
	}
}

func TestWriteRangeToClampsLikeReadAt(t *testing.T) {
	b := NewFromString("abc")
	for _, c := range []struct {
		pos, n int
		want   string
	}{
		{0, 99, "abc"}, // past the end
		{-5, 2, ""},    // lo clamps to 0 but hi stays put, so the range empties — as in ReadAt
		{1, 0, ""},     // empty
		{1, -1, ""},    // negative length
		{99, 1, ""},    // start past the end
	} {
		var w bytes.Buffer
		if _, err := b.WriteRangeTo(&w, c.pos, c.n); err != nil {
			t.Fatalf("WriteRangeTo(%d,%d): %v", c.pos, c.n, err)
		}
		if w.String() != c.want {
			t.Errorf("WriteRangeTo(%d,%d) = %q, want %q", c.pos, c.n, w.String(), c.want)
		}
	}
}

// errWriter fails on the nth write, so that an error from either gap segment
// is seen to propagate.
type errWriter struct {
	failOn, n int
}

var errBoom = errors.New("boom")

func (e *errWriter) Write(p []byte) (int, error) {
	e.n++
	if e.n == e.failOn {
		return 0, errBoom
	}
	return len(p), nil
}

func TestWriteToPropagatesErrors(t *testing.T) {
	for _, failOn := range []int{1, 2} {
		b := withGapAt(t, "abcdef", 3)
		if _, err := b.WriteTo(&errWriter{failOn: failOn}); !errors.Is(err, errBoom) {
			t.Errorf("failing write %d: err = %v, want %v", failOn, err, errBoom)
		}
	}
}
