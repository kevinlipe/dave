package input

import (
	"dave/buffer"
	"dave/cmd"
	"dave/render"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
)

// CmdWindow is the always-visible command window at the top of the dave window.
//
// It is a single editable text buffer. There is no separate input line or
// output log — the buffer accumulates all typed text and command output, and
// any line can be re-executed by placing the cursor on it and pressing Enter.
//
// Pressing Enter with a non-null selection executes the selected text.
// Pressing Enter with a null cursor executes the current line.
// Command output is appended to the end of the buffer.
type CmdWindow struct {
	buf      *buffer.Buffer
	dot      buffer.Range // cursor/selection within buf
	scrollY  int32
	lines    []string // cached result of strings.Split(buf.String(), "\n")
	starts   []int    // codepoint offset of each line in lines
	linesGen int      // buf.Gen() when lines was last computed
}

// NewCmdWindow creates an empty command window.
func NewCmdWindow() *CmdWindow {
	return &CmdWindow{
		buf:      buffer.New(),
		linesGen: -1, // force getLines() to populate on first call (buf.Gen() starts at 0)
	}
}

// Buf returns the command window buffer.
func (cw *CmdWindow) Buf() *buffer.Buffer { return cw.buf }

// getLines returns the buffer content split into lines, using a cached result
// when the buffer hasn't changed since the last call.
func (cw *CmdWindow) getLines() []string {
	cw.ensureLines()
	return cw.lines
}

// lineStarts returns the codepoint offset at which each line begins. Drawing
// needs it to map a line back onto dot, and having it cached means a line that
// is scrolled out of view costs nothing to skip over.
func (cw *CmdWindow) lineStarts() []int {
	cw.ensureLines()
	return cw.starts
}

// lineAt returns the index of the line containing codepoint offset cp.
func (cw *CmdWindow) lineAt(cp int) int {
	starts := cw.lineStarts()
	// The first line starting after cp; the one before it contains cp.
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > cp })
	if i > 0 {
		i--
	}
	return i
}

func (cw *CmdWindow) ensureLines() {
	gen := cw.buf.Gen()
	if gen == cw.linesGen {
		return
	}
	cw.lines = strings.Split(cw.buf.String(), "\n")
	cw.starts = make([]int, len(cw.lines))
	off := 0
	for i, ln := range cw.lines {
		cw.starts[i] = off
		off += utf8.RuneCountInString(ln) + 1 // +1 for the \n
	}
	cw.linesGen = gen
}

// Dot returns the current selection.
func (cw *CmdWindow) Dot() buffer.Range { return cw.dot }

// ScrollBy scrolls by delta lines.
func (cw *CmdWindow) ScrollBy(delta int32) {
	cw.scrollY += delta * 20
	if cw.scrollY < 0 {
		cw.scrollY = 0
	}
}

// ScrollY returns the current scroll offset.
func (cw *CmdWindow) ScrollY() int32 { return cw.scrollY }

// EnsureVisible scrolls so that the cursor line is visible within a command
// window whose outer rect has height rectH pixels. The border and top/bottom
// pad are subtracted so the cursor never overlaps the border.
func (cw *CmdWindow) EnsureVisible(lineH, rectH int32) {
	if lineH <= 0 || rectH <= 0 {
		return
	}
	viewH := rectH - 2*render.BorderW - 2*render.HeaderPad
	if viewH <= 0 {
		return
	}
	// Find the line index containing dot. Scanning for it used to convert
	// every line above the cursor to runes, on every keystroke, and the
	// cursor is normally at the end of the buffer.
	cursorLine := cw.lineAt(cw.dot.Start)
	cursorTop := int32(cursorLine) * lineH
	cursorBot := cursorTop + lineH
	if cursorTop < cw.scrollY || cursorBot > cw.scrollY+viewH {
		cw.scrollY = cursorBot - viewH
	}
	if cw.scrollY < 0 {
		cw.scrollY = 0
	}
}

