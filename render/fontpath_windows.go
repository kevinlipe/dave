package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// resolveFontPath resolves a font family name to TTF/OTF bytes on Windows.
// It reads the Windows font registry (the authoritative name→file mapping),
// checking per-user fonts (HKCU) before system fonts (HKLM).
func resolveFontPath(name string) ([]byte, error) {
	fontDirs := systemFontDirs()
	nameLower := strings.ToLower(name)

	const fontRegPath = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`

	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, fontRegPath, registry.READ)
		if err != nil {
			continue
		}

		valueNames, err := k.ReadValueNames(-1)
		k.Close()
		if err != nil {
			continue
		}

		k, err = registry.OpenKey(root, fontRegPath, registry.READ)
		if err != nil {
			continue
		}
		filename, ok := bestFontMatch(k, valueNames, nameLower)
		k.Close()

		if ok {
			if data, err := readFontFile(filename, fontDirs); err == nil {
				return data, nil
			}
		}
	}

	return nil, fmt.Errorf("render: font %q not found", name)
}

// bestFontMatch picks the best (most Regular) font file for nameLower from
// the registry key's values.
func bestFontMatch(k registry.Key, valueNames []string, nameLower string) (string, bool) {
	type candidate struct {
		filename string
		score    int
	}
	var best *candidate

	for _, vname := range valueNames {
		filename, _, err := k.GetStringValue(vname)
		if err != nil {
			continue
		}

		// Registry entry format: "Family Style (TrueType)" or "Family (TrueType)".
		// Strip the trailing " (TrueType)" / " (OpenType)" qualifier.
		entry := vname
		if i := strings.LastIndex(entry, " ("); i >= 0 {
			entry = entry[:i]
		}
		entryLower := strings.ToLower(entry)

		if !strings.HasPrefix(entryLower, nameLower) {
			continue
		}

		suffix := strings.TrimSpace(entryLower[len(nameLower):])
		score := variantScore(suffix)

		if best == nil || score < best.score {
			best = &candidate{filename: filename, score: score}
		}
	}

	if best == nil {
		return "", false
	}
	return best.filename, true
}

// systemFontDirs returns the per-user then system font directories.
func systemFontDirs() []string {
	var dirs []string
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		dirs = append(dirs, filepath.Join(localAppData, "Microsoft", "Windows", "Fonts"))
	}
	windir := os.Getenv("WINDIR")
	if windir == "" {
		windir = `C:\Windows`
	}
	dirs = append(dirs, filepath.Join(windir, "Fonts"))
	return dirs
}

// readFontFile reads a font file. The registry value may be a bare filename
// ("consola.ttf") or an absolute path (common for per-user installed fonts).
func readFontFile(filename string, fontDirs []string) ([]byte, error) {
	if filepath.IsAbs(filename) {
		return os.ReadFile(filename)
	}
	for _, dir := range fontDirs {
		if data, err := os.ReadFile(filepath.Join(dir, filename)); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("font file %q not found", filename)
}

// variantScore ranks a font style suffix: lower is better (0 = Regular).
func variantScore(suffix string) int {
	suffix = strings.Trim(suffix, "-_ ")
	switch suffix {
	case "", "regular":
		return 0
	}
	bold := strings.Contains(suffix, "bold")
	italic := strings.Contains(suffix, "italic") || strings.Contains(suffix, "oblique")
	switch {
	case bold && italic:
		return 3
	case bold:
		return 2
	default:
		return 1
	}
}
