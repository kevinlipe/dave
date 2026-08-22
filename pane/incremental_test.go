package pane

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"dave/buffer"
	"dave/config"
	"dave/file"
	"dave/render"
)

// Relayout re-wraps only the logical lines an edit touched and splices the
// result into the existing layout. The whole correctness story is one
// property: however it got there, the layout must equal what a full Wrap of
// the same buffer would produce.
//
// A spliced layout that is subtly wrong shows as text drawn at the wrong
// place rather than as a crash, so these compare field by field.

func testPane(t *testing.T, text string, wordWrap bool, margin int) (*Pane, *buffer.Buffer) {
	t.Helper()
	cfg := config.Default()
	cfg.WordWrap = wordWrap
	cfg.Margin = margin
	cfg.TabWidth = 4
	f := file.New()
	f.Buffer().Insert(0, text)
	p := New(f, cfg, render.Palette{}, newFakeFace())
	return p, f.Buffer()
}

// wantLayout is what a full wrap produces for the pane's current state.
func (p *Pane) wantLayout(contentW int32) Layout {
	margin := p.cfg.Margin
	if !p.wordWrap {
		margin = 0
	}
	return Wrap(p.File.Buffer(), p.face, contentW, p.wordWrap, margin, p.cfg.TabWidth)
}

func assertLayoutsEqual(t *testing.T, got, want Layout, what string) {
	t.Helper()
	if got.LineH != want.LineH || got.MarginLeft != want.MarginLeft ||
		got.WrapWidth != want.WrapWidth || got.TotalHeight != want.TotalHeight {
		t.Fatalf("%s: layout metrics differ\n got  lineH=%d marginLeft=%d wrapWidth=%d total=%d\n want lineH=%d marginLeft=%d wrapWidth=%d total=%d",
			what, got.LineH, got.MarginLeft, got.WrapWidth, got.TotalHeight,
			want.LineH, want.MarginLeft, want.WrapWidth, want.TotalHeight)
	}
	if len(got.Lines) != len(want.Lines) {
		t.Fatalf("%s: %d visual lines, want %d", what, len(got.Lines), len(want.Lines))
	}
	for i := range got.Lines {
		if got.Lines[i] != want.Lines[i] {
			t.Fatalf("%s: line %d is %+v, want %+v", what, i, got.Lines[i], want.Lines[i])
		}
	}
}

const testWidth = int32(100)

// Edits of every shape, each checked against a full wrap.
func TestIncrementalRelayoutMatchesFullWrapForEachEditShape(t *testing.T) {
	const text = "alpha beta gamma\ndelta epsilon\n\nzeta eta theta iota\nkappa\n"

	cases := []struct {
		name string
		do   func(b *buffer.Buffer)
	}{
		{"insert mid-line", func(b *buffer.Buffer) { b.Insert(3, "XYZ") }},
		{"insert at line start", func(b *buffer.Buffer) { b.Insert(17, "X") }},
		{"insert at buffer start", func(b *buffer.Buffer) { b.Insert(0, "X") }},
		{"insert at buffer end", func(b *buffer.Buffer) { b.Insert(b.Len(), "X") }},
		{"insert a newline (splits a line)", func(b *buffer.Buffer) { b.Insert(5, "\n") }},
		{"insert several newlines", func(b *buffer.Buffer) { b.Insert(5, "\n\n\n") }},
		{"insert enough to force a wrap", func(b *buffer.Buffer) {
			b.Insert(3, strings.Repeat("q", 40))
		}},
		{"delete mid-line", func(b *buffer.Buffer) { b.Delete(3, 4) }},
		{"delete a newline (merges lines)", func(b *buffer.Buffer) { b.Delete(16, 1) }},
		{"delete across lines", func(b *buffer.Buffer) { b.Delete(10, 20) }},
		{"delete the blank line", func(b *buffer.Buffer) { b.Delete(30, 1) }},
		{"delete to end", func(b *buffer.Buffer) { b.Delete(20, b.Len()-20) }},
		{"delete everything", func(b *buffer.Buffer) { b.Delete(0, b.Len()) }},
		{"replace a selection", func(b *buffer.Buffer) {
			b.Delete(6, 4)
			b.Insert(6, "REPLACED")
		}},
		{"multibyte insert", func(b *buffer.Buffer) { b.Insert(6, "héllo ☃ wörld") }},
		{"insert a tab", func(b *buffer.Buffer) { b.Insert(6, "\t") }},
		{"empty insert", func(b *buffer.Buffer) { b.Insert(6, "") }},
	}

	for _, wordWrap := range []bool{true, false} {
		for _, c := range cases {
			p, buf := testPane(t, text, wordWrap, 0)
			p.Relayout(testWidth) // establish a baseline layout
			c.do(buf)
			p.Relayout(testWidth)
			what := fmt.Sprintf("%s (wordWrap=%v)", c.name, wordWrap)
			assertLayoutsEqual(t, p.layout, p.wantLayout(testWidth), what)
		}
	}
}