// HandleKey handles a KEYDOWN event targeting the command window.
// Returns cmd.ErrQuit if the user typed a quit command.
func (cw *CmdWindow) HandleKey(ev *sdl.KeyboardEvent, sess *cmd.Session) error {
	if ev.Type != sdl.KEYDOWN {
		return nil
	}
	sym := ev.Keysym.Sym
	ctrl := ev.Keysym.Mod&sdl.KMOD_CTRL != 0
	switch sym {
	case sdl.K_RETURN, sdl.K_KP_ENTER:
		if err := cw.execute(sess); err != nil {
			return err
		}
	case sdl.K_BACKSPACE:
		cw.deleteBackward()
	case sdl.K_DELETE:
		cw.deleteForward()
	case sdl.K_LEFT:
		cw.moveCursor(-1, false)
	case sdl.K_RIGHT:
		cw.moveCursor(+1, false)
	case sdl.K_HOME:
		cw.dot = buffer.Range{}
	case sdl.K_END:
		n := cw.buf.Len()
		cw.dot = buffer.Range{Start: n, End: n}
	// ctrl-U and ctrl-W work here too: sam's command window is a Text like any
	// other, and its keys are the same ones.
	case sdl.K_u:
		if ctrl {
			cw.deleteBackTo(LineStartBound)
		}
	case sdl.K_w:
		if ctrl {
			cw.deleteBackTo(WordBackBound)
		}
	}
	return nil
}

// deleteBackTo removes the text between bound(dot) and dot, or the selection if
// there is one — the command window's half of input.deleteBack. Its buffer
// holds the output log above the line being typed, and nothing stops a deletion
// running up into it, which is how backspace has always behaved here.
func (cw *CmdWindow) deleteBackTo(bound func(*buffer.Buffer, int) int) {
	if cw.dot.Start != cw.dot.End {
		cw.deleteSelection()
		return
	}
	start := bound(cw.buf, cw.dot.Start)
	if start >= cw.dot.Start {
		return
	}
	cw.buf.Delete(start, cw.dot.Start-start)
	cw.dot = buffer.Range{Start: start, End: start}
}

// HandleText inserts printable text at the cursor.
func (cw *CmdWindow) HandleText(ev *sdl.TextInputEvent) {
	text := ev.GetText()
	if text == "" {
		return
	}
	cw.deleteSelection()
	runes := []rune(text)
	cw.buf.Insert(cw.dot.Start, text)
	pos := cw.dot.Start + len(runes)
	cw.dot = buffer.Range{Start: pos, End: pos}
}

// CPAtXY returns the codepoint offset closest to pixel (x, y) within the
// command window. x and y are relative to the command window's top-left corner.
func (cw *CmdWindow) CPAtXY(x, y int32, face render.Face) int {
	lineH := int32(face.Height())
	if lineH <= 0 {
		return cw.buf.Len()
	}

	// Mirror the Draw layout: text begins at (ScrollBarW+PanePad,
	// BorderW+HeaderPad-scrollY) relative to the cmdwin rect's top-left.
	relY := y - render.BorderW - render.HeaderPad + cw.scrollY
	if relY < 0 {
		relY = 0
	}
	lineIdx := int(relY / lineH)

	lines := cw.getLines()
	if lineIdx >= len(lines) {
		lineIdx = len(lines) - 1
	}

	// Codepoint offset of the start of lineIdx, from the cached table rather
	// than by walking every line above it.
	lineStart := cw.lineStarts()[lineIdx]

	// Find character within the line based on x.
	relX := x - render.ScrollBarW - render.PanePad
	if relX <= 0 {
		return lineStart
	}
	lineRunes := []rune(lines[lineIdx])
	cur := int32(0)
	for i, r := range lineRunes {
		rw := int32(face.GlyphAdvance(r))
		if relX < cur+rw/2 {
			return lineStart + i
		}
		cur += rw
	}
	return lineStart + len(lineRunes)
}

