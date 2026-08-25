package config

import (
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.FontSize != 14 {
		t.Errorf("FontSize: got %v, want 14", cfg.FontSize)
	}
	if cfg.FontMode != "ttf" {
		t.Errorf("FontMode: got %q, want ttf", cfg.FontMode)
	}
	if !cfg.WordWrap {
		t.Error("WordWrap: got false, want true")
	}
	if cfg.Margin != 80 {
		t.Errorf("Margin: got %d, want 80", cfg.Margin)
	}
	if cfg.TabWidth != 4 {
		t.Errorf("TabWidth: got %d, want 4", cfg.TabWidth)
	}
	// Spot-check default colors.
	want := Color{0xFF, 0xFF, 0xEA}
	if cfg.Colors.Background != want {
		t.Errorf("Background: got %v, want %v", cfg.Colors.Background, want)
	}
	want = Color{0x9F, 0xEE, 0xEE}
	if cfg.Colors.SelectionBG != want {
		t.Errorf("SelectionBG: got %v, want %v", cfg.Colors.SelectionBG, want)
	}
	want = Color{0x88, 0x88, 0xCC}
	if cfg.Colors.CmdwinBorder != want {
		t.Errorf("CmdwinBorder: got %v, want %v", cfg.Colors.CmdwinBorder, want)
	}
}

func TestParseEmpty(t *testing.T) {
	cfg, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	// Empty config = defaults.
	def := Default()
	if cfg.FontSize != def.FontSize {
		t.Errorf("FontSize: got %v, want %v", cfg.FontSize, def.FontSize)
	}
}

func TestParseAllKeys(t *testing.T) {
	input := `
# Dave config test
font = Go Mono
font_size = 16
font_mode = ttf
plan9_font = /usr/share/plan9port/font/lucsans/euro.8.font
word_wrap = false
margin = 100
split = horizontal
tab_width = 8
use_pwsh = false
color_background    = #AABBCC
color_text          = #112233
color_selection_bg  = #AAFFAA
color_selection_fg  = #001100
color_border        = #444444
color_header_bg     = #CCCCBB
color_header_fg     = #111111
color_scrollbar     = #777730
color_cmdwin_bg     = #CCFFFF
color_cmdwin_fg     = #001122
color_cmdwin_border = #6666AA
color_cmdwin_scroll = #6666AA
`
	cfg, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Font != "Go Mono" {
		t.Errorf("Font: got %q, want %q", cfg.Font, "Go Mono")
	}
	if cfg.FontSize != 16 {
		t.Errorf("FontSize: got %v, want 16", cfg.FontSize)
	}
	if cfg.WordWrap {
		t.Error("WordWrap: got true, want false")
	}
	if cfg.Margin != 100 {
		t.Errorf("Margin: got %d, want 100", cfg.Margin)
	}
	if cfg.Split != "horizontal" {
		t.Errorf("Split: got %q, want horizontal", cfg.Split)
	}
	if cfg.TabWidth != 8 {
		t.Errorf("TabWidth: got %d, want 8", cfg.TabWidth)
	}
	if cfg.UsePwsh {
		t.Error("UsePwsh: got true, want false")
	}
	want := Color{0xAA, 0xBB, 0xCC}
	if cfg.Colors.Background != want {
		t.Errorf("Background: got %v, want %v", cfg.Colors.Background, want)
	}
	want = Color{0x6666AA & 0xFF, (0x6666AA >> 8) & 0xFF, 0x6666AA >> 16}
	// Simpler: just check the hex parse directly.
	want2 := Color{0x66, 0x66, 0xAA}
	if cfg.Colors.CmdwinBorder != want2 {
		t.Errorf("CmdwinBorder: got %v, want %v", cfg.Colors.CmdwinBorder, want2)
	}
	_ = want
}

func TestParseBoolVariants(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true}, {"True", true}, {"TRUE", true},
		{"1", true}, {"yes", true}, {"on", true},
		{"false", false}, {"False", false}, {"FALSE", false},
		{"0", false}, {"no", false}, {"off", false},
	}
	for _, tc := range cases {
		got, err := parseBool(tc.in)
		if err != nil {
			t.Errorf("parseBool(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseBool(%q): got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in      string
		want    Color
		wantErr bool
	}{
		{"#FFFFEA", Color{0xFF, 0xFF, 0xEA}, false},
		{"#000000", Color{0, 0, 0}, false},
		{"#9FEEEE", Color{0x9F, 0xEE, 0xEE}, false},
		{"#8888CC", Color{0x88, 0x88, 0xCC}, false},
		{"FFFFEA", Color{0xFF, 0xFF, 0xEA}, false}, // # optional
		{"#FFFFF", Color{}, true},                  // too short
		{"#GGGGGG", Color{}, true},                 // invalid hex
	}
	for _, tc := range cases {
		got, err := parseHexColor(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHexColor(%q): expected error, got none", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHexColor(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseHexColor(%q): got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"no equals", "font_size 14"},
		{"unknown key", "unknown_key = value"},
		{"bad font_size", "font_size = abc"},
		{"bad font_size zero", "font_size = 0"},
		{"bad font_mode", "font_mode = acme"},
		{"bad word_wrap", "word_wrap = maybe"},
		{"bad margin", "margin = -1"},
		{"bad tab_width", "tab_width = 0"},
		{"bad split", "split = diagonal"},
		{"bad color", "color_background = #ZZZZZZ"},
		{"short color", "color_text = #FFFF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.input))
			if err == nil {
				t.Errorf("expected error for input %q, got none", tc.input)
			}
		})
	}
}

func TestParseComments(t *testing.T) {
	input := `
# This is a comment
  # Indented comment

font_size = 12
# Another comment
`
	cfg, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FontSize != 12 {
		t.Errorf("FontSize: got %v, want 12", cfg.FontSize)
	}
}
