package input

import (
	"strings"
	"testing"
)

// The command window caches its lines and the codepoint offset each one
// starts at. Drawing, hit-testing and EnsureVisible all read those offsets;
// they used to recompute them by converting every preceding line to runes,
// which cost the whole scrollback on every frame and every keystroke. These
// check the table against the running computation it replaced.

func mkCmdWin(text string) *CmdWindow {
	cw := NewCmdWindow()
	if text != "" {
		cw.Buf().Insert(0, text)
	}
	return cw
}

// naiveStarts is the accumulation the drawing loop used to do inline.
func naiveStarts(lines []string) []int {
	out := make([]int, len(lines))
	off := 0
	for i, ln := range lines {
		out[i] = off
		off += len([]rune(ln)) + 1 // +1 for the \n
	}
	return out
}

// naiveCursorLine is the scan EnsureVisible used to do: the first line whose
// end is at or past cp.
func naiveCursorLine(lines []string, cp int) int {
	lineStart := 0
	for i, line := range lines {
		lineEnd := lineStart + len([]rune(line))
		if cp <= lineEnd {
			return i
		}
		lineStart = lineEnd + 1
	}
	return len(lines) - 1
}

var cmdWinTexts = []string{
	"",
	"a",
	"a\n",
	"one\ntwo\nthree\n",
	"\n\n\n",
	"héllo wörld\n日本語\n😀\n",
	"trailing text with no newline",
	"\nleading newline",
}

func TestCmdWinLineStartsMatchTheRunningCount(t *testing.T) {
	for _, text := range cmdWinTexts {
		cw := mkCmdWin(text)
		lines := cw.getLines()
		got := cw.lineStarts()
		want := naiveStarts(lines)
		if len(got) != len(lines) {
			t.Errorf("%q: %d starts for %d lines", text, len(got), len(lines))
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: starts[%d] = %d, want %d", text, i, got[i], want[i])
			}
		}
	}
}

// Every line start must be where the buffer actually says that line begins.
func TestCmdWinLineStartsAgreeWithTheBuffer(t *testing.T) {
	for _, text := range cmdWinTexts {
		cw := mkCmdWin(text)
		for i, start := range cw.lineStarts() {
			line := cw.getLines()[i]
			n := len([]rune(line))
			if got := cw.Buf().ReadAt(start, n); got != line {
				t.Errorf("%q: line %d at offset %d reads %q, want %q",
					text, i, start, got, line)
			}
		}
	}
}

func TestCmdWinLineAtMatchesTheOldScan(t *testing.T) {
	for _, text := range cmdWinTexts {
		cw := mkCmdWin(text)
		lines := cw.getLines()
		for cp := 0; cp <= cw.Buf().Len(); cp++ {
			if got, want := cw.lineAt(cp), naiveCursorLine(lines, cp); got != want {
				t.Errorf("%q: lineAt(%d) = %d, want %d", text, cp, got, want)
			}
		}
	}
}

// The cache is keyed on the buffer generation, so an edit has to invalidate
// it -- including one that changes offsets without changing the line count.
func TestCmdWinLineCacheFollowsEdits(t *testing.T) {
	cw := mkCmdWin("one\ntwo\n")
	if got := cw.lineStarts(); got[1] != 4 {
		t.Fatalf("starts[1] = %d, want 4", got[1])
	}
	cw.Buf().Insert(0, "xx")
	if got := cw.lineStarts(); got[1] != 6 {
		t.Errorf("after insert: starts[1] = %d, want 6", got[1])
	}
	cw.Buf().Insert(0, "a\nb\n")
	lines := cw.getLines()
	want := naiveStarts(lines)
	for i := range want {
		if cw.lineStarts()[i] != want[i] {
			t.Errorf("after second insert: starts[%d] = %d, want %d",
				i, cw.lineStarts()[i], want[i])
		}
	}
}

// AppendOutput is how command results reach the window, and it is the reason
// the buffer grows without bound; the offsets must track it.
func TestCmdWinLineStartsAfterAppendOutput(t *testing.T) {
	cw := NewCmdWindow()
	for i := 0; i < 20; i++ {
		cw.AppendOutput("output line\n")
	}
	lines := cw.getLines()
	want := naiveStarts(lines)
	got := cw.lineStarts()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("starts[%d] = %d, want %d", i, got[i], want[i])
		}
	}
	// And the last real line still reads back correctly from its offset.
	last := len(lines) - 1
	for last > 0 && lines[last] == "" {
		last--
	}
	if got := cw.Buf().ReadAt(cw.lineStarts()[last], len([]rune(lines[last]))); got != lines[last] {
		t.Errorf("last line reads %q, want %q", got, lines[last])
	}
}

// A multibyte line must advance offsets by codepoints, not bytes.
func TestCmdWinLineStartsAreCodepoints(t *testing.T) {
	cw := mkCmdWin("héllo\n😀😀\nend\n")
	starts := cw.lineStarts()
	// "héllo" is 5 codepoints (7 bytes), so line 1 starts at 6.
	if starts[1] != 6 {
		t.Errorf("starts[1] = %d, want 6 (codepoints, not bytes)", starts[1])
	}
	// Two emoji are 2 codepoints (8 bytes), so line 2 starts at 6+2+1 = 9.
	if starts[2] != 9 {
		t.Errorf("starts[2] = %d, want 9", starts[2])
	}
	if got := cw.Buf().ReadAt(starts[2], 3); got != "end" {
		t.Errorf("line 2 reads %q, want %q", got, "end")
	}
}

func TestCmdWinGetLinesMatchesSplit(t *testing.T) {
	for _, text := range cmdWinTexts {
		cw := mkCmdWin(text)
		want := strings.Split(text, "\n")
		got := cw.getLines()
		if len(got) != len(want) {
			t.Errorf("%q: %d lines, want %d", text, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: line %d = %q, want %q", text, i, got[i], want[i])
			}
		}
	}
}