// SetDotStart places a null cursor at cp.
func (cw *CmdWindow) SetDotStart(cp int) {
	n := cw.buf.Len()
	if cp > n {
		cp = n
	}
	cw.dot = buffer.Range{Start: cp, End: cp}
}

// ExtendDrag extends the selection from anchor to cp.
func (cw *CmdWindow) ExtendDrag(anchor, cp int) {
	lo, hi := anchor, cp
	if lo > hi {
		lo, hi = hi, lo
	}
	cw.dot = buffer.Range{Start: lo, End: hi}
}

// Snarf copies the current selection to the system clipboard.
func (cw *CmdWindow) Snarf() {
	if cw.dot.Start != cw.dot.End {
		sdl.SetClipboardText(cw.buf.ReadAt(cw.dot.Start, cw.dot.End-cw.dot.Start))
	}
}

// Cut copies the current selection to the system clipboard and deletes it.
func (cw *CmdWindow) Cut() {
	cw.Snarf()
	cw.deleteSelection()
}

// Paste inserts the system clipboard contents at the cursor.
func (cw *CmdWindow) Paste() {
	text, err := sdl.GetClipboardText()
	if err != nil || text == "" {
		return
	}
	cw.deleteSelection()
	runes := []rune(text)
	cw.buf.Insert(cw.dot.Start, text)
	pos := cw.dot.Start + len(runes)
	cw.dot = buffer.Range{Start: pos, End: pos}
}

// AppendOutput appends text to the end of the command window buffer and moves
// the cursor to the end. This is the same path used by execute() for command output.
func (cw *CmdWindow) AppendOutput(text string) {
	if text == "" {
		return
	}
	n := cw.buf.Len()
	if n > 0 && cw.buf.RuneAt(n-1) != '\n' {
		cw.buf.Insert(n, "\n")
		n++
	}
	cw.buf.Insert(n, text)
	n = cw.buf.Len()
	cw.dot = buffer.Range{Start: n, End: n}
}

// execute runs the selected text (if non-null) or the current line (if null cursor)
// as a dave command. Output is appended to the end of the buffer.
func (cw *CmdWindow) execute(sess *cmd.Session) error {
	var cmdText string
	if cw.dot.Start != cw.dot.End {
		cmdText = cw.buf.ReadAt(cw.dot.Start, cw.dot.End-cw.dot.Start)
	} else {
		cmdText = cw.currentLine()
	}
	cmdText = strings.TrimSpace(cmdText)
	if cmdText == "" {
		// An empty command line is sam's \n command: print the line after dot
		// and move there, so Enter on a blank line pages down the file.
		return sess.ExecString("\n")
	}

	// "Config" is a GUI command handled by the event loop, not the text engine.
	if cmdText == "Config" {
		sess.ConfigRequest = true
		return nil
	}

	// Both streams land in the command window, which is what sam does once it
	// is downloaded: termwrite puts dprint's output there too, so there is
	// nowhere for a diagnostic to go but the window.
	var sb strings.Builder
	origOut, origDiag := sess.Output, sess.Diag
	sess.Output, sess.Diag = &sb, &sb
	err := sess.ExecString(cmdText)
	sess.Output, sess.Diag = origOut, origDiag

	output := sb.String()
	if err != nil && err != cmd.ErrQuit {
		output += "? " + err.Error() + "\n"
	}
	if output != "" {
		cw.AppendOutput(output)
	} else {
		// No output: still move cursor to a fresh line at end of buffer.
		n := cw.buf.Len()
		if n > 0 && cw.buf.RuneAt(n-1) != '\n' {
			cw.buf.Insert(n, "\n")
			n++
		}
		cw.dot = buffer.Range{Start: n, End: n}
	}

	if err == cmd.ErrQuit {
		return err
	}
	return nil
}

