package render

import (
	"fmt"

	"dave/config"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// ScrollBarW is the pixel width of the scrollbar strip on the left edge
	// of each pane and the command window.
	ScrollBarW int32 = 12

	// CmdWinLines is the fixed height of the command window in lines of text.
	CmdWinLines = 4

	// HeaderPad is vertical padding above and below text in a pane header.
	HeaderPad int32 = 2

	// PanePad is the horizontal inner padding of a pane's text area.
	PanePad int32 = 4

	// BorderW is the pixel width of the current-frame highlight border.
	BorderW int32 = 3

	// ThinBorderW is the pixel width of the inactive-frame border.
	ThinBorderW int32 = 1
)

// Window wraps an SDL window and renderer pair.
type Window struct {
	Win     *sdl.Window
	Ren     *sdl.Renderer
	Face    Face
	Palette Palette
	Scale   float64 // physical pixels per logical pixel (1.0 on most Linux, >1 on HiDPI)
	W, H    int32   // cached output size; call UpdateSize on window resize events
	dpi     float64 // display DPI used when loading fonts; set once in Open
}

// Open creates the SDL window and renderer, loads the font, and builds the
// Palette from cfg.
func Open(cfg *config.Config) (*Window, error) {
	// Prevent Windows DPI virtualization from blurring the framebuffer.
	// Must be set before SDL_Init. No effect on Linux/macOS.
	sdl.SetHint("SDL_WINDOWS_DPI_AWARENESS", "permonitorv2")

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return nil, fmt.Errorf("render: sdl init: %w", err)
	}

	win, err := sdl.CreateWindow(
		"dave",
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		900, 650,
		sdl.WINDOW_HIDDEN|sdl.WINDOW_RESIZABLE|sdl.WINDOW_ALLOW_HIGHDPI,
	)
	if err != nil {
		sdl.Quit()
		return nil, fmt.Errorf("render: create window: %w", err)
	}

	sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, "0")

	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_ACCELERATED|sdl.RENDERER_PRESENTVSYNC)
	if err != nil {
		win.Destroy()
		sdl.Quit()
		return nil, fmt.Errorf("render: create renderer: %w", err)
	}

	// Compute DPI scale: ratio of physical renderer pixels to logical window pixels.
	// On Windows with DPI awareness this will be e.g. 1.25 at 125% display scaling.
	scale := 1.0
	if lw, _ := win.GetSize(); lw > 0 {
		if pw, _, err := ren.GetOutputSize(); err == nil && pw > 0 {
			if s := float64(pw) / float64(lw); s > 1.0 {
				scale = s
			}
		}
	}

	// Determine the display DPI so that font point sizes match other apps on
	// this system. Linux desktops use 96 DPI; macOS uses 72. SDL's reported
	// value is trusted if it's in a plausible range [72, 300].
	displayDPI := 96.0
	if idx, err := win.GetDisplayIndex(); err == nil {
		if _, _, vdpi, err := sdl.GetDisplayDPI(idx); err == nil && vdpi >= 72 && vdpi <= 300 {
			displayDPI = float64(vdpi)
		}
	}

	face, err := loadFace(cfg, ren, scale, displayDPI)
	if err != nil {
		ren.Destroy()
		win.Destroy()
		sdl.Quit()
		return nil, err
	}

	sdl.StartTextInput()

	pw, ph, _ := ren.GetOutputSize()
	return &Window{
		Win:     win,
		Ren:     ren,
		Face:    face,
		Scale:   scale,
		dpi:     displayDPI,
		Palette: PaletteFromConfig(cfg),
		W:       pw,
		H:       ph,
	}, nil
}

// Close releases all window resources. Safe to call multiple times.
func (w *Window) Close() {
	// Hide first so the window disappears before any teardown work is visible.
	if w.Win != nil {
		w.Win.Hide()
	}
	sdl.StopTextInput()
	if w.Face != nil {
		w.Face.Close()
	}
	if w.Ren != nil {
		w.Ren.Destroy()
	}
	if w.Win != nil {
		w.Win.Destroy()
	}
	sdl.Quit()
}

// DrawBorder draws a w-pixel thick border inside rect using the current draw color.
func DrawBorder(ren *sdl.Renderer, rect sdl.Rect, w int32) {
	ren.FillRect(&sdl.Rect{X: rect.X, Y: rect.Y, W: rect.W, H: w})                          // top
	ren.FillRect(&sdl.Rect{X: rect.X, Y: rect.Y + rect.H - w, W: rect.W, H: w})             // bottom
	ren.FillRect(&sdl.Rect{X: rect.X, Y: rect.Y, W: w, H: rect.H})                          // left
	ren.FillRect(&sdl.Rect{X: rect.X + rect.W - w, Y: rect.Y, W: w, H: rect.H})             // right
}

// DrawCursor draws an I-beam cursor at pixel position (x, y) with the given
// line height and color. The cursor is a 2-px vertical bar with 2×3 filled
// square terminals centered at the top and bottom, matching Sam's cursor appearance.
func DrawCursor(ren *sdl.Renderer, x, y, lineH int32, col sdl.Color) {
	ren.SetDrawColor(col.R, col.G, col.B, 255)
	// Vertical bar (2px wide).
	ren.FillRect(&sdl.Rect{X: x, Y: y, W: 2, H: lineH})
	// Top square: 3px wide × 2px tall, centered on bar.
	ren.FillRect(&sdl.Rect{X: x - 1, Y: y, W: 4, H: 2})
	// Bottom square: 3px wide × 2px tall, centered on bar.
	ren.FillRect(&sdl.Rect{X: x - 1, Y: y + lineH - 2, W: 4, H: 2})
}

// Size returns the cached output size in pixels.
func (w *Window) Size() (width, height int32) {
	return w.W, w.H
}

// UpdateSize re-queries the renderer output size and updates the cache.
// Call this when a SDL_WINDOWEVENT_SIZE_CHANGED event is received.
func (w *Window) UpdateSize() {
	pw, ph, err := w.Ren.GetOutputSize()
	if err == nil {
		w.W, w.H = pw, ph
	}
}

// ReloadFace creates a new font face from cfg using the display scale and DPI
// that were determined when the window was opened. The caller is responsible
// for closing the old face after swapping it out.
func (w *Window) ReloadFace(cfg *config.Config) (Face, error) {
	return loadFace(cfg, w.Ren, w.Scale, w.dpi)
}

// loadFace creates the text rendering face from cfg. Falls back to embedded
// Go Mono on any error. scale is the physical-to-logical pixel ratio for HiDPI
// compensation; pass 1.0 on non-HiDPI displays. dpi is the display DPI used to
// convert point sizes to pixels (96 on Linux desktops, 72 on macOS, etc.).
func loadFace(cfg *config.Config, ren *sdl.Renderer, scale, dpi float64) (Face, error) {
	if cfg.FontMode == "plan9" && cfg.Plan9Font != "" {
		face, err := NewPlan9Face(cfg.Plan9Font, ren, cfg.Plan9Scale)
		if err != nil {
			return nil, fmt.Errorf("render: load plan9 font %q: %w", cfg.Plan9Font, err)
		}
		return face, nil
	}

	data := GoMonoTTF
	if cfg.Font != "" {
		resolved, err := resolveFontPath(cfg.Font)
		if err == nil {
			data = resolved
		}
		// On failure: fall through to embedded Go Mono silently.
	}
	face, err := NewTTFFace(data, cfg.FontSize*scale, dpi, ren)
	if err != nil {
		return nil, fmt.Errorf("render: load font: %w", err)
	}
	return face, nil
}
