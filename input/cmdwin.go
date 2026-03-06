package input

import (
	"dave/buffer"
	"dave/cmd"
	"dave/render"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// CmdWindow is the always-visible command window at the top of the Dave window.
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
	linesGen int      // buf.Gen() when lines was last computed
}

// NewCmdWindow creates an empty command window.
func NewCmdWindow() *CmdWindow {
	return &CmdWindow{
		buf: buffer.New(),
	}
}

// Buf returns the command window buffer.
func (cw *CmdWindow) Buf() *buffer.Buffer { return cw.buf }

// getLines returns the buffer content split into lines, using a cached result
// when the buffer hasn't changed since the last call.
func (cw *CmdWindow) getLines() []string {
	if gen := cw.buf.Gen(); gen != cw.linesGen {
		cw.lines = strings.Split(cw.buf.String(), "\n")
		cw.linesGen = gen
	}
	return cw.lines
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

// HandleKey handles a KEYDOWN event targeting the command window.
// Returns cmd.ErrQuit if the user typed a quit command.
func (cw *CmdWindow) HandleKey(ev *sdl.KeyboardEvent, sess *cmd.Session) error {
	if ev.Type != sdl.KEYDOWN {
		return nil
	}
	sym := ev.Keysym.Sym
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
	}
	return nil
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

	// Mirror the Draw layout: text begins at (ScrollBarW+PanePad, HeaderPad-scrollY).
	relY := y - render.HeaderPad + cw.scrollY
	if relY < 0 {
		relY = 0
	}
	lineIdx := int(relY / lineH)

	lines := cw.getLines()
	if lineIdx >= len(lines) {
		lineIdx = len(lines) - 1
	}

	// Codepoint offset of the start of lineIdx.
	lineStart := 0
	for i := 0; i < lineIdx; i++ {
		lineStart += len([]rune(lines[i])) + 1 // +1 for '\n'
	}

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
// as a Dave command. Output is appended to the end of the buffer.
func (cw *CmdWindow) execute(sess *cmd.Session) error {
	var cmdText string
	if cw.dot.Start != cw.dot.End {
		cmdText = cw.buf.ReadAt(cw.dot.Start, cw.dot.End-cw.dot.Start)
	} else {
		cmdText = cw.currentLine()
	}
	cmdText = strings.TrimSpace(cmdText)
	if cmdText == "" {
		return nil
	}

	var sb strings.Builder
	origOut := sess.Output
	sess.Output = &sb
	err := sess.ExecString(cmdText)
	sess.Output = origOut

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
	textY := rect.Y + render.HeaderPad - cw.scrollY

	// Render the buffer line by line.
	if cw.buf.Len() == 0 {
		render.DrawCursor(ren, textX, textY, lineH, pal.CmdwinFG)
		return nil
	}

	lines := cw.getLines()
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

	dot := cw.dot
	lineStart := 0 // codepoint offset of the start of the current line
	for i, line := range lines {
		lineRunes := []rune(line)
		lineEnd := lineStart + len(lineRunes) // not including the \n

		y := textY + int32(i)*lineH
		// Clip to cmdwin rect.
		if y+lineH < rect.Y || y > rect.Y+rect.H {
			lineStart = lineEnd + 1
			continue
		}

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

		lineStart = lineEnd + 1 // +1 for the \n
	}

	return nil
}