// currentLine returns the text of the logical line containing dot.Start.
func (cw *CmdWindow) currentLine() string {
	n := cw.buf.Len()
	pos := cw.dot.Start

	// Find start of line.
	lo := pos
	for lo > 0 && cw.buf.RuneAt(lo-1) != '\n' {
		lo--
	}

	// Find end of line (stop before the newline).
	hi := pos
	for hi < n && cw.buf.RuneAt(hi) != '\n' {
		hi++
	}

	return cw.buf.ReadAt(lo, hi-lo)
}

// deleteSelection deletes the selected text.
func (cw *CmdWindow) deleteSelection() {
	if cw.dot.Start == cw.dot.End {
		return
	}
	cw.buf.Delete(cw.dot.Start, cw.dot.End-cw.dot.Start)
	cw.dot = buffer.Range{Start: cw.dot.Start, End: cw.dot.Start}
}

func (cw *CmdWindow) deleteBackward() {
	if cw.dot.Start != cw.dot.End {
		cw.deleteSelection()
		return
	}
	if cw.dot.Start > 0 {
		cw.buf.Delete(cw.dot.Start-1, 1)
		pos := cw.dot.Start - 1
		cw.dot = buffer.Range{Start: pos, End: pos}
	}
}

func (cw *CmdWindow) deleteForward() {
	if cw.dot.Start != cw.dot.End {
		cw.deleteSelection()
		return
	}
	n := cw.buf.Len()
	if cw.dot.Start < n {
		cw.buf.Delete(cw.dot.Start, 1)
	}
}

func (cw *CmdWindow) moveCursor(delta int, extend bool) {
	dot := cw.dot
	n := cw.buf.Len()
	newPos := dot.Start + delta
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
		cw.dot = buffer.Range{Start: lo, End: hi}
	} else {
		cw.dot = buffer.Range{Start: newPos, End: newPos}
	}
}

