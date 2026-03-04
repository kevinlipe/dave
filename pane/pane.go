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
	layoutGen int   // buffer generation when layout was last computed
	scrollY   int32 // pixel scroll offset (0 = top)
	wordWrap  bool  // current word-wrap state (starts from cfg.WordWrap)
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

// ToggleWordWrap switches word-wrap on/off and invalidates the layout.
func (p *Pane) ToggleWordWrap() {
	p.wordWrap = !p.wordWrap
	p.paneW = 0 // force re-layout
}

// Relayout recomputes the visual-line layout for the given pane pixel width.
// contentW is the full text-area width (pane width minus scrollbar).
// Must be called after any text change or resize before drawing.
func (p *Pane) Relayout(contentW int32) {
	// Ensure the file is loaded before we access its buffer. This is the
	// earliest point in the render path where we touch the buffer, so it is
	// the right place to trigger lazy loading.
	if err := p.File.EnsureLoaded(); err != nil {
		// Can't render an unloadable file; leave layout as-is.
		return
	}
	bufGen := p.File.Buffer().Gen()
	if contentW == p.paneW && bufGen == p.layoutGen {
		return
	}
	p.paneW = contentW
	p.layoutGen = bufGen
	margin := p.cfg.Margin
	if !p.wordWrap {
		margin = 0 // margin ignored when word wrap is off
	}
	p.layout = Wrap(p.File.Buffer(), p.face, contentW, p.wordWrap, margin, p.cfg.TabWidth)
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
func (p *Pane) EnsureVisible(cp int, paneH int32) {
	lineIdx := p.layout.LineAt(cp)
	if lineIdx < 0 || lineIdx >= len(p.layout.Lines) {
		return
	}
	vl := p.layout.Lines[lineIdx]
	lineH := int32(p.face.Height())
	// Scroll up if line is above the viewport.
	if vl.Y < p.scrollY {
		p.ScrollTo(vl.Y)
	}
	// Scroll down if line bottom is below the viewport.
	bodyH := paneH - HeaderH(p.face)
	if vl.Y+lineH > p.scrollY+bodyH {
		p.ScrollTo(vl.Y + lineH - bodyH)
	}
}

// CPAtXY returns the codepoint offset closest to pixel position (x, y)
// within the pane body (y is relative to the pane's top edge, not the body).
func (p *Pane) CPAtXY(x, y int32) int {
	hdrH := HeaderH(p.face)
	bodyY := y - hdrH + p.scrollY

	// Find the visual line at this y.
	for i := len(p.layout.Lines) - 1; i >= 0; i-- {
		vl := p.layout.Lines[i]
		if vl.Y <= bodyY {
			// Subtract scrollbar width, inner pad, and olivetti margin offset.
			relX := x - render.ScrollBarW - render.PanePad - p.layout.MarginLeft
			return cpAtX(p.File.Buffer(), vl, relX, p.face, p.cfg.TabWidth)
		}
	}
	if len(p.layout.Lines) > 0 {
		return 0
	}
	return 0
}

// cpAtX finds the codepoint offset within vl closest to pixel x.
func cpAtX(buf *buffer.Buffer, vl VisualLine, x int32, face render.Face, tabWidth int) int {
	if x <= 0 {
		return vl.Lo
	}
	cur := int32(0)
	for p := vl.Lo; p < vl.Hi; p++ {
		s := buf.ReadAt(p, 1)
		if len(s) == 0 {
			break
		}
		r := []rune(s)[0]
		if r == '\n' {
			return p
		}
		rw := runeWidth(r, cur, face, tabWidth)
		if x < cur+rw/2 {
			return p
		}
		cur += rw
	}
	return vl.Hi
}

// Draw renders the pane into the current SDL viewport.
// rect is the pane's bounding box in window coordinates.
func (p *Pane) Draw(ren *sdl.Renderer, rect sdl.Rect, active bool) error {
	p.Relayout(rect.W - render.ScrollBarW)

	// --- Background ---
	ren.SetDrawColor(p.pal.Background.R, p.pal.Background.G, p.pal.Background.B, 255)
	ren.FillRect(&sdl.Rect{X: rect.X + render.ScrollBarW, Y: rect.Y + HeaderH(p.face), W: rect.W - render.ScrollBarW, H: rect.H - HeaderH(p.face)})

	// --- Pane header ---
	p.drawHeader(ren, rect, active)

	// --- Scrollbar ---
	p.drawScrollbar(ren, rect, false)

	// --- Text body ---
	return p.drawBody(ren, rect)
}

// drawHeader renders the single-line pane header.
func (p *Pane) drawHeader(ren *sdl.Renderer, rect sdl.Rect, active bool) {
	hdrH := HeaderH(p.face)
	hdrRect := sdl.Rect{X: rect.X, Y: rect.Y, W: rect.W, H: hdrH}

	// Header background.
	bg := p.pal.HeaderBG
	if active {
		bg = p.pal.Background
	}
	ren.SetDrawColor(bg.R, bg.G, bg.B, 255)
	ren.FillRect(&hdrRect)

	// Header bottom border.
	ren.SetDrawColor(p.pal.Border.R, p.pal.Border.G, p.pal.Border.B, 255)
	ren.DrawLine(rect.X, rect.Y+hdrH-1, rect.X+rect.W, rect.Y+hdrH-1)

	// Header text: menu line.
	isCurrent := active
	_ = p.face.DrawText(p.File.MenuLine(isCurrent),
		rect.X+render.ScrollBarW+render.PanePad, rect.Y+render.HeaderPad, p.pal.HeaderFG)
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
	hdrH := HeaderH(p.face)
	bodyH := rect.H - hdrH
	sbRect := sdl.Rect{X: rect.X, Y: rect.Y + hdrH, W: render.ScrollBarW, H: bodyH}

	// Track background.
	ren.SetDrawColor(trackCol.R, trackCol.G, trackCol.B, 255)
	ren.FillRect(&sbRect)

	// Right-edge border in thumb color.
	ren.SetDrawColor(thumbCol.R, thumbCol.G, thumbCol.B, 255)
	ren.DrawLine(rect.X+render.ScrollBarW-1, rect.Y+hdrH, rect.X+render.ScrollBarW-1, rect.Y+rect.H-1)

	// Thumb: proportional position bubble.
	total := p.layout.TotalHeight
	if total > 0 && bodyH > 0 {
		thumbY := int32(float64(p.scrollY) / float64(total) * float64(bodyH))
		thumbH := int32(float64(bodyH) / float64(total) * float64(bodyH))
		if thumbH < 4 {
			thumbH = 4
		}
		if thumbY+thumbH > bodyH {
			thumbY = bodyH - thumbH
		}
		ren.SetDrawColor(thumbCol.R, thumbCol.G, thumbCol.B, 255)
		ren.FillRect(&sdl.Rect{X: rect.X, Y: rect.Y + hdrH + thumbY, W: render.ScrollBarW, H: thumbH})
	}
}

// drawBody renders the visible text lines into the pane body area.
func (p *Pane) drawBody(ren *sdl.Renderer, rect sdl.Rect) error {
	hdrH := HeaderH(p.face)
	bodyY := rect.Y + hdrH
	bodyH := rect.H - hdrH
	// textX includes the scrollbar, the inner pad, and the olivetti margin offset.
	textX := rect.X + render.ScrollBarW + render.PanePad + p.layout.MarginLeft
	lineH := int32(p.face.Height())

	dot := p.File.Dot()
	buf := p.File.Buffer()

	for _, vl := range p.layout.Lines {
		// Skip lines above the scroll viewport.
		lineBottom := vl.Y + lineH
		if lineBottom <= p.scrollY {
			continue
		}
		// Stop at lines below the viewport.
		lineScreenY := bodyY + vl.Y - p.scrollY
		if lineScreenY > rect.Y+rect.H {
			break
		}

		// Draw selection highlight for any overlap with dot.
		if dot.Start != dot.End {
			p.drawLineSelection(ren, vl, dot.Start, dot.End, lineScreenY, lineH, textX, rect)
		}

		// Draw cursor (caret) if dot is a null selection within this line.
		if dot.Start == dot.End && dot.Start >= vl.Lo && dot.Start <= vl.Hi {
			cx := textX + XForCP(buf, vl, dot.Start, p.face, p.cfg.TabWidth)
			render.DrawCursor(ren, cx, lineScreenY, lineH, p.pal.Text)
		}

		// Draw text.
		if err := p.drawLineText(ren, vl, dot.Start, dot.End, lineScreenY, textX, bodyY, bodyH); err != nil {
			return err
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
		sel := p.pal.SelectionBG
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

	text := buf.ReadAt(vl.Lo, drawHi-vl.Lo)
	// Replace tab characters with spaces for display.
	text = expandTabs(buf, vl, drawHi, p.face, p.cfg.TabWidth)

	// Determine if entire line is unselected.
	if selLo >= selHi || selHi <= vl.Lo || selLo >= drawHi {
		return p.face.DrawText(text, textX, lineY, p.pal.Text)
	}

	// Split into up-to three segments: before, during, after selection.
	pre := max(selLo-vl.Lo, 0)
	post := max(drawHi-selHi, 0)
	_ = pre
	_ = post

	// Simpler: render the whole line, then overlay the selected segment
	// using selection foreground colour at the correct x position.
	// First render with normal colour.
	if err := p.face.DrawText(text, textX, lineY, p.pal.Text); err != nil {
		return err
	}
	// Overlay selected portion with selection text colour.
	clipLo := max(selLo, vl.Lo)
	clipHi := min(selHi, drawHi)
	if clipLo < clipHi {
		selText := expandTabs(buf, VisualLine{Lo: clipLo, Hi: clipHi}, clipHi, p.face, p.cfg.TabWidth)
		sx := textX + XForCP(buf, vl, clipLo, p.face, p.cfg.TabWidth)
		if err := p.face.DrawText(selText, sx, lineY, p.pal.SelectionFG); err != nil {
			return err
		}
	}
	return nil
}

// expandTabs returns the display string for [vl.Lo, drawHi) with tabs
// replaced by appropriate spaces.
func expandTabs(buf *buffer.Buffer, vl VisualLine, drawHi int, face render.Face, tabWidth int) string {
	spaceW := face.GlyphAdvance(' ')
	tabW := int32(spaceW * tabWidth)
	if tabW <= 0 {
		tabW = int32(spaceW)
	}
	x := int32(0)
	out := make([]rune, 0, drawHi-vl.Lo)
	for p := vl.Lo; p < drawHi; p++ {
		s := buf.ReadAt(p, 1)
		if len(s) == 0 {
			break
		}
		r := []rune(s)[0]
		if r == '\t' {
			col := x / tabW
			stops := int(tabW*(col+1)-x) / int(spaceW)
			if stops < 1 {
				stops = 1
			}
			for i := 0; i < stops; i++ {
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

// HeaderH returns the pixel height of a pane header.
func HeaderH(face render.Face) int32 {
	return int32(face.Height()) + render.HeaderPad*2
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