// The same property under long random edit sequences, which is what actually
// exercises splices landing next to previous splices.
func TestIncrementalRelayoutMatchesFullWrapUnderRandomEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	alpha := []rune("abcdefg hij \n\n\tkl☃é—mn opqrst")

	for _, wordWrap := range []bool{true, false} {
		for _, width := range []int32{60, 100, 250} {
			for _, margin := range []int{0, 20} {
				p, buf := testPane(t,
					"the quick brown fox\njumps over\n\nthe lazy dog\nand keeps going\n",
					wordWrap, margin)
				p.Relayout(width)

				for step := 0; step < 250; step++ {
					cur := buf.Len()
					switch {
					case cur == 0 || rng.Intn(100) < 55:
						ins := make([]rune, 1+rng.Intn(8))
						for i := range ins {
							ins[i] = alpha[rng.Intn(len(alpha))]
						}
						buf.Insert(rng.Intn(cur+1), string(ins))
					default:
						pos := rng.Intn(cur)
						buf.Delete(pos, 1+rng.Intn(min(12, cur-pos)))
					}

					p.Relayout(width)
					what := fmt.Sprintf("step %d (wordWrap=%v width=%d margin=%d)",
						step, wordWrap, width, margin)
					assertLayoutsEqual(t, p.layout, p.wantLayout(width), what)
				}
			}
		}
	}
}

// A run of edits between two relayouts must fold correctly, which is the case
// a command like ,x/re/c/X/ produces.
func TestIncrementalRelayoutAfterSeveralEditsAtOnce(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	for trial := 0; trial < 200; trial++ {
		p, buf := testPane(t,
			"one two three\nfour five six\nseven eight\nnine ten\neleven\n", true, 0)
		p.Relayout(testWidth)

		n := 1 + rng.Intn(10)
		for i := 0; i < n; i++ {
			cur := buf.Len()
			if cur == 0 || rng.Intn(100) < 60 {
				buf.Insert(rng.Intn(cur+1), strings.Repeat("z", 1+rng.Intn(5)))
			} else {
				pos := rng.Intn(cur)
				buf.Delete(pos, 1+rng.Intn(min(8, cur-pos)))
			}
		}

		p.Relayout(testWidth)
		assertLayoutsEqual(t, p.layout, p.wantLayout(testWidth),
			fmt.Sprintf("trial %d, %d edits", trial, n))
	}
}

// Falling further behind than the buffer's edit log must produce a correct
// layout via the full-wrap fallback rather than a wrong one.
func TestIncrementalRelayoutFallsBackWhenTooFarBehind(t *testing.T) {
	p, buf := testPane(t, "alpha beta\ngamma delta\n", true, 0)
	p.Relayout(testWidth)
	for i := 0; i < 200; i++ {
		buf.Insert(rand.Intn(buf.Len()+1), "x")
	}
	p.Relayout(testWidth)
	assertLayoutsEqual(t, p.layout, p.wantLayout(testWidth), "after log overflow")
}

// Everything that is not a plain text change must still take the full path.
func TestNonTextChangesStillRelayoutFully(t *testing.T) {
	const text = "alpha beta gamma delta epsilon\nzeta eta\n"

	t.Run("width change", func(t *testing.T) {
		p, buf := testPane(t, text, true, 0)
		p.Relayout(testWidth)
		buf.Insert(3, "X")
		p.Relayout(250)
		assertLayoutsEqual(t, p.layout, p.wantLayout(250), "after resize")
	})

	t.Run("word wrap toggled", func(t *testing.T) {
		p, buf := testPane(t, text, true, 0)
		p.Relayout(testWidth)
		buf.Insert(3, "X")
		p.ToggleWordWrap()
		p.Relayout(testWidth)
		assertLayoutsEqual(t, p.layout, p.wantLayout(testWidth), "after toggle")
	})

	t.Run("layout invalidated", func(t *testing.T) {
		p, buf := testPane(t, text, true, 0)
		p.Relayout(testWidth)
		buf.Insert(3, "X")
		p.InvalidateLayout()
		p.Relayout(testWidth)
		assertLayoutsEqual(t, p.layout, p.wantLayout(testWidth), "after invalidate")
	})
}

