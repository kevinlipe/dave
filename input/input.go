// Package input handles SDL keyboard and mouse events for dave.
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

	// typeEsc is samterm's typeesc: where the run typed since the last Escape
	// began, so Escape can select it. -1 is "no run open". typeEscPane records
	// where it was typed, indexed like activePane — samterm's is one global
	// because its Escape always acts on the current window, and dave has to
	// notice that focus moved instead.
	typeEsc     int
	typeEscPane int
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
		typeEsc:         -1, // nothing typed yet
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

	// Escape selects the run typed since the last Escape, as samterm does
	// (main.c type(), `if(c==ESC && typeesc>=0)`): type, press Escape, and what
	// you typed is dot and ready for a command. dave used to bind it to "focus
	// the command window", which ctrl-K below already does — and does better,
	// since it goes both ways.
	if sym == sdl.K_ESCAPE {
		h.selectTypedRun()
		return nil
	}

	// Ctrl+K: cycle focus between command window and file pane(s).
	if ctrl && sym == sdl.K_k {
		h.cycleFocus()
		return nil
	}

	if h.activePane < 0 {
		// The command window types too — samterm treats it as one more Text —
		// so the run bookkeeping applies there as well. Enter runs the command
		// and clears the input line, which leaves no run to select.
		switch sym {
		case sdl.K_RETURN, sdl.K_KP_ENTER, sdl.K_HOME, sdl.K_END:
			h.typeEsc = -1
		}
		err := h.cmdWin.HandleKey(ev, h.session)
		switch sym {
		case sdl.K_BACKSPACE, sdl.K_DELETE, sdl.K_u, sdl.K_w:
			h.clampTypedRun(h.cmdWin.Dot().Start)
		}
		return err
	}

	p := h.ActivePane()
	if p == nil || p.File == nil {
		return nil
	}
	f := p.File

	switch sym {
	case sdl.K_RETURN, sdl.K_KP_ENTER:
		h.openTypedRun(f.Dot())
		insertText(h.session, f, "\n")
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_BACKSPACE:
		deleteBackward(h.session, f)
		h.clampTypedRun(f.Dot().Start)
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_DELETE:
		deleteForward(h.session, f)
		h.clampTypedRun(f.Dot().Start)
		p.EnsureVisible(f.Dot().Start)

	// ctrl-U and ctrl-W, sam's and every Unix line discipline's. A plain u or w
	// is left alone here and arrives as text input instead.
	case sdl.K_u:
		if !ctrl {
			break
		}
		deleteBack(h.session, f, LineStartBound)
		h.clampTypedRun(f.Dot().Start)
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_w:
		if !ctrl {
			break
		}
		deleteBack(h.session, f, WordBackBound)
		h.clampTypedRun(f.Dot().Start)
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_TAB:
		h.openTypedRun(f.Dot())
		insertText(h.session, f, "\t")
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_LEFT:
		if ctrl {
			moveDotWord(f, p, -1, shift)
		} else {
			moveDotChar(f, -1, shift)
		}
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_RIGHT:
		if ctrl {
			moveDotWord(f, p, +1, shift)
		} else {
			moveDotChar(f, +1, shift)
		}
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_UP:
		moveDotVisualLine(f, p, -1, shift, winH)

	case sdl.K_DOWN:
		moveDotVisualLine(f, p, +1, shift, winH)

	// Home and End do the job samterm gives ctrl-A and ctrl-E, and samterm
	// flushes the typed run on those two — unlike the arrows and the page
	// keys, which flush the undo run but leave typeesc alone.
	case sdl.K_HOME:
		h.typeEsc = -1
		if ctrl {
			moveDotAbsolute(f, 0, shift)
		} else {
			moveDotLineStart(f, p, shift)
		}
		p.EnsureVisible(f.Dot().Start)

	case sdl.K_END:
		h.typeEsc = -1
		if ctrl {
			moveDotAbsolute(f, f.Len(), shift)
		} else {
			moveDotLineEnd(f, p, shift)
		}
		p.EnsureVisible(f.Dot().Start)

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
		h.openTypedRun(h.cmdWin.Dot())
		h.cmdWin.HandleText(ev)
		return
	}
	p := h.ActivePane()
	if p == nil || p.File == nil {
		return
	}
	h.openTypedRun(p.File.Dot())
	insertText(h.session, p.File, ev.GetText())
	p.EnsureVisible(p.File.Dot().Start)
}

