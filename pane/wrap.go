package pane

import (
	"dave/buffer"
	"dave/render"
	"unicode"
)

// Wrap builds a Layout for buf.
//
// When wordWrap is true, lines are broken at word boundaries.  The break
// width is determined by marginChars and paneWidthPx:
//
//   - marginWidthPx = marginChars × (width of one 'M' glyph).
//   - If marginWidthPx ≤ paneWidthPx: wrap at marginWidthPx and centre the
//     resulting column horizontally within paneWidthPx (olivetti mode).
//   - If marginWidthPx > paneWidthPx: wrap at paneWidthPx, no centering.
//
// When wordWrap is false, visual lines map 1:1 to logical lines and
// paneWidthPx is ignored.
//
// tabWidth is the number of spaces a tab character expands to.
func Wrap(buf *buffer.Buffer, face render.Face, paneWidthPx int32, wordWrap bool, marginChars, tabWidth int) Layout {
	lineH := int32(face.Height())

	// Compute wrap width and centering offset.
	// Use average lowercase width rather than 'M' so that proportional fonts
	// (e.g. lucidasans) get a realistic character width and don't blow past
	// paneWidthPx before olivetti mode can kick in.
	const sample = "abcdefghijklmnopqrstuvwxyz"
	charW := int32(face.MeasureText(sample)) / int32(len(sample))
	if charW <= 0 {
		charW = int32(face.GlyphAdvance('M'))
	}
	if charW <= 0 {
		charW = int32(face.GlyphAdvance(' '))
	}
	marginWidthPx := charW * int32(marginChars)

	var wrapPx int32
	var marginLeft int32
	if wordWrap {
		if marginWidthPx > 0 && marginWidthPx <= paneWidthPx {
			wrapPx = marginWidthPx
			marginLeft = (paneWidthPx - marginWidthPx) / 2
		} else {
			wrapPx = paneWidthPx
			marginLeft = 0
		}
	} else {
		wrapPx = paneWidthPx
		marginLeft = 0
	}

	// Read the entire buffer once and work on the rune slice.
	runes := []rune(buf.String())
	n := len(runes)

	// Wrapping never crosses a newline: every reset of the wrap state happens
	// at one. So the layout is exactly the concatenation of independently
	// wrapped logical lines, and wrapLine does the actual work. A trailing
	// newline yields a final empty line, and so does an empty buffer.
	var lines []VisualLine
	segStart := 0
	for i := 0; i <= n; i++ {
		if i == n || runes[i] == '\n' {
			lines = wrapLine(lines, runes[segStart:i], segStart, face, wordWrap, wrapPx, tabWidth)
			segStart = i + 1
		}
	}

	return Layout{
		Lines:       lines,
		LineH:       lineH,
		TotalHeight: int32(len(lines)) * lineH,
		MarginLeft:  marginLeft,
		WrapWidth:   wrapPx,
	}
}

// wrapLine appends to out the visual lines for one logical line. runes is the
// line's text with its terminating newline excluded, and base is the
// codepoint offset at which it begins in the buffer.
//
// A logical line always produces at least one visual line, even when empty.
func wrapLine(out []VisualLine, runes []rune, base int, face render.Face,
	wordWrap bool, wrapPx int32, tabWidth int) []VisualLine {

	n := len(runes)
	lineStart := 0
	lineWrapped := false
	curW := int32(0)
	wordStart := 0
	inWord := false

	flush := func(lo, hi int, wrapped bool) {
		out = append(out, VisualLine{Lo: base + lo, Hi: base + hi, Wrapped: wrapped})
	}

	pos := 0
	for pos < n {
		r := runes[pos]
		rw := runeWidth(r, curW, face, tabWidth)

		if wordWrap {
			isWord := isWordRune(r)
			if isWord && !inWord {
				wordStart = pos
				inWord = true
			} else if !isWord {
				inWord = false
			}
		}

		if wordWrap && curW+rw > wrapPx && pos > lineStart {
			if wordStart > lineStart {
				// Soft wrap back to last word boundary.
				flush(lineStart, wordStart, lineWrapped)
				newCurW := measureRunes(runes, wordStart, pos, face, tabWidth)
				lineStart = wordStart
				lineWrapped = true
				curW = newCurW
				wordStart = lineStart
				inWord = isWordRune(r)
				rw = runeWidth(r, curW, face, tabWidth)
			} else {
				// Hard wrap: no word boundary in this run.
				flush(lineStart, pos, lineWrapped)
				lineStart = pos
				lineWrapped = true
				curW = 0
				wordStart = pos
				inWord = isWordRune(r)
				rw = runeWidth(r, 0, face, tabWidth)
			}
		}

		curW += rw
		pos++
	}

	flush(lineStart, n, lineWrapped)
	return out
}

// LineAt returns the index of the visual line containing codepoint offset cp.
// Always returns a valid index.
func (l *Layout) LineAt(cp int) int {
	if len(l.Lines) == 0 {
		return 0
	}
	lo, hi := 0, len(l.Lines)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if l.Lines[mid].Lo <= cp {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// LineAtY returns the index of the visual line whose line box contains the
// pixel offset y, measured from the top of the scrollable content (not the
// screen). A y above the first line clamps to line 0; a y past the last line
// clamps to the last line. Always returns a valid index.
//
// Every line box is LineH tall and they are stacked in order, so the line
// containing y is just y/LineH.
func (l *Layout) LineAtY(y int32) int {
	if len(l.Lines) == 0 || l.LineH <= 0 {
		return 0
	}
	i := int(y / l.LineH)
	if y < 0 {
		i = 0 // Go truncates toward zero, so -1 line rounds up to 0
	}
	if i >= len(l.Lines) {
		i = len(l.Lines) - 1
	}
	return i
}

// XForCP returns the pixel x-offset within vl at which codepoint cp appears,
// measured from the start of the visual line's text (before MarginLeft).
func XForCP(buf *buffer.Buffer, vl VisualLine, cp int, face render.Face, tabWidth int) int32 {
	return measureRange(buf, vl.Lo, cp, face, tabWidth)
}

// measureRange returns the pixel width of codepoints [lo, hi) in buf.
func measureRange(buf *buffer.Buffer, lo, hi int, face render.Face, tabWidth int) int32 {
	if hi <= lo {
		return 0
	}
	runes := []rune(buf.ReadAt(lo, hi-lo))
	return measureRunes(runes, 0, len(runes), face, tabWidth)
}

// measureRunes returns the pixel width of runes[lo:hi].
func measureRunes(runes []rune, lo, hi int, face render.Face, tabWidth int) int32 {
	x := int32(0)
	for i := lo; i < hi && i < len(runes); i++ {
		x += runeWidth(runes[i], x, face, tabWidth)
	}
	return x
}

// runeWidth returns the pixel width of rune r when placed at pixel curX.
func runeWidth(r rune, curX int32, face render.Face, tabWidth int) int32 {
	if r == '\t' {
		spaceW := int32(face.GlyphAdvance(' '))
		tabW := spaceW * int32(tabWidth)
		if tabW <= 0 {
			tabW = spaceW
		}
		col := curX / tabW
		rw := tabW*(col+1) - curX
		if rw <= 0 {
			rw = tabW
		}
		return rw
	}
	return int32(face.GlyphAdvance(r))
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
