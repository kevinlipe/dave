// Package render implements SDL2 window creation, font rendering, and color
// management for Dave. It depends on config/ for the color scheme and on
// x/image/font/opentype for TTF rendering (not SDL_ttf).
package render

import (
	"dave/config"

	"github.com/veandco/go-sdl2/sdl"
)

// Default color scheme (Sam/Plan9 classic appearance). All colors match the
// values in dave-spec.md. These are the fallbacks; config overrides them.
var (
	ColBackground   = sdl.Color{R: 0xFF, G: 0xFF, B: 0xEA, A: 0xFF} // #FFFFEA
	ColText         = sdl.Color{R: 0x00, G: 0x00, B: 0x00, A: 0xFF} // #000000
	ColSelectionBG  = sdl.Color{R: 0x9F, G: 0xEE, B: 0xEE, A: 0xFF} // #9FEEEE
	ColSelectionFG  = sdl.Color{R: 0x00, G: 0x00, B: 0x00, A: 0xFF} // #000000
	ColBorder       = sdl.Color{R: 0x88, G: 0x88, B: 0x88, A: 0xFF} // #888888
	ColHeaderBG     = sdl.Color{R: 0xEE, G: 0xEE, B: 0xDD, A: 0xFF} // #EEEEDD
	ColHeaderFG     = sdl.Color{R: 0x00, G: 0x00, B: 0x00, A: 0xFF} // #000000
	ColScrollbar    = sdl.Color{R: 0x99, G: 0x99, B: 0x4C, A: 0xFF} // #99994C
	ColCmdwinBG     = sdl.Color{R: 0xEA, G: 0xFF, B: 0xFF, A: 0xFF} // #EAFFFF
	ColCmdwinFG     = sdl.Color{R: 0x00, G: 0x00, B: 0x00, A: 0xFF} // #000000
	ColCmdwinBorder = sdl.Color{R: 0x88, G: 0x88, B: 0xCC, A: 0xFF} // #8888CC
	ColCmdwinScroll = sdl.Color{R: 0x88, G: 0x88, B: 0xCC, A: 0xFF} // #8888CC

	// Menu colors (not overridable via config).
	ColMenuBG      = sdl.Color{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF} // #FFFFFF
	ColMenuFG      = sdl.Color{R: 0x00, G: 0x00, B: 0x00, A: 0xFF} // #000000
	ColMenuHilight = sdl.Color{R: 0x9F, G: 0xEE, B: 0xEE, A: 0xFF} // #9FEEEE (same as selection)
)

// Palette holds the active colors for a Dave session, derived from config.
type Palette struct {
	Background      sdl.Color
	Text            sdl.Color
	SelectionBG     sdl.Color // cmdwin selection
	SelectionFG     sdl.Color
	BodySelectionBG sdl.Color // body/pane selection
	BodySelectionFG sdl.Color
	Border          sdl.Color
	HeaderBG     sdl.Color
	HeaderFG     sdl.Color
	Scrollbar    sdl.Color
	CmdwinBG     sdl.Color
	CmdwinFG     sdl.Color
	CmdwinBorder sdl.Color
	CmdwinScroll sdl.Color
	MenuBG       sdl.Color
	MenuFG       sdl.Color
	MenuHilight  sdl.Color
}

// PaletteFromConfig builds a Palette from config, using spec defaults for
// any unset fields.
func PaletteFromConfig(cfg *config.Config) Palette {
	return Palette{
		Background:   toSDL(cfg.Colors.Background),
		Text:         toSDL(cfg.Colors.Text),
		SelectionBG:     toSDL(cfg.Colors.SelectionBG),
		SelectionFG:     toSDL(cfg.Colors.SelectionFG),
		BodySelectionBG: toSDL(cfg.Colors.BodySelectionBG),
		BodySelectionFG: toSDL(cfg.Colors.BodySelectionFG),
		Border:       toSDL(cfg.Colors.Border),
		HeaderBG:     toSDL(cfg.Colors.HeaderBG),
		HeaderFG:     toSDL(cfg.Colors.HeaderFG),
		Scrollbar:    toSDL(cfg.Colors.Scrollbar),
		CmdwinBG:     toSDL(cfg.Colors.CmdwinBG),
		CmdwinFG:     toSDL(cfg.Colors.CmdwinFG),
		CmdwinBorder: toSDL(cfg.Colors.CmdwinBorder),
		CmdwinScroll: toSDL(cfg.Colors.CmdwinScroll),
		MenuBG:       ColMenuBG,
		MenuFG:       ColMenuFG,
		MenuHilight:  ColMenuHilight,
	}
}

// toSDL converts a config.Color to an sdl.Color.
func toSDL(c config.Color) sdl.Color {
	return sdl.Color{R: c.R, G: c.G, B: c.B, A: 0xFF}
}
