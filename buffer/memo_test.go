package buffer

import (
	"math/rand"
	"strings"
	"testing"
)

// runeToBytePos memoizes its previous answer and reuses it as an anchor. A
// stale or mis-segmented anchor yields a wrong byte offset rather than an
// error, so these tests compare against a naive scan under the access
// patterns that could expose one.

// naiveByteOff is the reference: the byte offset of rune n, counted from the
// start of the logical content.
func naiveByteOff(s string, n int) int {
	if n > len([]rune(s)) {
		return len(s)
	}
	return len(string([]rune(s)[:n]))
}

func checkAllPositions(t *testing.T, b *Buffer, want string, label string) {
	t.Helper()
	n := len([]rune(want))
	for pos := 0; pos <= n; pos++ {
		if got, w := b.runeToBytePos(pos), naiveByteOff(want, pos); got != w {
			t.Fatalf("%s: runeToBytePos(%d) = %d, want %d (contents %q)",
				label, pos, got, w, want)
		}
	}
}

// The memo is stamped with the generation; an edit must retire it.
func TestMemoInvalidatedByEdits(t *testing.T) {
	b := NewFromString("héllo wörld")
	checkAllPositions(t, b, "héllo wörld", "initial")

	// Warm the memo at the far end, then edit near the front. If the memo
	// survived, the stale pair would place later lookups wrongly.
	b.runeToBytePos(11)
	b.Insert(0, "☃☃")
	checkAllPositions(t, b, "☃☃héllo wörld", "after insert at front")

	b.runeToBytePos(13)
	b.Delete(2, 6)
	checkAllPositions(t, b, "☃☃wörld", "after delete")

	b.runeToBytePos(7)
	b.Insert(3, "—")
	checkAllPositions(t, b, "☃☃w—örld", "after insert in middle")
}

// The memo lands in one gap segment; a lookup in the other must not use it.
func TestMemoAcrossGapSegments(t *testing.T) {
	const text = "one\ntwö\nthrée\nfour\nfive"
	n := len([]rune(text))
	for gapAt := 0; gapAt <= n; gapAt++ {
		b := NewFromString(text)
		b.moveGap(b.runeToBytePos(gapAt))

		// Alternate across the gap so consecutive lookups land in different
		// segments as often as possible.
		for i := 0; i <= n; i++ {
			for _, pos := range []int{i, n - i} {
				if got, w := b.runeToBytePos(pos), naiveByteOff(text, pos); got != w {
					t.Fatalf("gap at %d: runeToBytePos(%d) = %d, want %d",
						gapAt, pos, got, w)
				}
			}
		}
	}
}

// A lookup past the end clamps; that answer does not describe the position
// asked for and must not become the anchor.
func TestClampedLookupDoesNotPoisonMemo(t *testing.T) {
	const text = "héllo wörld"
	n := len([]rune(text))
	for _, past := range []int{n + 1, n + 5, n + 1000} {
		b := NewFromString(text)
		if got, w := b.runeToBytePos(past), len(text); got != w {
			t.Fatalf("runeToBytePos(%d) = %d, want %d (clamped)", past, got, w)
		}
		checkAllPositions(t, b, text, "after clamped lookup")
	}
}

// Access patterns: the memo helps forward walks, but must be correct for any
// order, including repeats and jumps.
func TestMemoUnderVariedAccessOrders(t *testing.T) {
	const text = "aé☃b—cd\nef☃gh—ij\nkl"
	n := len([]rune(text))

	orders := map[string][]int{}
	var fwd, bwd, rep []int
	for i := 0; i <= n; i++ {
		fwd = append(fwd, i)
		bwd = append(bwd, n-i)
		rep = append(rep, i, i, 0, i)
	}
	orders["forward"] = fwd
	orders["backward"] = bwd
	orders["repeats"] = rep

	rng := rand.New(rand.NewSource(5))
	var rnd []int
	for i := 0; i < 500; i++ {
		rnd = append(rnd, rng.Intn(n+1))
	}
	orders["random"] = rnd

	for label, order := range orders {
		b := NewFromString(text)
		for _, pos := range order {
			if got, w := b.runeToBytePos(pos), naiveByteOff(text, pos); got != w {
				t.Fatalf("%s order: runeToBytePos(%d) = %d, want %d", label, pos, got, w)
			}
		}
	}
}

