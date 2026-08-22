package input

import (
	"dave/buffer"
	"dave/cmd"
	"dave/file"
	"dave/pane"
	"dave/render"
)

// WordBounds returns the start and end of the word (or bracket pair) at cp.
func WordBounds(buf *buffer.Buffer, cp int) (lo, hi int) {
	n := buf.Len()
	if cp >= n {
		cp = n - 1
	}
	if cp < 0 {
		return 0, 0
	}

	r := runeAt(buf, cp)

	// Bracket/paren/quote pair selection.
	open, close, forward := matchingPair(r)
	if open != 0 {
		if forward {
			end := findMatchingClose(buf, cp, open, close, n)
			return cp, end
		}
		start := findMatchingOpen(buf, cp, open, close)
		return start, cp + 1
	}

	// Word selection: extend in both directions.
	isWord := buffer.IsWordRune(r)
	lo = cp
	for lo > 0 && isWordChar(buf, lo-1) == isWord {
		lo--
	}
	hi = cp + 1
	for hi < n && isWordChar(buf, hi) == isWord {
		hi++
	}
	return lo, hi
}

func runeAt(buf *buffer.Buffer, cp int) rune {
	s := buf.ReadAt(cp, 1)
	if len(s) == 0 {
		return 0
	}
	return []rune(s)[0]
}

func isWordChar(buf *buffer.Buffer, cp int) bool {
	return buffer.IsWordRune(runeAt(buf, cp))
}

// matchingPair returns the bracket pair for r.
// Returns (open, close, forwardSearch) — forwardSearch true means r is an
// opening delimiter and we search forward; false means r is a closing delimiter.
func matchingPair(r rune) (open, close rune, forward bool) {
	switch r {
	case '(':
		return '(', ')', true
	case ')':
		return '(', ')', false
	case '[':
		return '[', ']', true
	case ']':
		return '[', ']', false
	case '{':
		return '{', '}', true
	case '}':
		return '{', '}', false
	case '<':
		return '<', '>', true
	case '>':
		return '<', '>', false
	case '"', '\'', '`':
		return r, r, true
	}
	return 0, 0, false
}

func findMatchingClose(buf *buffer.Buffer, start int, open, close rune, n int) int {
	depth := 1
	for i := start + 1; i < n; i++ {
		r := runeAt(buf, i)
		if r == open && open != close {
			depth++
		} else if r == close {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return n
}

func findMatchingOpen(buf *buffer.Buffer, end int, open, close rune) int {
	depth := 1
	for i := end - 1; i >= 0; i-- {
		r := runeAt(buf, i)
		if r == close && open != close {
			depth++
		} else if r == open {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return 0
}

func insertText(sess *cmd.Session, f *file.File, text string) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	del := dot.End - dot.Start
	sess.ApplyEdit(f, dot.Start, del, text)
	n := len([]rune(text))
	f.SetDot(buffer.Range{Start: dot.Start + n, End: dot.Start + n})
}

func deleteBackward(sess *cmd.Session, f *file.File) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	if dot.Start != dot.End {
		sess.ApplyEdit(f, dot.Start, dot.End-dot.Start, "")
		f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start})
	} else if dot.Start > 0 {
		sess.ApplyEdit(f, dot.Start-1, 1, "")
		f.SetDot(buffer.Range{Start: dot.Start - 1, End: dot.Start - 1})
	}
}

func deleteForward(sess *cmd.Session, f *file.File) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	n := f.Len()
	if dot.Start != dot.End {
		sess.ApplyEdit(f, dot.Start, dot.End-dot.Start, "")
		f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start})
	} else if dot.Start < n {
		sess.ApplyEdit(f, dot.Start, 1, "")
	}
}

func moveDotChar(f *file.File, delta int, extend bool) {
	dot := f.Dot()
	n := f.Len()
	if !extend && dot.Start != dot.End {
		// Collapse selection to appropriate end.
		if delta < 0 {
			f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start})
		} else {
			f.SetDot(buffer.Range{Start: dot.End, End: dot.End})
		}
		return
	}
	anchor := dot.Start
	if extend {
		anchor = dot.End
	}
	newPos := anchor + delta
	if newPos < 0 {
		newPos = 0
	}
	if newPos > n {
		newPos = n
	}
	if extend {
		lo, hi := dot.Start, newPos
		if lo > hi {
			lo, hi = hi, lo
		}
		f.SetDot(buffer.Range{Start: lo, End: hi})
	} else {
		f.SetDot(buffer.Range{Start: newPos, End: newPos})
	}
}

func moveDotWord(f *file.File, p *pane.Pane, dir int, extend bool) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	buf := f.Buffer()
	n := buf.Len()
	pos := dot.Start
	if dir > 0 {
		pos = dot.End
	}
	pos = skipWordBoundary(buf, pos, dir, n)
	if extend {
		lo, hi := dot.Start, pos
		if lo > hi {
			lo, hi = hi, lo
		}
		f.SetDot(buffer.Range{Start: lo, End: hi})
	} else {
		f.SetDot(buffer.Range{Start: pos, End: pos})
	}
}

