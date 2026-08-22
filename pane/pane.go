package pane

import (
	"dave/buffer"
	"dave/config"
	"dave/file"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// Pane is a scrollable, word-wrapped view of a *file.File.
type Pane struct {
	File      *file.File
	cfg       *config.Config
	pal       render.Palette
	face      render.Face
	layout    Layout
	paneW     int32 // last computed content width (widthPx passed to Wrap)
	paneH     int32 // last known pane height in pixels (set during Draw)
	layoutGen int   // buffer generation when layout was last computed
	scrollY   int32 // pixel scroll offset (0 = top)
	wordWrap  bool  // current word-wrap state (starts from cfg.WordWrap)

	// Counters for tests, so they can tell an incremental relayout from a
	// full one rather than passing either way.
	fullWraps            int
	incrementalRelayouts int
}

// New creates a Pane displaying f.
func New(f *file.File, cfg *config.Config, pal render.Palette, face render.Face) *Pane {
	return &Pane{
		File:     f,
		cfg:      cfg,
		pal:      pal,
		face:     face,
		wordWrap: cfg.WordWrap,
	}
}

// Layout returns the current visual-line layout (may be stale until Relayout is called).
func (p *Pane) Layout() *Layout { return &p.layout }

// Face returns the font face used by this pane.
func (p *Pane) Face() render.Face { return p.face }

// TabWidth returns the configured tab stop width.
func (p *Pane) TabWidth() int { return p.cfg.TabWidth }

// ScrollY returns the current vertical scroll offset in pixels.
func (p *Pane) ScrollY() int32 { return p.scrollY }

// PaneH returns the last known pane height in pixels (set during Draw).
func (p *Pane) PaneH() int32 { return p.paneH }

// InvalidateLayout forces a full re-layout on the next draw without resetting
// the scroll position. Use after config changes that affect text metrics.
func (p *Pane) InvalidateLayout() { p.paneW = 0 }

// SetFace replaces the font face and invalidates the layout.
func (p *Pane) SetFace(face render.Face) {
	p.face = face
	p.paneW = 0
}

// SetPalette replaces the color palette used for rendering.
func (p *Pane) SetPalette(pal render.Palette) { p.pal = pal }

// SetFile replaces the file displayed by this pane and invalidates the layout.
// The scroll position is reset to the top so the new file is visible from the start.
func (p *Pane) SetFile(f *file.File) {
	p.File = f
	p.paneW = 0  // force re-layout
	p.scrollY = 0 // reset scroll to top of new file
}

// ToggleWordWrap switches word-wrap on/off and invalidates the layout.
func (p *Pane) ToggleWordWrap() {
	p.wordWrap = !p.wordWrap
	p.paneW = 0 // force re-layout
}

// Relayout recomputes the visual-line layout for the given pane pixel width.
// contentW is the full text-area width (pane width minus scrollbar).
// Must be called after any text change or resize before drawing.
func (p *Pane) Relayout(contentW int32) {
	if p.File == nil {
		return
	}
	// Ensure the file is loaded before we access its buffer. This is the
	// earliest point in the render path where we touch the buffer, so it is
	// the right place to trigger lazy loading.
	if err := p.File.EnsureLoaded(); err != nil {
		// Can't render an unloadable file; leave layout as-is.
		return
	}
	buf := p.File.Buffer()
	bufGen := buf.Gen()
	if contentW == p.paneW && bufGen == p.layoutGen {
		return
	}
	margin := p.cfg.Margin
	if !p.wordWrap {
		margin = 0 // margin ignored when word wrap is off
	}

	// If only the text changed, and the buffer can still say where, re-wrap
	// just the logical lines the edit touched. Anything else — a resize, a
	// new face, word wrap toggled, or an edit too old for the buffer's log —
	// falls through to a full wrap.
	if contentW == p.paneW && len(p.layout.Lines) > 0 {
		if lo, hi, ok := buf.EditsSince(p.layoutGen); ok {
			if p.relayoutRange(buf, lo, hi) {
				p.layoutGen = bufGen
				return
			}
		}
	}

	p.paneW = contentW
	p.layoutGen = bufGen
	p.layout = Wrap(buf, p.face, contentW, p.wordWrap, margin, p.cfg.TabWidth)
	p.fullWraps++
}

// relayoutRange re-wraps the logical lines spanning codepoints [lo, hi) and
// splices the result into the existing layout, shifting the lines below by
// the change in length. It reports whether it succeeded; on false the caller
// must do a full wrap.
//
// Every step that assumes the old layout still lines up with the buffer is
// checked, and any mismatch bails out rather than producing a layout that is
// subtly wrong.
func (p *Pane) relayoutRange(buf *buffer.Buffer, lo, hi int) bool {
	old := p.layout.Lines
	// Wrap's last visual line always ends at the end of the buffer, which is
	// how the old length is recovered without storing it.
	oldLen := old[len(old)-1].Hi
	delta := buf.Len() - oldLen
	if lo == hi && delta == 0 {
		return true // a generation moved but nothing actually changed
	}

	firstLine := buf.LineNumber(lo)
	lastLine := buf.LineNumber(hi)
	first, ok1 := buf.LineRange(firstLine)
	last, ok2 := buf.LineRange(lastLine)
	if !ok1 || !ok2 {
		return false
	}

	// The region starts at a logical line start, which is at or before the
	// first changed codepoint and so has the same offset in both the old and
	// the new buffer.
	startCP := first.Start
	oldFirst := p.layout.LineAt(startCP)
	if oldFirst >= len(old) || old[oldFirst].Lo != startCP {
		return false
	}

	// It ends where the next logical line begins, which does move.
	oldLast := len(old) - 1
	if lastLine < buf.LineCount() {
		nextStart := last.End - delta
		idx := p.layout.LineAt(nextStart)
		if idx <= oldFirst || idx >= len(old) || old[idx].Lo != nextStart {
			return false
		}
		oldLast = idx - 1
	}
	if oldLast < oldFirst {
		return false
	}

	// Re-wrap the affected logical lines, and only those.
	var fresh []VisualLine
	for ln := firstLine; ln <= lastLine; ln++ {
		r, ok := buf.LineRange(ln)
		if !ok {
			return false
		}
		end := r.End
		if end > r.Start && buf.RuneAt(end-1) == '\n' {
			end-- // the newline belongs to no visual line
		}
		fresh = wrapLine(fresh, []rune(buf.ReadAt(r.Start, end-r.Start)), r.Start,
			p.face, p.wordWrap, p.layout.WrapWidth, p.cfg.TabWidth)
	}

	// Typing inside a line usually produces the same number of visual lines,
	// in which case the replacement is written over the old lines in place
	// and nothing is allocated at all.
	replaced := oldLast + 1 - oldFirst
	lines := old
	if len(fresh) == replaced {
		copy(old[oldFirst:], fresh)
	} else {
		tail := make([]VisualLine, len(old)-oldLast-1)
		copy(tail, old[oldLast+1:])
		lines = append(append(old[:oldFirst], fresh...), tail...)
	}
	// Everything below the splice keeps its wrapping and just moves.
	for i := oldFirst + len(fresh); i < len(lines); i++ {
		lines[i].Lo += delta
		lines[i].Hi += delta
	}

	p.layout.Lines = lines
	p.layout.TotalHeight = int32(len(lines)) * p.layout.LineH
	p.incrementalRelayouts++
	return true
}

// ScrollTo sets the scroll offset (clamped to valid range).
func (p *Pane) ScrollTo(y int32) {
	if y < 0 {
		y = 0
	}
	max := p.layout.TotalHeight - int32(p.face.Height())
	if max < 0 {
		max = 0
	}
	if y > max {
		y = max
	}
	p.scrollY = y
}

// ScrollBy scrolls by delta lines (positive = down, negative = up).
func (p *Pane) ScrollBy(deltaLines int32) {
	p.ScrollTo(p.scrollY + deltaLines*int32(p.face.Height()))
}

// EnsureVisible scrolls so that codepoint cp is visible.
func (p *Pane) EnsureVisible(cp int) {
	p.Relayout(p.paneW) // refresh layout if buffer changed since last Draw
	lineIdx := p.layout.LineAt(cp)
	if lineIdx < 0 || lineIdx >= len(p.layout.Lines) {
		return
	}
	lineY := p.layout.Y(lineIdx)
	lineH := int32(p.face.Height())
	// Scroll up if line is above the viewport.
	if lineY < p.scrollY {
		p.ScrollTo(lineY)
	}
	// Scroll down if line bottom is below the viewport.
	viewH := p.paneH - render.BodyTopPad
	if lineY+lineH > p.scrollY+viewH {
		p.ScrollTo(lineY + lineH - viewH)
	}
}

// CPAtXY returns the codepoint offset closest to pixel position (x, y)
// within the pane body (y is relative to the pane's top edge).
func (p *Pane) CPAtXY(x, y int32) int {
	if p.File == nil {
		return 0
	}
	bodyY := y - render.BodyTopPad + p.scrollY
	// Above the first line (a click in the top pad while scrolled to the
	// top) means the start of the buffer.
	if bodyY < 0 || len(p.layout.Lines) == 0 {
		return 0
	}
	vl := p.layout.Lines[p.layout.LineAtY(bodyY)]
	// Subtract scrollbar width, inner pad, and olivetti margin offset.
	relX := x - render.ScrollBarW - render.PanePad - p.layout.MarginLeft
	return cpAtX(p.File.Buffer(), vl, relX, p.face, p.cfg.TabWidth)
}

// cpAtX finds the codepoint offset within vl closest to pixel x.
func cpAtX(buf *buffer.Buffer, vl VisualLine, x int32, face render.Face, tabWidth int) int {
	if x <= 0 {
		return vl.Lo
	}
	runes := []rune(buf.ReadAt(vl.Lo, vl.Hi-vl.Lo))
	cur := int32(0)
	for i, r := range runes {
		if r == '\n' {
			return vl.Lo + i
		}
		rw := runeWidth(r, cur, face, tabWidth)
		if x < cur+rw/2 {
			return vl.Lo + i
		}
		cur += rw
	}
	return vl.Hi
}

// Draw renders the pane into the current SDL viewport.
// rect is the pane's bounding box in window coordinates.
// focused is true when this pane has keyboard focus (drives border thickness).
func (p *Pane) Draw(ren *sdl.Renderer, rect sdl.Rect, focused bool) error {
	p.paneH = rect.H
	if p.File == nil {
		// Empty pane: plain white background.
		ren.SetDrawColor(255, 255, 255, 255)
		ren.FillRect(&rect)
		return nil
	}
	p.Relayout(rect.W - render.ScrollBarW)

	// --- Background ---
	ren.SetDrawColor(p.pal.Background.R, p.pal.Background.G, p.pal.Background.B, 255)
	ren.FillRect(&sdl.Rect{X: rect.X + render.ScrollBarW, Y: rect.Y, W: rect.W - render.ScrollBarW, H: rect.H})

	// --- Scrollbar ---
	p.drawScrollbar(ren, rect, false)

	// --- Text body ---
	// Clip to the pane bounds so partially-visible lines at the top or bottom
	// of the viewport don't bleed glyph pixels into adjacent panes.
	ren.SetClipRect(&rect)
	err := p.drawBody(ren, rect)
	ren.SetClipRect(nil)
	if err != nil {
		return err
	}

	// --- Border (drawn last so it sits on top) ---
	// Keyboard-focused pane: thick border (BorderW). All other panes: thin border (ThinBorderW).
	if focused {
		ren.SetDrawColor(p.pal.Scrollbar.R, p.pal.Scrollbar.G, p.pal.Scrollbar.B, 255)
		render.DrawBorder(ren, rect, render.BorderW)
	} else {
		ren.SetDrawColor(p.pal.Border.R, p.pal.Border.G, p.pal.Border.B, 255)
		render.DrawBorder(ren, rect, render.ThinBorderW)
	}
	return nil
}

// drawScrollbar renders the scrollbar strip on the left edge.
// The track uses the body background color; the thumb uses the scrollbar accent color.
func (p *Pane) drawScrollbar(ren *sdl.Renderer, rect sdl.Rect, isCmd bool) {
	trackCol := p.pal.Background
	thumbCol := p.pal.Scrollbar
	if isCmd {
		trackCol = p.pal.CmdwinBG
		thumbCol = p.pal.CmdwinScroll
	}
	bodyH := rect.H
	sbRect := sdl.Rect{X: rect.X, Y: rect.Y, W: render.ScrollBarW, H: bodyH}

	// Track background.
	ren.SetDrawColor(trackCol.R, trackCol.G, trackCol.B, 255)
	ren.FillRect(&sbRect)

	// Right-edge border in thumb color.
	ren.SetDrawColor(thumbCol.R, thumbCol.G, thumbCol.B, 255)
	ren.DrawLine(rect.X+render.ScrollBarW-1, rect.Y, rect.X+render.ScrollBarW-1, rect.Y+rect.H-1)

	// Thumb: only drawn when content exceeds the viewport; avoids overflow when
	// thumbH = bodyH²/total would be larger than bodyH (total < bodyH case).
	total := p.layout.TotalHeight
	if total > bodyH {
		thumbY := int32(float64(p.scrollY) / float64(total) * float64(bodyH))
		thumbH := int32(float64(bodyH) / float64(total) * float64(bodyH))
		if thumbH < 4 {
			thumbH = 4
		}
		if thumbY+thumbH > bodyH {
			thumbY = bodyH - thumbH
		}
		ren.SetDrawColor(thumbCol.R, thumbCol.G, thumbCol.B, 255)
		ren.FillRect(&sdl.Rect{X: rect.X, Y: rect.Y + thumbY, W: render.ScrollBarW, H: thumbH})
	}
}

// drawBody renders the visible text lines into the pane body area.
func (p *Pane) drawBody(ren *sdl.Renderer, rect sdl.Rect) error {
	bodyY := rect.Y
	bodyH := rect.H
	// textX includes the scrollbar, the inner pad, and the olivetti margin offset.
	textX := rect.X + render.ScrollBarW + render.PanePad + p.layout.MarginLeft
	lineH := int32(p.face.Height())

	dot := p.File.Dot()
	buf := p.File.Buffer()

	// Start at the line containing the top of the viewport rather than
	// scanning every line in the file. The skip below still applies: on the
	// last line, scrollY can sit past the line box entirely.
	first := p.layout.LineAtY(p.scrollY)
	for i, vl := range p.layout.Lines[first:] {
		lineTop := p.layout.Y(first + i)
		// Skip lines above the scroll viewport.
		lineBottom := lineTop + lineH
		if lineBottom <= p.scrollY {
			continue
		}
		// Stop at lines below the viewport.
		lineScreenY := bodyY + render.BodyTopPad + lineTop - p.scrollY
		if lineScreenY > rect.Y+rect.H {
			break
		}

		// Draw selection highlight for any overlap with dot.
		if dot.Start != dot.End {
			p.drawLineSelection(ren, vl, dot.Start, dot.End, lineScreenY, lineH, textX, rect)
		}

		// Draw text.
		if err := p.drawLineText(ren, vl, dot.Start, dot.End, lineScreenY, textX, bodyY, bodyH); err != nil {
			return err
		}

		// Draw cursor (caret) on top of text if dot is a null selection within this line.
		if dot.Start == dot.End && dot.Start >= vl.Lo && dot.Start <= vl.Hi {
			cx := textX + XForCP(buf, vl, dot.Start, p.face, p.cfg.TabWidth)
			render.DrawCursor(ren, cx, lineScreenY, lineH, p.pal.Text)
		}
	}
	return nil
}

// drawLineSelection renders the selection highlight for the portion of vl
// that overlaps with [selLo, selHi).
func (p *Pane) drawLineSelection(ren *sdl.Renderer, vl VisualLine, selLo, selHi int, lineY, lineH, textX int32, rect sdl.Rect) {
	if selHi <= vl.Lo || selLo > vl.Hi {
		return
	}
	buf := p.File.Buffer()
	clipLo := max(selLo, vl.Lo)
	clipHi := min(selHi, vl.Hi)
	x0 := textX + XForCP(buf, vl, clipLo, p.face, p.cfg.TabWidth)
	x1 := textX + XForCP(buf, vl, clipHi, p.face, p.cfg.TabWidth)
	if selHi >= vl.Hi && vl.Hi < p.File.Len() {
		// Selection extends past this line: fill to edge.
		x1 = rect.X + rect.W
	}
	if x1 > x0 {
		sel := p.pal.BodySelectionBG
		ren.SetDrawColor(sel.R, sel.G, sel.B, 255)
		ren.FillRect(&sdl.Rect{X: x0, Y: lineY, W: x1 - x0, H: lineH})
	}
}

// drawLineText renders the text of vl, using selection foreground for the
// selected portion and normal foreground for the rest.
func (p *Pane) drawLineText(ren *sdl.Renderer, vl VisualLine, selLo, selHi int, lineY, textX, bodyY, bodyH int32) error {
	if vl.Lo >= vl.Hi {
		return nil
	}
	buf := p.File.Buffer()
	// Only draw up to (not including) a trailing newline.
	drawHi := vl.Hi
	if drawHi > vl.Lo {
		s := buf.ReadAt(drawHi-1, 1)
		if len(s) > 0 && []rune(s)[0] == '\n' {
			drawHi--
		}
	}
	if drawHi <= vl.Lo {
		return nil
	}

	// Read the visible portion of the line once, then expand tabs.
	lineRunes := []rune(buf.ReadAt(vl.Lo, drawHi-vl.Lo))
	text := expandTabsRunes(lineRunes, 0, len(lineRunes), 0, p.face, p.cfg.TabWidth)

	// Determine if entire line is unselected.
	if selLo >= selHi || selHi <= vl.Lo || selLo >= drawHi {
		return p.face.DrawText(text, textX, lineY, p.pal.Text)
	}

	// Render whole line in normal colour, then overlay the selected portion.
	if err := p.face.DrawText(text, textX, lineY, p.pal.Text); err != nil {
		return err
	}
	clipLo := max(selLo, vl.Lo)
	clipHi := min(selHi, drawHi)
	if clipLo < clipHi {
		// Expand just the selected runes, starting at their pixel x offset
		// within the line so tab stops are computed correctly.
		startX := measureRunes(lineRunes, 0, clipLo-vl.Lo, p.face, p.cfg.TabWidth)
		selText := expandTabsRunes(lineRunes, clipLo-vl.Lo, clipHi-vl.Lo, startX, p.face, p.cfg.TabWidth)
		if err := p.face.DrawText(selText, textX+startX, lineY, p.pal.BodySelectionFG); err != nil {
			return err
		}
	}
	return nil
}

// expandTabsRunes returns the display string for runes[lo:hi] with tabs
// replaced by appropriate spaces. startX is the pixel x-offset at which
// runes[lo] begins (needed for correct tab-stop alignment mid-line).
func expandTabsRunes(runes []rune, lo, hi int, startX int32, face render.Face, tabWidth int) string {
	spaceW := face.GlyphAdvance(' ')
	tabW := int32(spaceW * tabWidth)
	if tabW <= 0 {
		tabW = int32(spaceW)
	}
	x := startX
	out := make([]rune, 0, hi-lo)
	for i := lo; i < hi; i++ {
		r := runes[i]
		if r == '\t' {
			col := x / tabW
			stops := int(tabW*(col+1)-x) / int(spaceW)
			if stops < 1 {
				stops = 1
			}
			for j := 0; j < stops; j++ {
				out = append(out, ' ')
			}
			x += tabW*(col+1) - x
		} else {
			out = append(out, r)
			x += int32(face.GlyphAdvance(r))
		}
	}
	return string(out)
}


// ScrollbarJump returns the codepoint offset corresponding to a click at
// pixel y within the scrollbar (y relative to the body top).
func (p *Pane) ScrollbarJump(y, bodyH int32) {
	if p.layout.TotalHeight <= 0 {
		return
	}
	newScrollY := int32(float64(y) / float64(bodyH) * float64(p.layout.TotalHeight))
	p.ScrollTo(newScrollY)
}

// --- helpers -----------------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