// ---- benchmarks -------------------------------------------------------------

// A prose file of roughly the size the user cares about: 100k words is about
// 600KB, which at this width is a few thousand visual lines.
func proseText(words int) string {
	var sb strings.Builder
	word := []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
		"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	for i := 0; i < words; i++ {
		sb.WriteString(word[i%len(word)])
		if i%12 == 11 {
			sb.WriteString("\n\n") // paragraph break
		} else {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

func benchKeystroke(b *testing.B, words int, width int32) {
	cfg := config.Default()
	cfg.Margin = 0
	f := file.New()
	f.Buffer().Insert(0, proseText(words))
	p := New(f, cfg, render.Palette{}, newFakeFace())
	p.Relayout(width)
	buf := f.Buffer()
	pos := buf.Len() / 2
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Insert(pos, "x")
		p.Relayout(width)
	}
	b.StopTimer()
	b.ReportMetric(float64(len(p.layout.Lines)), "visual-lines")
}

func BenchmarkKeystroke100kWords(b *testing.B) { benchKeystroke(b, 100_000, 600) }
func BenchmarkKeystroke1mWords(b *testing.B)   { benchKeystroke(b, 1_000_000, 600) }

// The same edit forced down the full-wrap path, for comparison.
func benchFullWrap(b *testing.B, words int, width int32) {
	cfg := config.Default()
	cfg.Margin = 0
	f := file.New()
	f.Buffer().Insert(0, proseText(words))
	p := New(f, cfg, render.Palette{}, newFakeFace())
	p.Relayout(width)
	buf := f.Buffer()
	pos := buf.Len() / 2
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Insert(pos, "x")
		p.InvalidateLayout()
		p.Relayout(width)
	}
}

func BenchmarkKeystroke100kWordsFullWrap(b *testing.B) { benchFullWrap(b, 100_000, 600) }
func BenchmarkKeystroke1mWordsFullWrap(b *testing.B)   { benchFullWrap(b, 1_000_000, 600) }

// The property tests above compare against a full wrap, so they would also
// pass if the incremental path never ran and everything fell back. This
// checks the path is actually taken, which is what makes them mean anything.
func TestOrdinaryEditsTakeTheIncrementalPath(t *testing.T) {
	p, buf := testPane(t, "alpha beta gamma\ndelta epsilon\n\nzeta eta\n", true, 0)
	p.Relayout(testWidth)
	if p.fullWraps != 1 {
		t.Fatalf("setup did %d full wraps, want 1", p.fullWraps)
	}

	edits := []func(){
		func() { buf.Insert(3, "X") },                     // inside a line
		func() { buf.Insert(0, "Y") },                     // at the buffer start
		func() { buf.Insert(buf.Len(), "Z") },             // at the buffer end
		func() { buf.Delete(5, 2) },                       // inside a line
		func() { buf.Insert(8, "\n") },                    // splitting a line
		func() { buf.Delete(8, 1) },                       // merging it back
		func() { buf.Insert(4, strings.Repeat("q", 30)) }, // forcing a re-wrap
	}
	for i, e := range edits {
		before := p.fullWraps
		e()
		p.Relayout(testWidth)
		if p.fullWraps != before {
			t.Errorf("edit %d fell back to a full wrap", i)
		}
		assertLayoutsEqual(t, p.layout, p.wantLayout(testWidth),
			fmt.Sprintf("edit %d", i))
	}
	if p.incrementalRelayouts != len(edits) {
		t.Errorf("%d incremental relayouts for %d edits",
			p.incrementalRelayouts, len(edits))
	}
}

// And the random sequences must overwhelmingly take it too, otherwise the
// property is being proved against the fallback.
func TestRandomEditsMostlyTakeTheIncrementalPath(t *testing.T) {
	rng := rand.New(rand.NewSource(47))
	p, buf := testPane(t, proseText(2000), true, 0)
	p.Relayout(testWidth)
	base := p.fullWraps

	const steps = 300
	for i := 0; i < steps; i++ {
		cur := buf.Len()
		if rng.Intn(100) < 60 {
			buf.Insert(rng.Intn(cur+1), "x")
		} else {
			pos := rng.Intn(cur)
			buf.Delete(pos, 1+rng.Intn(min(4, cur-pos)))
		}
		p.Relayout(testWidth)
	}
	if fell := p.fullWraps - base; fell != 0 {
		t.Errorf("%d of %d edits fell back to a full wrap", fell, steps)
	}
}
