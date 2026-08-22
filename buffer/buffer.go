// Package buffer implements a gap buffer with Unicode codepoint addressing,
// a transaction log, and multi-level undo.
//
// All external positions are codepoint offsets. Byte-level operations are
// internal only. This invariant is enforced by keeping all conversion in
// this package.
package buffer

import "unicode/utf8"

const defaultGapSize = 4096

// Buffer is a gap buffer holding UTF-8 text. All positions exposed via the
// public API are codepoint (rune) offsets, not byte offsets.
//
// The gap always begins and ends on a rune boundary: gapStart is only ever
// set from a rune-aligned byte offset, and content is only ever added or
// removed in whole runes. preGapRunes and totalRunes rely on that invariant.
type Buffer struct {
	data     []byte // entire storage: [0..gapStart) ++ gap ++ [gapEnd..len)
	gapStart int    // first byte of the gap (byte index)
	gapEnd   int    // first byte after the gap (byte index)
	gen      int    // incremented on every Insert or Delete

	// Cached rune counts. Maintained incrementally so that converting a
	// codepoint offset to a byte offset never has to scan the whole
	// buffer; see runeToBytePos.
	preGapRunes int // runes in data[:gapStart]
	totalRunes  int // runes in the whole logical content

	// Line-start table, rebuilt lazily when gen moves; see lines.go.
	lines lineIndex
}

// Gen returns the current modification generation. Callers can cache this
// value and compare it later to detect whether the buffer has changed.
func (b *Buffer) Gen() int { return b.gen }

// New returns an empty Buffer.
func New() *Buffer {
	b := &Buffer{}
	b.data = make([]byte, defaultGapSize)
	b.gapStart = 0
	b.gapEnd = defaultGapSize
	return b
}

// NewFromString creates a Buffer pre-loaded with s.
func NewFromString(s string) *Buffer {
	b := New()
	b.insert(0, []byte(s))
	return b
}

// Len returns the number of codepoints in the buffer.
func (b *Buffer) Len() int { return b.totalRunes }

// byteLen returns the number of content bytes (excluding the gap).
func (b *Buffer) byteLen() int {
	return b.gapStart + len(b.data) - b.gapEnd
}

// gapSize returns the size of the gap in bytes.
func (b *Buffer) gapSize() int { return b.gapEnd - b.gapStart }

// runeToBytePos converts a codepoint offset to a byte offset in the
// logical (gap-collapsed) content. It panics if runePos is negative;
// offsets past the end clamp to the end of the content.
//
// The cached rune counts pin down three known offsets — the start of the
// buffer, the gap, and the end — so the walk is always from the nearest of
// the two bounding the segment runePos falls in. Editing at or near the gap,
// which is where editing normally happens, therefore costs O(1).
func (b *Buffer) runeToBytePos(runePos int) int {
	if runePos < 0 {
		panic("buffer: negative rune position")
	}
	if runePos <= b.preGapRunes {
		pre := b.data[:b.gapStart]
		if runePos*2 <= b.preGapRunes {
			return nthRuneByteOffset(pre, runePos)
		}
		return byteOffsetFromEnd(pre, b.preGapRunes-runePos)
	}
	post := b.data[b.gapEnd:]
	postPos := runePos - b.preGapRunes
	postRunes := b.totalRunes - b.preGapRunes
	if postPos*2 <= postRunes {
		return b.gapStart + nthRuneByteOffset(post, postPos)
	}
	// Also covers runePos > totalRunes: the count is negative, the walk
	// does not run, and the result clamps to the end of the content.
	return b.gapStart + byteOffsetFromEnd(post, postRunes-postPos)
}

// nthRuneByteOffset returns the byte offset of the n-th rune in p.
// If n >= len(runes in p), it returns len(p) (the end position).
func nthRuneByteOffset(p []byte, n int) int {
	off := 0
	for i := 0; i < n; i++ {
		_, sz := utf8.DecodeRune(p[off:])
		if sz == 0 {
			break
		}
		off += sz
	}
	return off
}

// byteOffsetFromEnd returns the byte offset in p of the position n runes
// before the end of p. If n <= 0 it returns len(p).
func byteOffsetFromEnd(p []byte, n int) int {
	off := len(p)
	for i := 0; i < n; i++ {
		_, sz := utf8.DecodeLastRune(p[:off])
		if sz == 0 {
			break
		}
		off -= sz
	}
	return off
}

