package buffer

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// These tests cover the cached rune counts (preGapRunes, totalRunes) that
// let runeToBytePos avoid scanning from position 0. The counts are
// maintained incrementally, so the failure mode to guard against is drift:
// a count that no longer matches the bytes it describes. checkInvariants
// recomputes both from scratch and compares.

func checkInvariants(t *testing.T, b *Buffer) {
	t.Helper()
	if b.gapStart < 0 || b.gapStart > b.gapEnd || b.gapEnd > len(b.data) {
		t.Fatalf("gap out of range: gapStart=%d gapEnd=%d len(data)=%d",
			b.gapStart, b.gapEnd, len(b.data))
	}
	pre := b.data[:b.gapStart]
	post := b.data[b.gapEnd:]
	if want := utf8.RuneCount(pre); b.preGapRunes != want {
		t.Fatalf("preGapRunes = %d, recomputed %d", b.preGapRunes, want)
	}
	if want := utf8.RuneCount(pre) + utf8.RuneCount(post); b.totalRunes != want {
		t.Fatalf("totalRunes = %d, recomputed %d", b.totalRunes, want)
	}
	// The gap must sit on a rune boundary in both directions, or the
	// counts describe half-decoded runes.
	if len(pre) > 0 {
		if r, _ := utf8.DecodeLastRune(pre); r == utf8.RuneError {
			t.Fatalf("gapStart=%d splits a rune", b.gapStart)
		}
	}
	if len(post) > 0 {
		if r, _ := utf8.DecodeRune(post); r == utf8.RuneError {
			t.Fatalf("gapEnd=%d splits a rune", b.gapEnd)
		}
	}
}

// runeToBytePos must agree with a naive scan for every position, from
// either direction and regardless of where the gap sits.
func TestRuneToBytePosMatchesNaiveScan(t *testing.T) {
	const text = "héllo, wörld — naïve ☃ scan\nsecond line ﬁnis"
	runes := []rune(text)

	// Park the gap at every possible offset in turn, then check every
	// position against a scan of the gap-collapsed content.
	for gapAt := 0; gapAt <= len(runes); gapAt++ {
		b := NewFromString(text)
		b.moveGap(b.runeToBytePos(gapAt))
		checkInvariants(t, b)

		for pos := 0; pos <= len(runes); pos++ {
			want := len(string(runes[:pos]))
			if got := b.runeToBytePos(pos); got != want {
				t.Fatalf("gap at rune %d: runeToBytePos(%d) = %d, want %d",
					gapAt, pos, got, want)
			}
		}
		// Past the end clamps to the end of the content.
		if got, want := b.runeToBytePos(len(runes)+7), b.byteLen(); got != want {
			t.Fatalf("gap at rune %d: runeToBytePos past end = %d, want %d",
				gapAt, got, want)
		}
	}
}

func TestRuneToBytePosNegativePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("runeToBytePos(-1) did not panic")
		}
	}()
	newBuf("hello").runeToBytePos(-1)
}

// ReadAt spans that start pre-gap and end post-gap take the two-segment
// path; the single-segment paths must agree with them.
func TestReadAtEveryRangeAcrossEveryGapPosition(t *testing.T) {
	const text = "abc—déf☃ghi\njkl"
	runes := []rune(text)

	for gapAt := 0; gapAt <= len(runes); gapAt++ {
		b := NewFromString(text)
		b.moveGap(b.runeToBytePos(gapAt))

		for pos := 0; pos <= len(runes); pos++ {
			for n := 0; pos+n <= len(runes); n++ {
				want := string(runes[pos : pos+n])
				if got := b.ReadAt(pos, n); got != want {
					t.Fatalf("gap at %d: ReadAt(%d, %d) = %q, want %q",
						gapAt, pos, n, got, want)
				}
			}
		}
		for pos := 0; pos < len(runes); pos++ {
			if got, want := b.RuneAt(pos), runes[pos]; got != want {
				t.Fatalf("gap at %d: RuneAt(%d) = %q, want %q",
					gapAt, pos, got, want)
			}
		}
	}
}

