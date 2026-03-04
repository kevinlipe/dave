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
)

// Window wraps an SDL window and renderer pair.
type Window struct {
	Win     *sdl.Window
	Ren     *sdl.Renderer
	Face    Face
	Palette Palette
}

// Open creates the SDL window and renderer, loads the font, and builds the
// Palette from cfg.
func Open(cfg *config.Config) (*Window, error) {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return nil, fmt.Errorf("render: sdl init: %w", err)
	}

	win, err := sdl.CreateWindow(
		"Dave",
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		900, 650,
		sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE|sdl.WINDOW_ALLOW_HIGHDPI,
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

	face, err := loadFace(cfg, ren)
	if err != nil {
		ren.Destroy()
		win.Destroy()
		sdl.Quit()
		return nil, err
	}

	sdl.StartTextInput()

	return &Window{
		Win:     win,
		Ren:     ren,
		Face:    face,
		Palette: PaletteFromConfig(cfg),
	}, nil
}

// Close releases all window resources. Safe to call multiple times.
func (w *Window) Close() {
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

// Size returns the current logical window size in pixels.
func (w *Window) Size() (width, height int32) {
	pw, ph, err := w.Ren.GetOutputSize()
	if err != nil {
		return 900, 650
	}
	return pw, ph
}

// loadFace creates the text rendering face from cfg. Falls back to embedded
// Go Mono on any error.
func loadFace(cfg *config.Config, ren *sdl.Renderer) (Face, error) {
	data := GoMonoTTF
	if cfg.Font != "" {
		resolved, err := resolveFontPath(cfg.Font)
		if err == nil {
			// Use the system font data if we can load it.
			data = resolved
		}
		// On failure: fall through to embedded Go Mono silently.
	}
	face, err := NewTTFFace(data, cfg.FontSize, ren)
	if err != nil {
		return nil, fmt.Errorf("render: load font: %w", err)
	}
	return face, nil
}
