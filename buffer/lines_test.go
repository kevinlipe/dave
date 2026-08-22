package buffer

import (
	"math/rand"
	"strings"
	"testing"
)

// naiveLineStarts is the reference: the codepoint offset of each line start,
// scanned directly from the string.
func naiveLineStarts(s string) []int {
	starts := []int{0}
	n := 0
	for _, r := range s {
		n++
		if r == '\n' {
			starts = append(starts, n)
		}
	}
	return starts
}

func naiveLineNumber(s string, pos int) int {
	starts := naiveLineStarts(s)
	n := 1
	for i, st := range starts {
		if st <= pos {
			n = i + 1
		}
	}
	return n
}

// checkLines compares every line query against the reference for a buffer
// whose contents are known to be want.
func checkLines(t *testing.T, b *Buffer, want string) {
	t.Helper()
	starts := naiveLineStarts(want)
	total := len([]rune(want))

	if got := b.LineCount(); got != len(starts) {
		t.Fatalf("LineCount() = %d, want %d (contents %q)", got, len(starts), want)
	}
	for n := 1; n <= len(starts); n++ {
		r, ok := b.LineRange(n)
		if !ok {
			t.Fatalf("LineRange(%d) not ok (contents %q)", n, want)
		}
		wantStart := starts[n-1]
		wantEnd := total
		if n < len(starts) {
			wantEnd = starts[n]
		}
		if r.Start != wantStart || r.End != wantEnd {
			t.Fatalf("LineRange(%d) = [%d,%d), want [%d,%d) (contents %q)",
				n, r.Start, r.End, wantStart, wantEnd, want)
		}
	}
	// Out of range in both directions.
	for _, n := range []int{-1, 0, len(starts) + 1, len(starts) + 99} {
		if _, ok := b.LineRange(n); ok {
			t.Fatalf("LineRange(%d) reported ok, want not ok (contents %q)", n, want)
		}
	}
	for pos := 0; pos <= total; pos++ {
		if got, w := b.LineNumber(pos), naiveLineNumber(want, pos); got != w {
			t.Fatalf("LineNumber(%d) = %d, want %d (contents %q)", pos, got, w, want)
		}
	}
}

func TestLineQueriesMatchNaiveScan(t *testing.T) {
	texts := []string{
		"",
		"\n",
		"\n\n\n",
		"a",
		"a\n",
		"a\nb",
		"a\nb\n",
		"hello\nworld\nfoo\n",
		"héllo\nwörld\n☃\n",
		"no newlines at all here",
		"trailing blank line\n\n",
	}
	for _, s := range texts {
		checkLines(t, NewFromString(s), s)
	}
}

// The table is built from the two gap segments, so where the gap sits must
// not change any answer -- and no line terminator may be lost at the seam.
func TestLineQueriesIndependentOfGapPosition(t *testing.T) {
	const text = "one\ntwo\nthrée\n\nfive"
	n := len([]rune(text))
	for gapAt := 0; gapAt <= n; gapAt++ {
		b := NewFromString(text)
		b.moveGap(b.runeToBytePos(gapAt))
		checkLines(t, b, text)
	}
}

// The index is keyed on the buffer generation; an edit must invalidate it.
func TestLineIndexInvalidatedByEdits(t *testing.T) {
	b := NewFromString("one\ntwo\n")
	checkLines(t, b, "one\ntwo\n")

	b.Insert(4, "mid\n") // "one\nmid\ntwo\n"
	checkLines(t, b, "one\nmid\ntwo\n")

	b.Delete(0, 4) // "mid\ntwo\n"
	checkLines(t, b, "mid\ntwo\n")

	// Deleting a newline merges two lines.
	b.Delete(3, 1) // "midtwo\n"
	checkLines(t, b, "midtwo\n")

	// Inserting a newline splits one.
	b.Insert(3, "\n") // "mid\ntwo\n"
	checkLines(t, b, "mid\ntwo\n")

	// Emptying the buffer leaves the one-empty-line case.
	b.Delete(0, b.Len())
	checkLines(t, b, "")
}

// White-box: the table must be cached between queries at the same
// generation, and rebuilt once the generation moves. Corrupting the cached
// table is the only way to observe the difference from outside.
func TestLineIndexIsCachedUntilGenChanges(t *testing.T) {
	b := NewFromString("one\ntwo\nthree\n")
	if got := b.LineCount(); got != 4 {
		t.Fatalf("LineCount() = %d, want 4", got)
	}
	b.lines.starts = []int{0} // corrupt the cache without touching gen
	if got := b.LineCount(); got != 1 {
		t.Fatalf("LineCount() = %d after corrupting cache, want 1 "+
			"(table is being rebuilt on every query, so it is not cached)", got)
	}
	b.Insert(0, "x") // bumps gen
	if got := b.LineCount(); got != 4 {
		t.Fatalf("LineCount() = %d after edit, want 4 "+
			"(stale table survived a generation change)", got)
	}
}

func TestRandomEditsKeepLineIndexCorrect(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alpha := []rune("ab\n\nc—☃\n")
	b := New()
	var model []rune

	for step := 0; step < 400; step++ {
		if len(model) == 0 || rng.Intn(100) < 55 {
			ins := make([]rune, 1+rng.Intn(10))
			for i := range ins {
				ins[i] = alpha[rng.Intn(len(alpha))]
			}
			pos := rng.Intn(len(model) + 1)
			b.Insert(pos, string(ins))
			model = append(model[:pos:pos], append(ins, model[pos:]...)...)
		} else {
			pos := rng.Intn(len(model))
			n := 1 + rng.Intn(len(model)-pos)
			b.Delete(pos, n)
			model = append(model[:pos:pos], model[pos+n:]...)
		}
		checkLines(t, b, string(model))
	}
}

// ---- benchmarks -------------------------------------------------------------

func linesText(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString("the quick brown fox jumps over the lazy dog\n")
	}
	return sb.String()
}

// The realistic pattern: one edit, then a command that resolves many line
// addresses against that one generation. Only the first query scans.
func BenchmarkLineRangeRepeated(b *testing.B) {
	buf := NewFromString(linesText(100000))
	buf.LineCount() // build once, as the first address in a command would
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = buf.LineRange(i%100000 + 1)
	}
}

// The cost paid once per generation.
func BenchmarkLineIndexRebuild(b *testing.B) {
	buf := NewFromString(linesText(100000))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.rebuildLines()
	}
}

func BenchmarkLineNumber(b *testing.B) {
	buf := NewFromString(linesText(100000))
	buf.LineCount()
	total := buf.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.LineNumber(i % total)
	}
}
