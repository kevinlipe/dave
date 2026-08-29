package render

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// resolveFontPath resolves a font name to a file path using fontconfig.
// Returns the raw TTF/OTF bytes, or an error if the font cannot be found or
// is not a TrueType/CFF font (Type1, bitmap, etc. are not supported).
func resolveFontPath(name string) ([]byte, error) {
	out, err := exec.Command("fc-match", "--format=%{fontformat}|%{file}", name).Output()
	if err != nil {
		return nil, fmt.Errorf("render: fc-match %q: %w", name, err)
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("render: fc-match %q: unexpected output", name)
	}
	format, path := parts[0], parts[1]
	if format != "TrueType" && format != "CFF" {
		return nil, fmt.Errorf("render: font %q resolved to unsupported format %q", name, format)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("render: reading font %q: %w", path, err)
	}
	return data, nil
}
