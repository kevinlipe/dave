// Package pane implements the pane model for Dave: word-wrap layout,
// scroll position, dot rendering, and pane headers.
//
// The layout representation is defined here and must be correct before any
// cursor-movement code is written, per the Phase 2 design constraint.
package pane

// VisualLine describes one row of text as it appears on screen after word
// wrap has been applied.
//
// A single logical line (terminated by '\n', or the end of the file) may
// produce one or more VisualLines.  The first VisualLine for a logical line
// has Wrapped = false; all continuations have Wrapped = true.
type VisualLine struct {
	// Lo is the codepoint offset of the first rune in this visual line
	// within the underlying file buffer.
	Lo int

	// Hi is the codepoint offset one past the last rune in this visual line.
	// For a non-final visual line that ends at a wrap point, Hi points to
	// the first rune of the next visual line (which is not a newline).
	// For a visual line that ends with '\n', Hi points to the newline itself
	// and the newline is not rendered.
	Hi int

	// Y is the top-of-line-box pixel position within the pane's text area
	// (i.e. relative to the top of the scrollable content, not the screen).
	Y int32

	// Wrapped is true when this visual line is a soft-wrap continuation of
	// the previous logical line (i.e. it does not start after a '\n').
	Wrapped bool
}

// Layout holds the complete set of visual lines for a pane at a given width.
// It is recomputed whenever the text changes or the pane is resized.
type Layout struct {
	Lines []VisualLine
	// TotalHeight is the pixel height of all lines combined.
	TotalHeight int32
	// MarginLeft is the pixel x-offset from the left edge of the text area to
	// the start of rendered text. Non-zero when the margin column fits within
	// the pane and centering is applied (olivetti mode).
	MarginLeft int32
	// WrapWidth is the pixel width at which lines were broken.
	WrapWidth int32
}
