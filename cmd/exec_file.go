package cmd

import (
	"dave/buffer"
	"dave/file"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

func (s *Session) execN() error {
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current, false)
		s.printf("%s\n", ml)
	}
	return nil
}

func (s *Session) execB(arg string) error {
	names := strings.Fields(arg)
	for _, name := range names {
		f := s.FindFile(name)
		if f != nil {
			s.Current = f
			return nil
		}
	}
	if len(names) > 0 {
		return fmt.Errorf("b: %q not in buffer list", names[0])
	}
	return nil
}

func (s *Session) execBig(arg string) error {
	names := strings.Fields(arg)
	for _, name := range names {
		f := s.FindFile(name)
		if f == nil {
			f = file.NewNamed(name)
			s.AddFile(f)
		}
		s.Current = f
		s.OpenRequest = f
		s.printf("%s\n", f.MenuLine(true, false))
	}
	return nil
}

func (s *Session) execD(arg string) error {
	names := strings.Fields(arg)
	if len(names) == 0 || (len(names) == 1 && names[0] == "") {
		if s.Current == nil {
			return fmt.Errorf("D: no current file")
		}
		return s.RemoveFile(s.Current, false)
	}
	for _, name := range names {
		f := s.FindFile(name)
		if f == nil {
			return fmt.Errorf("D: %q not in buffer list", name)
		}
		if err := s.RemoveFile(f, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) execE(f *file.File, name string, ctx *execCtx) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = f.Name()
	}
	if name == "" {
		return fmt.Errorf("e: no filename")
	}
	// Load new content.
	data, err := os.ReadFile(name)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("e: %w", err)
	}
	newContent := f.DecodeContent(string(data))
	oldLen := f.Len()
	// Replace all old content with new content via transcript.
	if oldLen > 0 || newContent != "" {
		del := oldLen
		if err := ctx.addChange(f, 0, del, newContent); err != nil {
			return err
		}
	}
	f.SetName(name)
	f.SetDot(buffer.Range{})
	s.printf("%s: #%d\n", name, utf8.RuneCountInString(newContent))
	return nil
}

func (s *Session) execR(f *file.File, dot buffer.Range, name string, ctx *execCtx) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("r: no filename")
	}
	content, err := file.ReadFromPath(name)
	if err != nil {
		return err
	}
	if err := ctx.addChange(f, dot.Start, dot.Len(), content); err != nil {
		return err
	}
	n := utf8.RuneCountInString(content)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	s.printf("%s: #%d\n", name, n)
	return nil
}

func (s *Session) execW(f *file.File, dot buffer.Range, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = f.Name()
	}
	if name == "" {
		return fmt.Errorf("w: no filename")
	}
	// Write addressed range, restoring original line endings if needed.
	_, statErr := os.Stat(name)
	isNew := os.IsNotExist(statErr)
	runes := f.Buffer().ReadAt(dot.Start, dot.Len())
	content := f.EncodeContent(runes)
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		return fmt.Errorf("w: %w", err)
	}
	if name == f.Name() {
		f.SetModified(false)
	}
	s.printf("%s: ", name)
	if isNew {
		s.printf("(new file) ")
	}
	if len(runes) > 0 && runes[len(runes)-1] != '\n' {
		s.printf("?warning: last char not newline\n")
	}
	s.printf("#%d\n", len(runes))
	return nil
}

func (s *Session) execF(f *file.File, name string) error {
	name = strings.TrimSpace(name)
	if name != "" {
		f.SetName(name)
	}
	s.printf("%s\n", f.MenuLine(f == s.Current, false))
	return nil
}
