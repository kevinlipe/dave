package buffer

import (
	"bytes"
	"unicode/utf8"
)

// Line addressing.
//
// The index maps 1-based line numbers to the codepoint offset where each
// line begins. Building it is a single pass over the buffer, so it is built
// lazily on the first line query after the buffer changes: a command that
// resolves many line addresses against one generation pays for the scan
// once, and a session that never uses a line address never pays at all.
//
// Line numbering follows Sam. An empty buffer has one empty line. A
// trailing newline begins a further empty line, so "a\nb\n" has three
// lines, the third being the empty string at the end of the buffer.
type lineIndex struct {
	starts []int // starts[i] is the codepoint offset of line i+1; starts[0] == 0
	gen    int   // buffer generation the table was built from
	built  bool  // gen 0 is a real generation, so validity needs its own flag
}

// lineStarts returns the line-start table, rebuilding it if the buffer has
// changed since it was last built.
func (b *Buffer) lineStarts() []int {
	if !b.lines.built || b.lines.gen != b.gen {
		b.rebuildLines()
	}
	return b.lines.starts
}

// rebuildLines scans the buffer and records the codepoint offset of every
// line start. It walks the two gap segments directly rather than going
// through String, so it neither copies the buffer nor allocates beyond
// growing the table itself, which is reused across rebuilds.
//
// A newline is a single byte and the gap always sits on a rune boundary, so
// no line terminator can straddle the gap.
func (b *Buffer) rebuildLines() {
	starts := append(b.lines.starts[:0], 0)
	runes := 0
	for seg := 0; seg < 2; seg++ {
		p := b.data[:b.gapStart]
		if seg == 1 {
			p = b.data[b.gapEnd:]
		}
		for len(p) > 0 {
			i := bytes.IndexByte(p, '\n')
			if i < 0 {
				runes += utf8.RuneCount(p)
				break
			}
			runes += utf8.RuneCount(p[:i+1])
			starts = append(starts, runes)
			p = p[i+1:]
		}
	}

	b.lines.starts = starts
	b.lines.gen = b.gen
	b.lines.built = true
}

// LineCount returns the number of lines in the buffer. It is never zero:
// an empty buffer has a single empty line.
func (b *Buffer) LineCount() int { return len(b.lineStarts()) }

// LineRange returns the codepoint range of 1-based line n, including its
// terminating newline, and reports whether line n exists. The last line
// runs to the end of the buffer.
func (b *Buffer) LineRange(n int) (Range, bool) {
	starts := b.lineStarts()
	if n < 1 || n > len(starts) {
		return Range{}, false
	}
	end := b.totalRunes
	if n < len(starts) {
		end = starts[n]
	}
	return Range{Start: starts[n-1], End: end}, true
}

// LineNumber returns the 1-based number of the line containing codepoint
// offset pos. A pos sitting exactly on a line start belongs to the line it
// starts, and a pos past the end of the buffer reports the last line.
func (b *Buffer) LineNumber(pos int) int {
	starts := b.lineStarts()
	// Index of the last line start <= pos.
	lo, hi := 0, len(starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if starts[mid] <= pos {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}
