package render

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// resolveFontPath resolves a font name to a file path using fontconfig.
// Returns the raw TTF/OTF bytes, or an error if the font cannot be found.
func resolveFontPath(name string) ([]byte, error) {
	out, err := exec.Command("fc-match", "--format=%{file}", name).Output()
	if err != nil {
		return nil, fmt.Errorf("render: fc-match %q: %w", name, err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return nil, fmt.Errorf("render: fc-match %q: empty result", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("render: reading font %q: %w", path, err)
	}
	return data, nil
}
