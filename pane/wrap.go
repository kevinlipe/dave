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

	if n == 0 {
		return Layout{
			Lines:       []VisualLine{{Lo: 0, Hi: 0, Y: 0, Wrapped: false}},
			TotalHeight: lineH,
			MarginLeft:  marginLeft,
			WrapWidth:   wrapPx,
		}
	}

	var lines []VisualLine
	lineIdx := 0
	pos := 0
	lineStart := 0
	lineWrapped := false
	curW := int32(0)
	wordStart := 0
	inWord := false

	flushLine := func(lo, hi int, wrapped bool) {
		lines = append(lines, VisualLine{Lo: lo, Hi: hi, Y: int32(lineIdx) * lineH, Wrapped: wrapped})
		lineIdx++
	}

	for pos < n {
		r := runes[pos]

		if r == '\n' {
			flushLine(lineStart, pos, lineWrapped)
			pos++
			lineStart = pos
			lineWrapped = false
			curW = 0
			wordStart = pos
			inWord = false
			continue
		}

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
				flushLine(lineStart, wordStart, lineWrapped)
				newCurW := measureRunes(runes, wordStart, pos, face, tabWidth)
				lineStart = wordStart
				lineWrapped = true
				curW = newCurW
				wordStart = lineStart
				inWord = isWordRune(r)
				rw = runeWidth(r, curW, face, tabWidth)
			} else {
				// Hard wrap: no word boundary in this run.
				flushLine(lineStart, pos, lineWrapped)
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

	flushLine(lineStart, n, lineWrapped)
	return Layout{Lines: lines, TotalHeight: int32(lineIdx) * lineH, MarginLeft: marginLeft, WrapWidth: wrapPx}
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
