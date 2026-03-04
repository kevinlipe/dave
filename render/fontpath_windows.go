package render

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveFontPath resolves a font name to TTF bytes on Windows.
// Uses PowerShell to query installed fonts; falls back to scanning %WINDIR%\Fonts.
func resolveFontPath(name string) ([]byte, error) {
	// Try scanning WINDIR\Fonts for a matching file name.
	windir := os.Getenv("WINDIR")
	if windir == "" {
		windir = `C:\Windows`
	}
	fontsDir := filepath.Join(windir, "Fonts")
	entries, err := os.ReadDir(fontsDir)
	if err == nil {
		nameLower := strings.ToLower(name)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fileLower := strings.ToLower(e.Name())
			if strings.Contains(fileLower, nameLower) &&
				(strings.HasSuffix(fileLower, ".ttf") || strings.HasSuffix(fileLower, ".otf")) {
				path := filepath.Join(fontsDir, e.Name())
				data, err := os.ReadFile(path)
				if err == nil {
					return data, nil
				}
			}
		}
	}

	// Try fc-match if fontconfig happens to be installed (WSL-adjacent environments).
	out, err := exec.Command("fc-match", "--format=%{file}", name).Output()
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			if data, err := os.ReadFile(path); err == nil {
				return data, nil
			}
		}
	}

	return nil, fmt.Errorf("render: font %q not found", name)
}