// Draw renders the command window into rect.
func (cw *CmdWindow) Draw(ren *sdl.Renderer, rect sdl.Rect, face render.Face, pal render.Palette, active bool) error {
	lineH := int32(face.Height())

	// Background.
	ren.SetDrawColor(pal.CmdwinBG.R, pal.CmdwinBG.G, pal.CmdwinBG.B, 255)
	ren.FillRect(&rect)

	// Scrollbar on left: track uses background, thumb uses accent color.
	sbRect := sdl.Rect{X: rect.X, Y: rect.Y, W: render.ScrollBarW, H: rect.H}
	ren.SetDrawColor(pal.CmdwinBG.R, pal.CmdwinBG.G, pal.CmdwinBG.B, 255)
	ren.FillRect(&sbRect)
	// Right-edge border in thumb color.
	ren.SetDrawColor(pal.CmdwinScroll.R, pal.CmdwinScroll.G, pal.CmdwinScroll.B, 255)
	ren.DrawLine(rect.X+render.ScrollBarW-1, rect.Y, rect.X+render.ScrollBarW-1, rect.Y+rect.H-1)

	// Border drawn after scrollbar so it sits on top (not overwritten).
	if active {
		ren.SetDrawColor(pal.CmdwinScroll.R, pal.CmdwinScroll.G, pal.CmdwinScroll.B, 255)
		render.DrawBorder(ren, rect, render.BorderW)
	} else {
		ren.SetDrawColor(pal.CmdwinBorder.R, pal.CmdwinBorder.G, pal.CmdwinBorder.B, 255)
		ren.DrawRect(&rect)
	}

	textX := rect.X + render.ScrollBarW + render.PanePad
	textY := rect.Y + render.BorderW + render.HeaderPad - cw.scrollY

	// Interior region — inside the border strip on all four sides.
	// All text drawing is clipped to this so nothing bleeds into the border.
	inner := sdl.Rect{
		X: rect.X + render.BorderW,
		Y: rect.Y + render.BorderW,
		W: rect.W - 2*render.BorderW,
		H: rect.H - 2*render.BorderW,
	}

	// Render the buffer line by line.
	if cw.buf.Len() == 0 {
		ren.SetClipRect(&inner)
		render.DrawCursor(ren, textX, textY, lineH, pal.CmdwinFG)
		ren.SetClipRect(nil)
		return nil
	}

	lines := cw.getLines()
	starts := cw.lineStarts()
	// Always keep at least one line so the cursor has somewhere to sit.
	// Do not strip the trailing empty element: the cursor may be on that line.

	// Draw scrollbar thumb proportional to scroll position.
	totalH := int32(len(lines)) * lineH
	bodyH := rect.H
	if totalH > bodyH && bodyH > 0 {
		thumbY := int32(float64(cw.scrollY) / float64(totalH) * float64(bodyH))
		thumbH := int32(float64(bodyH) / float64(totalH) * float64(bodyH))
		if thumbH < 4 {
			thumbH = 4
		}
		if thumbY+thumbH > bodyH {
			thumbY = bodyH - thumbH
		}
		ren.SetDrawColor(pal.CmdwinScroll.R, pal.CmdwinScroll.G, pal.CmdwinScroll.B, 255)
		ren.FillRect(&sdl.Rect{X: rect.X, Y: rect.Y + thumbY, W: render.ScrollBarW, H: thumbH})
	}

	// Clip to the border-free interior so text can't bleed onto the border.
	ren.SetClipRect(&inner)
	dot := cw.dot

	// Only visible lines are drawn. The command window is append-only, so the
	// buffer holds every line of output for the whole session while four or
	// five of them are on screen; walking all of them per frame — and
	// converting each to runes before asking whether it was visible — made
	// every repaint cost the length of the scrollback.
	//
	// Every line is lineH tall, so the first visible one is arithmetic rather
	// than a scan. The bound is conservative and the per-line check below
	// still decides.
	first := 0
	if d := rect.Y - textY - lineH; d > 0 {
		first = int(d / lineH)
		if first > len(lines) {
			first = len(lines)
		}
	}
	for i := first; i < len(lines); i++ {
		y := textY + int32(i)*lineH
		if y > rect.Y+rect.H {
			break // past the bottom; nothing below is visible
		}
		if y+lineH < rect.Y {
			continue // above the top
		}
		line := lines[i]
		lineStart := starts[i] // codepoint offset of the start of this line
		lineRunes := []rune(line)
		lineEnd := lineStart + len(lineRunes) // not including the \n

		// Draw selection highlight for overlap with dot.
		if dot.Start != dot.End {
			selLo := dot.Start
			selHi := dot.End
			if selHi > lineStart && selLo <= lineEnd {
				clipLo := selLo
				if clipLo < lineStart {
					clipLo = lineStart
				}
				clipHi := selHi
				if clipHi > lineEnd {
					clipHi = lineEnd
				}
				x0 := textX + int32(face.MeasureText(string(lineRunes[:clipLo-lineStart])))
				x1 := textX + int32(face.MeasureText(string(lineRunes[:clipHi-lineStart])))
				if x1 > x0 {
					sel := pal.SelectionBG
					ren.SetDrawColor(sel.R, sel.G, sel.B, 255)
					ren.FillRect(&sdl.Rect{X: x0, Y: y, W: x1 - x0, H: lineH})
				}
			}
		}

		// Draw cursor (null dot on this line).
		if dot.Start == dot.End && dot.Start >= lineStart && dot.Start <= lineEnd {
			cx := textX + int32(face.MeasureText(string(lineRunes[:dot.Start-lineStart])))
			render.DrawCursor(ren, cx, y, lineH, pal.CmdwinFG)
		}

		// Draw text.
		if err := face.DrawText(line, textX, y, pal.CmdwinFG); err != nil {
			return err
		}

	}

	ren.SetClipRect(nil)
	return nil
}