// Edits and lookups interleaved, against a model. This is the test that would
// catch a memo that is invalidated in most paths but not all.
func TestMemoUnderInterleavedEditsAndLookups(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	alpha := []rune("ab\n☃é—x")
	b := New()
	var model []rune

	for step := 0; step < 600; step++ {
		if len(model) == 0 || rng.Intn(100) < 55 {
			ins := make([]rune, 1+rng.Intn(8))
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

		want := string(model)
		// A handful of lookups per edit, so the memo is warm and points
		// somewhere unrelated to the next edit.
		for i := 0; i < 5; i++ {
			pos := rng.Intn(len(model) + 1)
			if got, w := b.runeToBytePos(pos), naiveByteOff(want, pos); got != w {
				t.Fatalf("step %d: runeToBytePos(%d) = %d, want %d (contents %q)",
					step, pos, got, w, want)
			}
		}
		if got := b.String(); got != want {
			t.Fatalf("step %d: contents %q, want %q", step, got, want)
		}
	}
}

// ---- benchmarks -------------------------------------------------------------

// A structural command's access pattern: read successive small ranges moving
// forward through the file, with no edits in between.
func BenchmarkSequentialReadAtForward(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	n := buf.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pos := (i * 43) % (n - 40)
		if i%1000 == 0 {
			pos = 0
		}
		_ = buf.ReadAt(pos, 40)
	}
}

// The pattern the memo cannot help with: reads jumping around at random.
func BenchmarkScatteredReadAt(b *testing.B) {
	buf := NewFromString(largeText(1 << 20))
	n := buf.Len()
	rng := rand.New(rand.NewSource(1))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.ReadAt(rng.Intn(n-40), 40)
	}
}

func BenchmarkSequentialRuneAt(b *testing.B) {
	buf := NewFromString(strings.Repeat("héllo wörld — ☃\n", 40000))
	n := buf.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buf.RuneAt(i % n)
	}
}

// segByteOffset must give the same answer whatever anchor it is handed --
// including anchors belonging to the other gap segment or left over from a
// clamped lookup, which it is required to ignore rather than walk from.
//
// The callers currently cannot produce a winning out-of-range anchor, because
// raw distance always prefers a real one. That is arithmetic, not an
// invariant: weighting the backward walk (DecodeLastRune costs about twice
// what DecodeRune does, so weighting it is a plausible change) would break it,
// and the failure would be a silently wrong offset. The check lives in
// segByteOffset so it holds regardless, and this is what holds it there.
func TestSegByteOffsetIgnoresOutOfRangeAnchors(t *testing.T) {
	seg := []byte("héllo wörld — ☃ tail")
	segRunes := len([]rune(string(seg)))

	badAnchors := [][2]int{
		{segRunes + 1, len(seg)},        // past the end in runes
		{segRunes + 500, len(seg)},      // far past, as a clamped memo would be
		{0, len(seg) + 1},               // byte offset past the end
		{segRunes, len(seg) + 40},       // both past
		{-1, -1},                        // the explicit "no anchor"
		{-5, -20},                       // a rebased pre-gap anchor gone negative
		{segRunes + 1, len(seg) + 1000}, // nonsense in both coordinates
	}

	for target := 0; target <= segRunes+3; target++ {
		want := segByteOffset(seg, target, segRunes, -1, -1)
		// An anchor is only dangerous if it would win on distance, so include
		// ones sitting exactly on the target in runes but out of range in
		// bytes. Without the range check these walk off the end of the slice.
		cases := append([][2]int{
			{target, len(seg) + 1},
			{target, len(seg) + 500},
			{target, -1},
			{target, -len(seg)},
		}, badAnchors...)
		for _, a := range cases {
			got := segByteOffset(seg, target, segRunes, a[0], a[1])
			if got != want {
				t.Fatalf("target=%d anchor=%v: got %d, want %d", target, a, got, want)
			}
		}
		// A valid anchor at every position must also agree.
		for ar := 0; ar <= segRunes; ar++ {
			ab := len(string([]rune(string(seg))[:ar]))
			if got := segByteOffset(seg, target, segRunes, ar, ab); got != want {
				t.Fatalf("target=%d valid anchor=(%d,%d): got %d, want %d",
					target, ar, ab, got, want)
			}
		}
	}
}
