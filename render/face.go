package render

import "github.com/veandco/go-sdl2/sdl"

// Face is the interface over TTF font rendering backends.
// All text drawing in Dave goes through this interface so that the
// rendering backend can be swapped without changing callers.
type Face interface {
	// Height returns the line height in pixels (advance from one baseline to the next).
	Height() int
	// Ascent returns the distance from the top of the line box to the baseline.
	Ascent() int
	// GlyphAdvance returns the advance width of rune r in pixels.
	GlyphAdvance(r rune) int
	// MeasureText returns the total pixel width of string s.
	MeasureText(s string) int
	// DrawText renders s at pixel position (x, y) — y is the top of the line box.
	DrawText(s string, x, y int32, col sdl.Color) error
	// Close releases all resources held by the face.
	Close()
}