// openTypedRun records where a run of typing began, if one is not open here
// already. samterm keeps typeesc from the first character until something
// flushes it, which is what lets Escape reach back over many keystrokes.
// Typing over a selection starts a fresh run: samterm flushes before it cuts
// (main.c type(), the `a != l->p1` branch).
func (h *Handler) openTypedRun(dot buffer.Range) {
	if dot.Start != dot.End || h.typeEsc < 0 || h.typeEscPane != h.activePane {
		h.typeEsc, h.typeEscPane = dot.Start, h.activePane
	}
}

// clampTypedRun pulls the run's start back when a deletion has passed it, so
// Escape cannot select text that is no longer there. samterm does the same
// after a backspace, ctrl-U or ctrl-W (`if(typeesc >= l->p0) typeesc = l->p0`).
func (h *Handler) clampTypedRun(pos int) {
	if h.typeEsc > pos {
		h.typeEsc = pos
	}
}

// selectTypedRun is Escape: dot becomes the text typed since the last Escape,
// and the run ends. With no run open it does nothing, which is samterm's
// behaviour too — its Escape is guarded by `typeesc >= 0`.
func (h *Handler) selectTypedRun() {
	if h.typeEsc < 0 || h.typeEscPane != h.activePane {
		return
	}
	start := h.typeEsc
	h.typeEsc = -1

	if h.activePane < 0 {
		h.cmdWin.ExtendDrag(start, h.cmdWin.Dot().Start)
		return
	}
	p := h.ActivePane()
	if p == nil || p.File == nil {
		return
	}
	f := p.File
	end := f.Dot().Start
	if end > f.Len() {
		end = f.Len()
	}
	if start > end {
		start = end
	}
	f.SetDot(buffer.Range{Start: start, End: end})
}

// --- Mouse -------------------------------------------------------------------

// HandleMouseDown processes a mouse button press.
// paneRects are the bounding rectangles of each file pane (in window coords).
// cmdRect is the command window's bounding rect.
func (h *Handler) HandleMouseDown(ev *sdl.MouseButtonEvent, cmdRect sdl.Rect, paneRects []sdl.Rect) {
	x, y := ev.X, ev.Y
	// Any button ends the typed run, as samterm does before it looks at what
	// the button meant (`if(mousep->buttons) flushtyping(1)`).
	h.typeEsc = -1

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
			h.dragStart = cp
			h.dragPane = idx
		} else {
			p := h.panes[idx]
			if p.File != nil {
				cp = p.CPAtXY(x-paneRects[idx].X, y-paneRects[idx].Y)
				f := p.File
				f.SetDot(buffer.Range{Start: cp, End: cp})
				h.session.Current = f
				h.dragStart = cp
				h.dragPane = idx
			}
		}

		h.dragging = true

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
			if h.panes[idx].File != nil {
				h.session.Current = h.panes[idx].File
			}
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
	if p.File == nil {
		return
	}
	rect := paneRects[h.dragPane]

	// Auto-scroll and clamp when dragging above or below the pane body.
	localY := y - rect.Y
	if y < rect.Y {
		p.ScrollBy(-1)
		localY = 1 // clamp to top of body
	} else if y >= rect.Y+rect.H {
		p.ScrollBy(1)
		localY = rect.H - 1 // clamp to bottom of body
	}

	cp := p.CPAtXY(x-rect.X, localY)
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
	if p.File == nil {
		return
	}
	f := p.File
	if err := f.EnsureLoaded(); err != nil {
		return
	}
	lo, hi := WordBounds(f.Buffer(), cp)
	f.SetDot(buffer.Range{Start: lo, End: hi})
}

// cycleFocus advances keyboard focus in order:
// command window → pane 0 → pane 1 (if open) → command window.
// Mirrors what a mouse click does: the cycled-to pane becomes active and current.
func (h *Handler) cycleFocus() {
	if len(h.panes) == 0 {
		return
	}
	switch {
	case h.activePane < 0:
		// command window → pane 0
		h.activePane = 0
		h.currentFilePane = 0
		if h.panes[0].File != nil {
			h.session.Current = h.panes[0].File
		}
	case h.activePane == 0 && len(h.panes) > 1:
		// pane 0 → pane 1
		h.activePane = 1
		h.currentFilePane = 1
		if h.panes[1].File != nil {
			h.session.Current = h.panes[1].File
		}
	default:
		// pane 0 (sole pane) or pane 1 → command window
		h.activePane = -1
	}
}
