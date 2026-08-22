package buffer

import (
	"math/rand"
	"strings"
	"testing"
)

// EditsSince promises one thing: everything that changed since a generation
// lies inside the range it returns. These tests check exactly that, because
// a range that is too narrow is a silent corruption of whatever the caller
// derived from the buffer, while a range that is too wide only costs it
// some work.

// checkCovers asserts the promise: with delta the net change in length,
// old[:lo] must equal new[:lo], and old[hi-delta:] must equal new[hi:].
func checkCovers(t *testing.T, old, cur []rune, lo, hi int, what string) {
	t.Helper()
	delta := len(cur) - len(old)

	if lo < 0 || lo > len(cur) || hi < lo || hi > len(cur) {
		t.Fatalf("%s: range [%d,%d) is not within the current buffer of %d",
			what, lo, hi, len(cur))
	}
	// Head: unchanged and unmoved.
	if lo > len(old) {
		t.Fatalf("%s: lo=%d is past the old buffer of %d", what, lo, len(old))
	}
	if string(old[:lo]) != string(cur[:lo]) {
		t.Fatalf("%s: text before lo=%d changed\n old %q\n new %q",
			what, lo, string(old[:lo]), string(cur[:lo]))
	}
	// Tail: unchanged, but shifted by delta.
	oldHi := hi - delta
	if oldHi < 0 || oldHi > len(old) {
		t.Fatalf("%s: hi=%d maps to %d in the old buffer of %d",
			what, hi, oldHi, len(old))
	}
	if string(old[oldHi:]) != string(cur[hi:]) {
		t.Fatalf("%s: text after hi=%d changed\n old %q\n new %q",
			what, hi, string(old[oldHi:]), string(cur[hi:]))
	}
}

func TestEditsSinceCurrentGenerationIsEmpty(t *testing.T) {
	b := NewFromString("hello world")
	lo, hi, ok := b.EditsSince(b.Gen())
	if !ok || lo != 0 || hi != 0 {
		t.Fatalf("EditsSince(current) = (%d,%d,%v), want (0,0,true)", lo, hi, ok)
	}
	b.Insert(5, "!!")
	lo, hi, ok = b.EditsSince(b.Gen())
	if !ok || lo != 0 || hi != 0 {
		t.Fatalf("after an edit, EditsSince(current) = (%d,%d,%v), want (0,0,true)", lo, hi, ok)
	}
}

func TestEditsSinceFutureGenerationIsNotOK(t *testing.T) {
	b := NewFromString("hello")
	if _, _, ok := b.EditsSince(b.Gen() + 1); ok {
		t.Error("EditsSince(future) reported ok")
	}
}

// A consumer that falls further behind than the log retains must be told to
// rebuild rather than given a wrong answer.
func TestEditsSinceOverflowIsNotOK(t *testing.T) {
	b := NewFromString("hello world")
	gen := b.Gen()
	for i := 0; i < editLogSize+5; i++ {
		b.Insert(0, "x")
	}
	if _, _, ok := b.EditsSince(gen); ok {
		t.Error("EditsSince across a log overflow reported ok; it must ask for a rebuild")
	}
	// One generation back is still inside the log and must work.
	if _, _, ok := b.EditsSince(b.Gen() - 1); !ok {
		t.Error("EditsSince(gen-1) should be answerable")
	}
}

func TestEditsSinceSingleEdits(t *testing.T) {
	cases := []struct {
		name string
		text string
		do   func(b *Buffer)
	}{
		{"insert at start", "hello world", func(b *Buffer) { b.Insert(0, "X") }},
		{"insert in middle", "hello world", func(b *Buffer) { b.Insert(5, "XYZ") }},
		{"insert at end", "hello world", func(b *Buffer) { b.Insert(11, "!") }},
		{"delete at start", "hello world", func(b *Buffer) { b.Delete(0, 2) }},
		{"delete in middle", "hello world", func(b *Buffer) { b.Delete(4, 3) }},
		{"delete at end", "hello world", func(b *Buffer) { b.Delete(9, 2) }},
		{"delete everything", "hello world", func(b *Buffer) { b.Delete(0, 11) }},
		{"insert into empty", "", func(b *Buffer) { b.Insert(0, "new") }},
		{"multibyte insert", "héllo wörld", func(b *Buffer) { b.Insert(5, "☃—") }},
		{"multibyte delete", "héllo wörld", func(b *Buffer) { b.Delete(1, 3) }},
		{"replace via delete+insert", "hello world", func(b *Buffer) {
			b.Delete(6, 5)
			b.Insert(6, "there")
		}},
	}
	for _, c := range cases {
		b := NewFromString(c.text)
		old := []rune(b.String())
		gen := b.Gen()
		c.do(b)
		lo, hi, ok := b.EditsSince(gen)
		if !ok {
			t.Errorf("%s: EditsSince reported not ok", c.name)
			continue
		}
		checkCovers(t, old, []rune(b.String()), lo, hi, c.name)
	}
}

