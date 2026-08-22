package pane

import "testing"

// Layout.LineAt and Layout.LineAtY are pure functions over the Lines slice,
// so they can be tested against hand-built layouts without a Face or an SDL
// context.

const testLineH = 17

// mkLayout builds a layout of n uniform-height lines, each holding runesPer
// codepoints, matching what Wrap emits: Y = index * lineH, increasing.
func mkLayout(n, runesPer int) *Layout {
	l := &Layout{}
	for i := 0; i < n; i++ {
		l.Lines = append(l.Lines, VisualLine{
			Lo: i * runesPer,
			Hi: i*runesPer + runesPer - 1,
			Y:  int32(i) * testLineH,
		})
	}
	l.TotalHeight = int32(n) * testLineH
	return l
}

// naiveLineAtY is the backward scan LineAtY replaced, kept as the reference.
func naiveLineAtY(l *Layout, y int32) int {
	for i := len(l.Lines) - 1; i >= 0; i-- {
		if l.Lines[i].Y <= y {
			return i
		}
	}
	return 0
}

func TestLineAtYMatchesNaiveScan(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 7, 8, 9, 64, 100} {
		l := mkLayout(n, 10)
		// Sweep past both ends, and every pixel in between, so the
		// boundaries between line boxes are all exercised.
		for y := int32(-40); y <= int32(n)*testLineH+40; y++ {
			want := naiveLineAtY(l, y)
			if got := l.LineAtY(y); got != want {
				t.Fatalf("n=%d: LineAtY(%d) = %d, want %d", n, y, got, want)
			}
		}
	}
}

func TestLineAtYLandsInsideItsLineBox(t *testing.T) {
	l := mkLayout(50, 10)
	for y := int32(0); y < 50*testLineH; y++ {
		i := l.LineAtY(y)
		lo := l.Lines[i].Y
		if y < lo || y >= lo+testLineH {
			t.Fatalf("LineAtY(%d) = %d, whose box is [%d,%d)",
				y, i, lo, lo+testLineH)
		}
	}
}

// The property drawBody depends on: starting the draw loop at
// LineAtY(scrollY) must never skip a line that is actually on screen.
func TestLineAtYSkipsOnlyOffscreenLines(t *testing.T) {
	l := mkLayout(200, 10)
	for scrollY := int32(0); scrollY < 200*testLineH; scrollY += 3 {
		start := l.LineAtY(scrollY)
		for i := 0; i < start; i++ {
			if bottom := l.Lines[i].Y + testLineH; bottom > scrollY {
				t.Fatalf("scrollY=%d: start=%d skips line %d, "+
					"whose bottom %d is still visible", scrollY, start, i, bottom)
			}
		}
	}
}

func TestLineAtYClampsAtEnds(t *testing.T) {
	l := mkLayout(10, 10)
	if got := l.LineAtY(-1000); got != 0 {
		t.Errorf("LineAtY(-1000) = %d, want 0", got)
	}
	if got := l.LineAtY(1 << 20); got != 9 {
		t.Errorf("LineAtY(1<<20) = %d, want 9", got)
	}
	empty := &Layout{}
	if got := empty.LineAtY(0); got != 0 {
		t.Errorf("empty LineAtY(0) = %d, want 0", got)
	}
	if got := empty.LineAtY(500); got != 0 {
		t.Errorf("empty LineAtY(500) = %d, want 0", got)
	}
}

// LineAt already binary-searched; lock in its contract alongside LineAtY so
// the two stay consistent.
func TestLineAtMatchesNaiveScan(t *testing.T) {
	const runesPer = 10
	for _, n := range []int{0, 1, 2, 3, 7, 64} {
		l := mkLayout(n, runesPer)
		for cp := -5; cp <= n*runesPer+5; cp++ {
			want := 0
			for i := len(l.Lines) - 1; i >= 0; i-- {
				if l.Lines[i].Lo <= cp {
					want = i
					break
				}
			}
			if got := l.LineAt(cp); got != want {
				t.Fatalf("n=%d: LineAt(%d) = %d, want %d", n, cp, got, want)
			}
		}
	}
}

// moveDotToTopOfView wants the first line at or below the top of the
// viewport, which it derives from LineAtY. Check that derivation against the
// forward scan it replaced.
func TestFirstLineAtOrBelowDerivation(t *testing.T) {
	l := mkLayout(40, 10)
	for scrollY := int32(0); scrollY < 40*testLineH; scrollY++ {
		want := -1
		for i, vl := range l.Lines {
			if vl.Y >= scrollY {
				want = i
				break
			}
		}
		if want < 0 {
			continue // no such line; caller leaves dot alone
		}
		got := l.LineAtY(scrollY)
		if l.Lines[got].Y < scrollY && got+1 < len(l.Lines) {
			got++
		}
		if got != want {
			t.Fatalf("scrollY=%d: derived %d, want %d", scrollY, got, want)
		}
	}
}

func BenchmarkLineAtY(b *testing.B) {
	l := mkLayout(100000, 40)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.LineAtY(int32(i%100000) * testLineH)
	}
}

func BenchmarkLineAtYNaive(b *testing.B) {
	l := mkLayout(100000, 40)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = naiveLineAtY(l, int32(i%100000)*testLineH)
	}
}
