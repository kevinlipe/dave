// Package input handles SDL keyboard and mouse events for Dave.
//
// Dependency check: input imports cmd for ExecString. cmd imports
// buffer/file/addr/regexp — none of which import input — so there is no cycle.
package input

import (
	"dave/buffer"
	"dave/cmd"
	"dave/pane"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// Handler routes SDL events to the correct pane or command window.
type Handler struct {
	session         *cmd.Session
	cmdWin          *CmdWindow
	panes           []*pane.Pane // 0, 1, or 2 file panes
	activePane      int          // index into panes; -1 = command window active (keyboard focus)
	currentFilePane int          // last clicked file pane; -1 if none yet (never cleared by cmdwin click)
	face            render.Face
	pal             render.Palette

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
		session:         sess,
		cmdWin:          cmdWin,
		face:            face,
		pal:             pal,
		activePane:      -1, // start with command window focused
		currentFilePane: -1, // no file pane selected yet
	}
}

// CmdWin returns the command window.
func (h *Handler) CmdWin() *CmdWindow { return h.cmdWin }

// IsCmdWinActive reports whether the command window is focused.
func (h *Handler) IsCmdWinActive() bool { return h.activePane < 0 }

// SetPanes updates the active pane list.
func (h *Handler) SetPanes(panes []*pane.Pane) {
	h.panes = panes
}

// ActivePane returns the currently focused pane (keyboard target), or nil if
// the command window has keyboard focus.
func (h *Handler) ActivePane() *pane.Pane {
	if h.activePane < 0 || h.activePane >= len(h.panes) {
		return nil
	}
	return h.panes[h.activePane]
}

// CurrentFilePane returns the most recently clicked file pane.  This is the
// pane the command window operates on and the one shown with the heavy border.
// Clicking the command window does NOT change the current file pane.
// Returns nil if no file pane has ever been clicked.
func (h *Handler) CurrentFilePane() *pane.Pane {
	if h.currentFilePane < 0 || h.currentFilePane >= len(h.panes) {
		if len(h.panes) > 0 {
			return h.panes[0]
		}
		return nil
	}
	return h.panes[h.currentFilePane]
}

// SetFace updates the font face used for hit-testing.
func (h *Handler) SetFace(face render.Face) { h.face = face }

// SetPalette updates the color palette.
func (h *Handler) SetPalette(pal render.Palette) { h.pal = pal }

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
		// Left-clicking a file pane makes it current; cmdwin click leaves it unchanged.
		if idx >= 0 {
			h.currentFilePane = idx
		}

		var cp int
		if idx < 0 {
			cp = h.cmdWin.CPAtXY(x-cmdRect.X, y-cmdRect.Y, h.face)
			h.cmdWin.SetDotStart(cp)
		} else {
			p := h.panes[idx]
			cp = p.CPAtXY(x-paneRects[idx].X, y-paneRects[idx].Y)
			f := p.File
			f.SetDot(buffer.Range{Start: cp, End: cp})
			h.session.Current = f
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
		// Right-clicking a file pane makes it current (so the menu result
		// goes to the right pane) without stealing keyboard focus from cmdwin.
		idx := h.hitTest(x, y, cmdRect, paneRects)
		if idx >= 0 {
			h.activePane = idx
			h.currentFilePane = idx
			h.session.Current = h.panes[idx].File
		}
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
		cp := h.cmdWin.CPAtXY(x-cmdRect.X, y-cmdRect.Y, h.face)
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
	dy := -ev.Y
	if h.activePane < 0 {
		h.cmdWin.ScrollBy(dy)
		return
	}
	p := h.ActivePane()
	if p != nil {
		p.ScrollBy(dy * 3)
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
	lo, hi := WordBounds(f.Buffer(), cp)
	f.SetDot(buffer.Range{Start: lo, End: hi})
}