// Two small edits far apart, the later one to the left of the earlier. The
// left edit shifts the right one's dirty region rightwards, so a fold that
// unions raw positions without moving the range it has already accumulated
// reports an hi one short and misses the shifted text.
//
// Random runs on a small buffer do not find this: the edits overlap and the
// union covers the gap by accident.
func TestEditsSinceShiftsEarlierRangesByLaterEdits(t *testing.T) {
	for _, order := range []struct {
		name          string
		first, second int
	}{
		{"right then left", 50, 0},
		{"left then right", 0, 50},
		{"far apart, both right", 60, 30},
	} {
		b := NewFromString(strings.Repeat("a", 100))
		old := []rune(b.String())
		gen := b.Gen()
		b.Insert(order.first, "Z")
		// The second insert lands in coordinates that already include the first.
		pos := order.second
		if order.second > order.first {
			pos++
		}
		b.Insert(pos, "Y")

		lo, hi, ok := b.EditsSince(gen)
		if !ok {
			t.Fatalf("%s: not ok", order.name)
		}
		checkCovers(t, old, []rune(b.String()), lo, hi, order.name)
	}
}

// The same shape, with deletions, and with a wider gap than any union could
// paper over.
func TestEditsSinceShiftsAcrossDeletions(t *testing.T) {
	b := NewFromString(strings.Repeat("abcdefghij", 40)) // 400 runes
	old := []rune(b.String())
	gen := b.Gen()
	b.Insert(300, "Z") // dirty near the end
	b.Delete(0, 20)    // everything after shifts left by 20
	b.Insert(5, "QQ")  // and right by 2

	lo, hi, ok := b.EditsSince(gen)
	if !ok {
		t.Fatal("not ok")
	}
	checkCovers(t, old, []rune(b.String()), lo, hi, "shift across deletions")
}

// The real test: arbitrary runs of edits, checked against the contents as
// they were before the run.
func TestEditsSinceCoversRandomEditRuns(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	alpha := []rune("ab \n☃é—xyz")

	for trial := 0; trial < 400; trial++ {
		// Start from a random buffer.
		start := make([]rune, 200+rng.Intn(200))
		for i := range start {
			start[i] = alpha[rng.Intn(len(alpha))]
		}
		b := NewFromString(string(start))
		old := []rune(b.String())
		gen := b.Gen()

		// Apply a run of edits short enough to stay inside the log.
		n := 1 + rng.Intn(8)
		for i := 0; i < n; i++ {
			cur := b.Len()
			if cur == 0 || rng.Intn(100) < 55 {
				// Short insertions, so the dirty regions stay separated and
				// the fold has to shift them rather than swallow everything.
				ins := make([]rune, 1+rng.Intn(3))
				for j := range ins {
					ins[j] = alpha[rng.Intn(len(alpha))]
				}
				b.Insert(rng.Intn(cur+1), string(ins))
			} else {
				pos := rng.Intn(cur)
				b.Delete(pos, 1+rng.Intn(min(5, cur-pos)))
			}
		}

		lo, hi, ok := b.EditsSince(gen)
		if !ok {
			t.Fatalf("trial %d: EditsSince reported not ok for %d edits", trial, n)
		}
		checkCovers(t, old, []rune(b.String()), lo, hi, "random run")
	}
}

// Asking part-way through a run must cover only what happened after that
// point, and must still satisfy the promise.
func TestEditsSinceFromAnIntermediateGeneration(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	alpha := []rune("abc \n☃")

	for trial := 0; trial < 200; trial++ {
		b := NewFromString("the quick brown fox\njumps over\nthe lazy dog\n")

		// Snapshot the contents at each generation along the way.
		type snap struct {
			gen  int
			text []rune
		}
		snaps := []snap{{b.Gen(), []rune(b.String())}}
		for i := 0; i < 6; i++ {
			cur := b.Len()
			if cur == 0 || rng.Intn(100) < 60 {
				ins := make([]rune, 1+rng.Intn(4))
				for j := range ins {
					ins[j] = alpha[rng.Intn(len(alpha))]
				}
				b.Insert(rng.Intn(cur+1), string(ins))
			} else {
				pos := rng.Intn(cur)
				b.Delete(pos, 1+rng.Intn(cur-pos))
			}
			snaps = append(snaps, snap{b.Gen(), []rune(b.String())})
		}

		cur := []rune(b.String())
		for _, s := range snaps {
			lo, hi, ok := b.EditsSince(s.gen)
			if !ok {
				t.Fatalf("trial %d: not ok for gen %d", trial, s.gen)
			}
			checkCovers(t, s.text, cur, lo, hi, "intermediate")
		}
	}
}

// A no-op Delete does not move the generation, so it must not consume a slot
// in the log or appear as a change.
func TestEditsSinceIgnoresNoOpDeletes(t *testing.T) {
	b := NewFromString("hello")
	gen := b.Gen()
	b.Delete(2, 0)
	b.Delete(2, -1)
	lo, hi, ok := b.EditsSince(gen)
	if !ok || lo != 0 || hi != 0 {
		t.Fatalf("no-op deletes produced (%d,%d,%v), want (0,0,true)", lo, hi, ok)
	}
	if b.Gen() != gen {
		t.Errorf("no-op deletes moved gen from %d to %d", gen, b.Gen())
	}
}