// moveGap moves the gap so that gapStart == bytePos (a logical byte offset).
func (b *Buffer) moveGap(bytePos int) {
	if bytePos == b.gapStart {
		return
	}
	if bytePos < b.gapStart {
		// Move gap left: copy bytes from just before gap to just after gap
		n := b.gapStart - bytePos
		moved := b.data[bytePos:b.gapStart]
		b.preGapRunes -= utf8.RuneCount(moved)
		copy(b.data[b.gapEnd-n:], moved)
		b.gapEnd -= n
		b.gapStart = bytePos
	} else {
		// bytePos is a logical byte offset; actual position in data is
		// bytePos + gapSize (since the gap sits in the middle).
		// But bytePos here is already the logical position; we need the
		// physical position in data[gapEnd:].
		//
		// Logical byte offset > gapStart means we're in the post-gap segment.
		// The physical start of post-gap content is gapEnd.
		// The amount to move is bytePos - gapStart.
		n := bytePos - b.gapStart
		moved := b.data[b.gapEnd : b.gapEnd+n]
		b.preGapRunes += utf8.RuneCount(moved)
		copy(b.data[b.gapStart:], moved)
		b.gapStart += n
		b.gapEnd += n
	}
}

// grow ensures the gap has at least need bytes.
func (b *Buffer) grow(need int) {
	gapSize := b.gapEnd - b.gapStart
	if gapSize >= need {
		return
	}
	newGap := need + defaultGapSize
	newData := make([]byte, len(b.data)+newGap-gapSize)
	copy(newData, b.data[:b.gapStart])
	newGapEnd := b.gapStart + newGap
	copy(newData[newGapEnd:], b.data[b.gapEnd:])
	b.data = newData
	b.gapEnd = newGapEnd
}

// insert inserts text (bytes) at logical byte position bytePos.
func (b *Buffer) insert(bytePos int, text []byte) {
	b.grow(len(text))
	b.moveGap(bytePos)
	copy(b.data[b.gapStart:], text)
	b.gapStart += len(text)
	n := utf8.RuneCount(text)
	b.preGapRunes += n
	b.totalRunes += n
}

// delete removes n bytes starting at logical byte position bytePos. The
// bytes removed are the ones immediately following the gap, so preGapRunes
// is unaffected.
func (b *Buffer) delete(bytePos, n int) {
	b.moveGap(bytePos)
	b.totalRunes -= utf8.RuneCount(b.data[b.gapEnd : b.gapEnd+n])
	b.gapEnd += n
}

// Insert inserts text at codepoint position pos.
func (b *Buffer) Insert(pos int, text string) {
	bytePos := b.runeToBytePos(pos)
	b.insert(bytePos, []byte(text))
	b.gen++
}

// Delete removes n codepoints starting at codepoint position pos.
func (b *Buffer) Delete(pos, n int) {
	if n <= 0 {
		return
	}
	byteStart := b.runeToBytePos(pos)
	byteEnd := b.runeToBytePos(pos + n)
	b.delete(byteStart, byteEnd-byteStart)
	b.gen++
}

// ReadAt returns the string of n codepoints starting at codepoint offset pos.
// The range is clamped to the buffer, so a short read near the end returns
// however many codepoints are actually there.
func (b *Buffer) ReadAt(pos, n int) string {
	if n <= 0 {
		return ""
	}
	lo, hi := pos, pos+n
	if lo < 0 {
		lo = 0
	}
	if hi <= lo {
		return ""
	}
	start := b.runeToBytePos(lo)
	end := b.runeToBytePos(hi)
	if end <= start {
		return ""
	}
	// start and end are logical offsets; shift by the gap size to index
	// data for anything at or after the gap.
	gap := b.gapSize()
	if end <= b.gapStart {
		return string(b.data[start:end]) // wholly pre-gap
	}
	if start >= b.gapStart {
		return string(b.data[start+gap : end+gap]) // wholly post-gap
	}
	out := make([]byte, 0, end-start)
	out = append(out, b.data[start:b.gapStart]...)
	out = append(out, b.data[b.gapEnd:end+gap]...)
	return string(out)
}

// String returns the entire buffer contents as a string.
func (b *Buffer) String() string {
	return b.ReadAt(0, b.totalRunes)
}

// RuneAt returns the codepoint at position pos, or -1 if pos is out of range.
func (b *Buffer) RuneAt(pos int) rune {
	if pos < 0 || pos >= b.totalRunes {
		return -1
	}
	off := b.runeToBytePos(pos)
	if off >= b.gapStart {
		off += b.gapSize()
	}
	r, _ := utf8.DecodeRune(b.data[off:])
	return r
}
