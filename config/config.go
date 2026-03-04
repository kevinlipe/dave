// Package config parses Dave's configuration file and provides typed access
// to all settings. It has no SDL or rendering dependency.
//
// Config file location:
//
//	Linux/macOS:  ~/.config/dave/config
//	Windows:      %APPDATA%\dave\config
//
// Format: key = value, one per line. Lines beginning with # are comments.
// Unknown keys are an error. Missing file → all defaults.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Color is a 24-bit RGB color value.
type Color struct {
	R, G, B uint8
}

// Colors holds the full Dave color scheme.
type Colors struct {
	Background   Color // pane/buffer background         #FFFFEA
	Text         Color // normal text                    #000000
	SelectionBG  Color // selection highlight background #9FEEEE
	SelectionFG  Color // selection highlight text       #000000
	Border       Color // pane border and divider        #888888
	HeaderBG     Color // pane header background         #EEEEDD
	HeaderFG     Color // pane header text               #000000
	Scrollbar    Color // file pane scrollbar            #99994C
	CmdwinBG     Color // command window background      #EAFFFF
	CmdwinFG     Color // command window text            #000000
	CmdwinBorder Color // command window border          #8888CC
	CmdwinScroll Color // command window scrollbar       #8888CC
}

// Config holds all Dave configuration settings.
type Config struct {
	Font        string  // TTF font name; empty = embedded Go Mono
	FontSize    float64 // font size in points (default 14)
	FontMode    string  // "ttf" or "plan9" (default "ttf")
	Plan9Font   string  // full path to Plan 9 font file; only used when FontMode = "plan9"
	Plan9Embiggen bool   // render Plan 9 fonts at 150% scale (default false)
	WordWrap    bool    // soft word wrap on/off (default true)
	Margin      int     // olivetti margin width in chars (default 80)
	Split       string  // "vertical" or "horizontal" (default "vertical")
	TabWidth    int     // tab stop width in spaces (default 4)
	LineNumbers  bool    // show line numbers (default false)
	UsePwsh      bool    // Windows only: use pwsh instead of powershell.exe (default true)
	CommandLines int     // initial command window height in lines (default 10)
	Colors       Colors
}

// Default returns a Config populated with all spec defaults.
func Default() *Config {
	return &Config{
		FontSize:    14,
		FontMode:    "ttf",
		WordWrap:    true,
		Margin:      80,
		Split:       "vertical",
		TabWidth:    4,
		LineNumbers:  false,
		UsePwsh:      true,
		CommandLines: 10,
		Colors: Colors{
			Background:   Color{0xFF, 0xFF, 0xEA},
			Text:         Color{0x00, 0x00, 0x00},
			SelectionBG:  Color{0x9F, 0xEE, 0xEE},
			SelectionFG:  Color{0x00, 0x00, 0x00},
			Border:       Color{0x88, 0x88, 0x88},
			HeaderBG:     Color{0xEE, 0xEE, 0xDD},
			HeaderFG:     Color{0x00, 0x00, 0x00},
			Scrollbar:    Color{0x99, 0x99, 0x4C},
			CmdwinBG:     Color{0xEA, 0xFF, 0xFF},
			CmdwinFG:     Color{0x00, 0x00, 0x00},
			CmdwinBorder: Color{0x88, 0x88, 0xCC},
			CmdwinScroll: Color{0x88, 0x88, 0xCC},
		},
	}
}

// Load reads the config file from the OS-appropriate path and parses it.
// If the file does not exist, it is created and populated with commented
// defaults before returning the default Config.
func Load() (*Config, error) {
	path := configPath()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		_ = writeDefaults(path) // best-effort; ignore errors
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer f.Close()
	return Parse(f)
}

// writeDefaults creates the config file at path (and any parent directories)
// and writes all settings as commented-out key=value lines.
func writeDefaults(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprint(f, defaultConfigText)
	return err
}

// defaultConfigText is the content written to a newly-created config file.
// Every line is commented out so the file documents the available options
// without overriding any built-in defaults.
const defaultConfigText = `# Dave configuration file
# Uncomment and edit any line to override the default value.

# font          = Go Mono
# font_size     = 14
# font_mode     = ttf
# plan9_font    =
# plan9_embiggen = false

# word_wrap     = true
# margin        = 80
# split         = vertical
# tab_width     = 4
# line_numbers  = false

# use_pwsh      = true
# command_lines  = 10

# color_background    = #FFFFEA
# color_text          = #000000
# color_selection_bg  = #9FEEEE
# color_selection_fg  = #000000
# color_border        = #888888
# color_header_bg     = #EEEEDD
# color_header_fg     = #000000
# color_scrollbar     = #99994C
# color_cmdwin_bg     = #EAFFFF
# color_cmdwin_fg     = #000000
# color_cmdwin_border = #8888CC
# color_cmdwin_scroll = #8888CC
`