func skipWordBoundary(buf *buffer.Buffer, pos, dir, n int) int {
	if dir > 0 {
		// Skip non-word chars, then word chars.
		for pos < n && !isWordChar(buf, pos) {
			pos++
		}
		for pos < n && isWordChar(buf, pos) {
			pos++
		}
	} else {
		for pos > 0 && !isWordChar(buf, pos-1) {
			pos--
		}
		for pos > 0 && isWordChar(buf, pos-1) {
			pos--
		}
	}
	return pos
}

func moveDotVisualLine(f *file.File, p *pane.Pane, dir int, extend bool, winH int32) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	layout := p.Layout()
	if len(layout.Lines) == 0 {
		return
	}
	cp := dot.Start
	lineIdx := layout.LineAt(cp)
	lineIdx += dir
	if lineIdx < 0 {
		lineIdx = 0
	}
	if lineIdx >= len(layout.Lines) {
		lineIdx = len(layout.Lines) - 1
	}
	vl := layout.Lines[lineIdx]
	// Try to preserve horizontal position.
	xTarget := pane.XForCP(f.Buffer(), layout.Lines[layout.LineAt(dot.Start)], dot.Start, p.Face(), p.TabWidth())
	newCP := cpAtTargetX(f.Buffer(), vl, xTarget, p.Face(), p.TabWidth())
	if extend {
		lo, hi := dot.Start, newCP
		if lo > hi {
			lo, hi = hi, lo
		}
		f.SetDot(buffer.Range{Start: lo, End: hi})
	} else {
		f.SetDot(buffer.Range{Start: newCP, End: newCP})
	}
	p.EnsureVisible(f.Dot().Start)
}

func cpAtTargetX(buf *buffer.Buffer, vl pane.VisualLine, targetX int32, face render.Face, tabWidth int) int {
	x := int32(0)
	for cp := vl.Lo; cp < vl.Hi; cp++ {
		s := buf.ReadAt(cp, 1)
		if len(s) == 0 {
			break
		}
		r := []rune(s)[0]
		if r == '\n' {
			return cp
		}
		rw := runeWidthAt(r, x, face, tabWidth)
		if targetX < x+rw/2 {
			return cp
		}
		x += rw
	}
	return vl.Hi
}

func runeWidthAt(r rune, x int32, face render.Face, tabWidth int) int32 {
	if r == '\t' {
		spaceW := int32(face.GlyphAdvance(' '))
		tabW := spaceW * int32(tabWidth)
		if tabW <= 0 {
			tabW = spaceW
		}
		col := x / tabW
		rw := tabW*(col+1) - x
		if rw <= 0 {
			rw = tabW
		}
		return rw
	}
	return int32(face.GlyphAdvance(r))
}

func moveDotAbsolute(f *file.File, pos int, extend bool) {
	dot := f.Dot()
	if extend {
		lo, hi := dot.Start, pos
		if lo > hi {
			lo, hi = hi, lo
		}
		f.SetDot(buffer.Range{Start: lo, End: hi})
	} else {
		f.SetDot(buffer.Range{Start: pos, End: pos})
	}
}

func moveDotLineStart(f *file.File, p *pane.Pane, extend bool) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	layout := p.Layout()
	if len(layout.Lines) == 0 {
		return
	}
	lineIdx := layout.LineAt(dot.Start)
	newPos := layout.Lines[lineIdx].Lo
	moveDotAbsolute(f, newPos, extend)
}

func moveDotLineEnd(f *file.File, p *pane.Pane, extend bool) {
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	dot := f.Dot()
	layout := p.Layout()
	if len(layout.Lines) == 0 {
		return
	}
	lineIdx := layout.LineAt(dot.Start)
	vl := layout.Lines[lineIdx]
	// End of visual line: one before the newline (or Hi for last line).
	newPos := vl.Hi
	if newPos > vl.Lo {
		buf := f.Buffer()
		if newPos > 0 {
			s := buf.ReadAt(newPos-1, 1)
			if len(s) > 0 && []rune(s)[0] == '\n' {
				newPos--
			}
		}
	}
	moveDotAbsolute(f, newPos, extend)
}

func moveDotToTopOfView(f *file.File, p *pane.Pane, winH int32) {
	layout := p.Layout()
	scrollY := p.ScrollY()
	if len(layout.Lines) == 0 {
		return
	}
	// The first line at or below the top of the viewport: the line box
	// containing scrollY, or the next one down when scrollY falls partway
	// through it.
	i := layout.LineAtY(scrollY)
	if layout.Y(i) < scrollY && i+1 < len(layout.Lines) {
		i++
	}
	f.SetDot(buffer.Range{Start: layout.Lines[i].Lo, End: layout.Lines[i].Lo})
}

func visibleLines(p *pane.Pane, winH int32) int {
	lineH := int32(p.Face().Height())
	if lineH <= 0 {
		return 1
	}
	return int(p.PaneH() / lineH)
}
