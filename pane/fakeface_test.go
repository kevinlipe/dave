package pane

import (
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// fakeFace is a deterministic render.Face for tests.
//
// Wrap only ever asks a Face for metrics, so a fake removes the SDL and font
// dependencies from layout tests entirely and makes wrap points predictable
// enough to write down by hand.
//
// Most runes advance by advance px. A few are deliberately wider so that
// proportional behaviour is exercised — in particular Wrap derives its
// character width by averaging MeasureText over the lowercase alphabet
// (wrap.go), so a face where every glyph is identical would hide bugs in
// that averaging.
type fakeFace struct {
	advance int
	height  int
	wide    map[rune]int
}

func newFakeFace() *fakeFace {
	return &fakeFace{
		advance: 10,
		height:  20,
		wide: map[rune]int{
			'W': 30,
			'm': 15,
			'i': 5,
			'☃': 20,
		},
	}
}

func (f *fakeFace) Height() int { return f.height }
func (f *fakeFace) Ascent() int { return f.height * 4 / 5 }

func (f *fakeFace) GlyphAdvance(r rune) int {
	if w, ok := f.wide[r]; ok {
		return w
	}
	return f.advance
}

func (f *fakeFace) MeasureText(s string) int {
	total := 0
	for _, r := range s {
		total += f.GlyphAdvance(r)
	}
	return total
}

func (f *fakeFace) DrawText(string, int32, int32, sdl.Color) error { return nil }
func (f *fakeFace) Close()                                         {}

// compile-time check that the fake satisfies the real interface
var _ render.Face = (*fakeFace)(nil)