// Parse reads config key=value pairs from r, starting from the defaults.
// Returns an error if any line is malformed or contains an unknown key.
func Parse(r io.Reader) (*Config, error) {
	cfg := Default()
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("config: line %d: expected key = value, got %q", lineNum, line)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if err := applyKey(cfg, k, v); err != nil {
			return nil, fmt.Errorf("config: line %d: %w", lineNum, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: reading: %w", err)
	}
	return cfg, nil
}

// ShellArgs returns the shell executable and its argument prefix for running
// shell commands. Call this once at startup and cache the result.
func (c *Config) ShellArgs() (string, []string) {
	if runtime.GOOS == "windows" {
		if c.UsePwsh {
			return "pwsh", []string{"-Command"}
		}
		return "powershell.exe", []string{"-Command",
			"[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;"}
	}
	return "sh", []string{"-c"}
}

// configPath returns the OS-appropriate config file path.
func configPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "dave", "config")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dave", "config")
}

// applyKey applies a single parsed key=value pair to cfg.
func applyKey(cfg *Config, k, v string) error {
	switch k {
	case "font":
		cfg.Font = v
	case "font_size":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid font_size %q: want positive number", v)
		}
		cfg.FontSize = n
	case "font_mode":
		if v != "ttf" && v != "plan9" {
			return fmt.Errorf("invalid font_mode %q: want ttf or plan9", v)
		}
		cfg.FontMode = v
	case "plan9_font":
		cfg.Plan9Font = v
	case "plan9_embiggen":
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("invalid plan9_embiggen %q: %w", v, err)
		}
		cfg.Plan9Embiggen = b
	case "word_wrap":
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("invalid word_wrap %q: %w", v, err)
		}
		cfg.WordWrap = b
	case "margin":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid margin %q: want non-negative integer", v)
		}
		cfg.Margin = n
	case "split":
		if v != "vertical" && v != "horizontal" {
			return fmt.Errorf("invalid split %q: want vertical or horizontal", v)
		}
		cfg.Split = v
	case "tab_width":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid tab_width %q: want positive integer", v)
		}
		cfg.TabWidth = n
	case "line_numbers":
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("invalid line_numbers %q: %w", v, err)
		}
		cfg.LineNumbers = b
	case "use_pwsh":
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("invalid use_pwsh %q: %w", v, err)
		}
		cfg.UsePwsh = b
	case "command_lines":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid command_lines %q: want positive integer", v)
		}
		cfg.CommandLines = n
	case "color_background":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.Background = c
	case "color_text":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.Text = c
	case "color_selection_bg":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.SelectionBG = c
	case "color_selection_fg":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.SelectionFG = c
	case "color_border":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.Border = c
	case "color_header_bg":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.HeaderBG = c
	case "color_header_fg":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.HeaderFG = c
	case "color_scrollbar":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.Scrollbar = c
	case "color_cmdwin_bg":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.CmdwinBG = c
	case "color_cmdwin_fg":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.CmdwinFG = c
	case "color_cmdwin_border":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.CmdwinBorder = c
	case "color_cmdwin_scroll":
		c, err := parseHexColor(v)
		if err != nil {
			return err
		}
		cfg.Colors.CmdwinScroll = c
	default:
		return fmt.Errorf("unknown config key %q", k)
	}
	return nil
}

// parseBool accepts true/false/1/0/yes/no/on/off (case-insensitive).
func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("want true/false/yes/no/1/0/on/off")
}

// parseHexColor parses a #RRGGBB color string.
func parseHexColor(s string) (Color, error) {
	orig := s
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return Color{}, fmt.Errorf("invalid color %q: want #RRGGBB", orig)
	}
	r, err1 := strconv.ParseUint(s[0:2], 16, 8)
	g, err2 := strconv.ParseUint(s[2:4], 16, 8)
	b, err3 := strconv.ParseUint(s[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return Color{}, fmt.Errorf("invalid color %q: non-hex digit", orig)
	}
	return Color{uint8(r), uint8(g), uint8(b)}, nil
}
