package render

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveFontPath resolves a font name to TTF bytes on Windows.
// Scans per-user fonts (%LOCALAPPDATA%\Microsoft\Windows\Fonts) then
// system fonts (%WINDIR%\Fonts), then tries fc-match as a last resort.
func resolveFontPath(name string) ([]byte, error) {
	// Build the list of directories to search: per-user first, then system.
	var fontDirs []string
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		fontDirs = append(fontDirs, filepath.Join(localAppData, "Microsoft", "Windows", "Fonts"))
	}
	windir := os.Getenv("WINDIR")
	if windir == "" {
		windir = `C:\Windows`
	}
	fontDirs = append(fontDirs, filepath.Join(windir, "Fonts"))

	nameLower := strings.ToLower(name)
	nameCompact := strings.ReplaceAll(nameLower, " ", "")

	for _, fontsDir := range fontDirs {
		entries, err := os.ReadDir(fontsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			fileLower := strings.ToLower(e.Name())
			fileCompact := strings.ReplaceAll(fileLower, " ", "")
			if strings.Contains(fileCompact, nameCompact) &&
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