// Reads that run off the end clamp rather than erroring or over-reading.
func TestReadAtClampsOutOfRange(t *testing.T) {
	b := newBuf("héllo")
	cases := []struct {
		pos, n int
		want   string
	}{
		{0, 99, "héllo"},
		{3, 99, "lo"},
		{5, 3, ""},
		{99, 3, ""},
		{2, 0, ""},
		{2, -1, ""},
		{-2, 4, "hé"}, // negative pos clamps to 0, end stays at pos+n
	}
	for _, c := range cases {
		if got := b.ReadAt(c.pos, c.n); got != c.want {
			t.Errorf("ReadAt(%d, %d) = %q, want %q", c.pos, c.n, got, c.want)
		}
	}
	for _, pos := range []int{-1, 5, 99} {
		if got := b.RuneAt(pos); got != -1 {
			t.Errorf("RuneAt(%d) = %q, want -1", pos, got)
		}
	}
}

func TestLenTracksEdits(t *testing.T) {
	b := New()
	checkInvariants(t, b)
	if b.Len() != 0 {
		t.Fatalf("empty buffer Len = %d", b.Len())
	}
	b.Insert(0, "☃☃☃")
	if b.Len() != 3 {
		t.Fatalf("Len after multibyte insert = %d, want 3", b.Len())
	}
	b.Delete(1, 1)
	if b.Len() != 2 {
		t.Fatalf("Len after delete = %d, want 2", b.Len())
	}
	// Deleting past the end clamps and leaves Len consistent.
	b.Delete(1, 99)
	checkInvariants(t, b)
	if b.Len() != 1 || b.String() != "☃" {
		t.Fatalf("after clamped delete: Len=%d contents=%q", b.Len(), b.String())
	}
}

// Randomized edits compared against a []rune model. This is the test that
// would catch an incremental update that is wrong only in a rare case —
// a gap move that crosses a multibyte rune, a grow at the wrong moment.
func TestRandomEditsMatchModel(t *testing.T) {
	alphabets := []string{
		"abcdef\n",
		"héllo wörld\n",
		"☃—’日本語\n",
	}
	rng := rand.New(rand.NewSource(1))
	model := []rune(nil)
	b := New()

	for step := 0; step < 3000; step++ {
		switch {
		case len(model) == 0 || rng.Intn(100) < 55:
			alpha := []rune(alphabets[rng.Intn(len(alphabets))])
			n := 1 + rng.Intn(12)
			ins := make([]rune, n)
			for i := range ins {
				ins[i] = alpha[rng.Intn(len(alpha))]
			}
			pos := rng.Intn(len(model) + 1)
			b.Insert(pos, string(ins))
			model = append(model[:pos:pos], append(ins, model[pos:]...)...)
		default:
			pos := rng.Intn(len(model))
			n := 1 + rng.Intn(len(model)-pos)
			b.Delete(pos, n)
			model = append(model[:pos:pos], model[pos+n:]...)
		}

		checkInvariants(t, b)
		if b.Len() != len(model) {
			t.Fatalf("step %d: Len = %d, want %d", step, b.Len(), len(model))
		}
		if got, want := b.String(), string(model); got != want {
			t.Fatalf("step %d: contents diverged\n got %q\nwant %q", step, got, want)
		}
		// Spot-check random reads rather than all of them, to keep the
		// step count high.
		for i := 0; i < 4 && len(model) > 0; i++ {
			pos := rng.Intn(len(model))
			n := rng.Intn(len(model) - pos + 1)
			if got, want := b.ReadAt(pos, n), string(model[pos:pos+n]); got != want {
				t.Fatalf("step %d: ReadAt(%d,%d) = %q, want %q", step, pos, n, got, want)
			}
			if got, want := b.RuneAt(pos), model[pos]; got != want {
				t.Fatalf("step %d: RuneAt(%d) = %q, want %q", step, pos, got, want)
			}
		}
	}
}

// ---- benchmarks -------------------------------------------------------------

func largeText(runes int) string {
	var sb strings.Builder
	line := "the quick brown fox jumps over the lazy dog — pack my box\n"
	for sb.Len() < runes {
		sb.WriteString(line)
	}
	return sb.String()
}

// Typing in one spot: the gap is already there, so each keystroke should
// cost O(1) regardless of how far into the file it is.
func BenchmarkInsertAtEndOfLargeFile(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Insert(buf.Len(), "x")
	}
}

func BenchmarkInsertAtMiddleOfLargeFile(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	pos := buf.Len() / 2
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Insert(pos, "x")
	}
}

// The worst case the cache cannot help with: every edit is somewhere else,
// so the gap has to move each time.
func BenchmarkInsertScattered(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	rng := rand.New(rand.NewSource(1))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Insert(rng.Intn(buf.Len()), "x")
	}
}

func BenchmarkReadAtNearEnd(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	pos := buf.Len() - 100
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.ReadAt(pos, 80)
	}
}

func BenchmarkLen(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.Len()
	}
}
