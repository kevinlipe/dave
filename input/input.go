// Package input handles SDL keyboard and mouse events for Dave.
//
// Dependency check: input imports cmd for ExecString. cmd imports
// buffer/file/addr/regexp — none of which import input — so there is no cycle.
package input

import (
	"dave/buffer"
	"dave/cmd"
	"dave/file"
	"dave/pane"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// Handler routes SDL events to the correct pane or command window.
type Handler struct {
	session    *cmd.Session
	cmdWin     *CmdWindow
	panes      []*pane.Pane // 0, 1, or 2 file panes
	activePane int          // index into panes; -1 = command window active
	face       render.Face
	pal        render.Palette

	// Mouse drag state.
	dragging     bool
	dragStart    int // codepoint where drag began
	dragPane     int // pane index where drag started (-1 = cmdwin)
	doubleTimer  uint32
	lastClickPos int
	lastClickCP  int
}

// NewHandler creates an input handler wired to the given session and UI.
func NewHandler(sess *cmd.Session, cmdWin *CmdWindow, face render.Face, pal render.Palette) *Handler {
	return &Handler{
		session:    sess,
		cmdWin:     cmdWin,
		face:       face,
		pal:        pal,
		activePane: -1, // start with command window focused
	}
}

// SetPanes updates the active pane list.
func (h *Handler) SetPanes(panes []*pane.Pane) {
	h.panes = panes
}

// ActivePane returns the currently focused pane, or nil if the command
// window is active.
func (h *Handler) ActivePane() *pane.Pane {
	if h.activePane < 0 || h.activePane >= len(h.panes) {
		return nil
	}
	return h.panes[h.activePane]
}

// --- Keyboard ----------------------------------------------------------------

// HandleKey processes a KEYDOWN event. Returns cmd.ErrQuit if the user quit.
func (h *Handler) HandleKey(ev *sdl.KeyboardEvent, winW, winH int32) error {
	if ev.Type != sdl.KEYDOWN {
		return nil
	}

	sym := ev.Keysym.Sym
	mod := ev.Keysym.Mod
	ctrl := mod&sdl.KMOD_CTRL != 0
	shift := mod&sdl.KMOD_SHIFT != 0

	// Escape: focus command window.
	if sym == sdl.K_ESCAPE {
		h.activePane = -1
		return nil
	}

	if h.activePane < 0 {
		return h.cmdWin.HandleKey(ev, h.session)
	}

	p := h.ActivePane()
	if p == nil {
		return nil
	}
	f := p.File

	switch sym {
	case sdl.K_RETURN, sdl.K_KP_ENTER:
		insertText(h.session, f, "\n")
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_BACKSPACE:
		deleteBackward(h.session, f)
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_DELETE:
		deleteForward(h.session, f)
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_TAB:
		insertText(h.session, f, "\t")
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_LEFT:
		if ctrl {
			moveDotWord(f, p, -1, shift)
		} else {
			moveDotChar(f, -1, shift)
		}
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_RIGHT:
		if ctrl {
			moveDotWord(f, p, +1, shift)
		} else {
			moveDotChar(f, +1, shift)
		}
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_UP:
		moveDotVisualLine(f, p, -1, shift, winH)

	case sdl.K_DOWN:
		moveDotVisualLine(f, p, +1, shift, winH)

	case sdl.K_HOME:
		if ctrl {
			moveDotAbsolute(f, 0, shift)
		} else {
			moveDotLineStart(f, p, shift)
		}
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_END:
		if ctrl {
			moveDotAbsolute(f, f.Len(), shift)
		} else {
			moveDotLineEnd(f, p, shift)
		}
		p.EnsureVisible(f.Dot().Start, winH)

	case sdl.K_PAGEUP:
		p.ScrollBy(-int32(visibleLines(p, winH)))
		moveDotToTopOfView(f, p, winH)

	case sdl.K_PAGEDOWN:
		p.ScrollBy(int32(visibleLines(p, winH)))
		moveDotToTopOfView(f, p, winH)
	}
	return nil
}

// HandleText processes an SDL_TEXTINPUT event (printable character input).
func (h *Handler) HandleText(ev *sdl.TextInputEvent) {
	if h.activePane < 0 {
		h.cmdWin.HandleText(ev)
		return
	}
	p := h.ActivePane()
	if p == nil {
		return
	}
	insertText(h.session, p.File, ev.GetText())
}

// --- Mouse -------------------------------------------------------------------

// HandleMouseDown processes a mouse button press.
// paneRects are the bounding rectangles of each file pane (in window coords).
// cmdRect is the command window's bounding rect.
func (h *Handler) HandleMouseDown(ev *sdl.MouseButtonEvent, cmdRect sdl.Rect, paneRects []sdl.Rect) {
	x, y := ev.X, ev.Y

	switch ev.Button {
	case sdl.BUTTON_LEFT:
		// Determine which pane was clicked.
		idx := h.hitTest(x, y, cmdRect, paneRects)
		h.activePane = idx

		var cp int
		if idx < 0 {
			cp = h.cmdWin.CPAtXY(x-cmdRect.X, y-cmdRect.Y)
			h.cmdWin.SetDotStart(cp)
		} else {
			p := h.panes[idx]
			cp = p.CPAtXY(x-paneRects[idx].X, y-paneRects[idx].Y)
			f := p.File
			f.SetDot(buffer.Range{Start: cp, End: cp})
		}

		h.dragging = true
		h.dragStart = cp
		h.dragPane = idx

		// Double-click detection.
		now := sdl.GetTicks()
		if ev.Clicks == 2 {
			h.handleDoubleClick(idx, cp, paneRects)
		}
		h.doubleTimer = now
		h.lastClickCP = cp

	case sdl.BUTTON_RIGHT:
		// Right-click: buffer menu (handled by caller via MenuRequest).
	}
}

// HandleMouseUp ends a drag.
func (h *Handler) HandleMouseUp(ev *sdl.MouseButtonEvent) {
	if ev.Button == sdl.BUTTON_LEFT {
		h.dragging = false
	}
}

// HandleMouseMotion extends the selection during a drag.
func (h *Handler) HandleMouseMotion(ev *sdl.MouseMotionEvent, cmdRect sdl.Rect, paneRects []sdl.Rect) {
	if !h.dragging {
		return
	}
	x, y := ev.X, ev.Y

	if h.dragPane < 0 {
		cp := h.cmdWin.CPAtXY(x-cmdRect.X, y-cmdRect.Y)
		h.cmdWin.ExtendDrag(h.dragStart, cp)
		return
	}
	if h.dragPane >= len(h.panes) {
		return
	}
	p := h.panes[h.dragPane]
	rect := paneRects[h.dragPane]
	cp := p.CPAtXY(x-rect.X, y-rect.Y)
	f := p.File
	lo, hi := h.dragStart, cp
	if lo > hi {
		lo, hi = hi, lo
	}
	f.SetDot(buffer.Range{Start: lo, End: hi})
}

// HandleScroll scrolls the active pane.
func (h *Handler) HandleScroll(ev *sdl.MouseWheelEvent) {
	dy := ev.Y
	if ev.Direction == sdl.MOUSEWHEEL_FLIPPED {
		dy = -dy
	}
	if h.activePane < 0 {
		h.cmdWin.ScrollBy(-dy)
		return
	}
	p := h.ActivePane()
	if p != nil {
		p.ScrollBy(-dy * 3)
	}
}

// HandleScrollbarClick handles a left-click in a scrollbar.
func (h *Handler) HandleScrollbarClick(paneIdx int, y, bodyH int32) {
	if paneIdx < 0 || paneIdx >= len(h.panes) {
		return
	}
	h.panes[paneIdx].ScrollbarJump(y, bodyH)
}

// --- hit test ----------------------------------------------------------------

// hitTest returns the pane index (-1 = command window) for window position (x, y).
func (h *Handler) hitTest(x, y int32, cmdRect sdl.Rect, paneRects []sdl.Rect) int {
	if inRect(x, y, cmdRect) {
		return -1
	}
	for i, r := range paneRects {
		if inRect(x, y, r) {
			return i
		}
	}
	return -1
}

func inRect(x, y int32, r sdl.Rect) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// --- double-click selection --------------------------------------------------

func (h *Handler) handleDoubleClick(paneIdx, cp int, paneRects []sdl.Rect) {
	if paneIdx < 0 || paneIdx >= len(h.panes) {
		return
	}
	p := h.panes[paneIdx]
	f := p.File
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	lo, hi := wordBounds(f.Buffer(), cp)
	f.SetDot(buffer.Range{Start: lo, End: hi})
}

// wordBounds returns the start and end of the word (or bracket pair) at cp.
func wordBounds(buf *buffer.Buffer, cp int) (lo, hi int) {
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
	isWord := isWordRune(r)
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
	return isWordRune(runeAt(buf, cp))
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

// --- cursor movement helpers -------------------------------------------------

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
	p.EnsureVisible(f.Dot().Start, winH)
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
	for _, vl := range layout.Lines {
		if vl.Y >= scrollY {
			f.SetDot(buffer.Range{Start: vl.Lo, End: vl.Lo})
			return
		}
	}
}

func visibleLines(p *pane.Pane, winH int32) int {
	hdrH := pane.HeaderH(p.Face())
	bodyH := winH - hdrH
	lineH := int32(p.Face().Height())
	if lineH <= 0 {
		return 1
	}
	return int(bodyH / lineH)
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
