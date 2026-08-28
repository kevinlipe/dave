// Package buffer implements a gap buffer with Unicode codepoint addressing,
// a transaction log, and multi-level undo.
//
// All external positions are codepoint offsets. Byte-level operations are
// internal only. This invariant is enforced by keeping all conversion in
// this package.
package buffer

import (
	"strings"
	"unicode/utf8"
)

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

	// Bounded ring of recent edits, so consumers can ask what changed since
	// a generation they remember; see edits.go.
	edits    []edit
	editHead int

	// The whole content as one string, cached until the next edit. Several
	// callers need the buffer flattened -- the regexp engine takes a string,
	// and word wrap walks runes -- and a command evaluates against one
	// unchanging state, so a structural command that searches once per match
	// asked for the same flattening thousands of times over.
	//
	// The cost is a second copy of the content resident between edits. That
	// is a real price on a large file, paid so that repeated whole-buffer
	// reads are free rather than quadratic.
	//
	// Unlike the line table and the memo below, this is not stamped with the
	// generation. It has to be dropped at the edit regardless -- a stale copy
	// left stamped would pin the size of the file until someone next asked
	// for a flattening -- and once it is dropped there, a stamp could never
	// disagree with the generation to begin with.
	flat   string
	flatOK bool

	// Memo of the previous runeToBytePos answer, used as an extra anchor so
	// that sequential access is cheap. The pair is in logical coordinates,
	// which do not shift when the gap moves, so only a content change can
	// invalidate it -- hence the gen stamp. The zero value is the true
	// statement "rune 0 is at byte 0", so it is safe before anything is set.
	memoRune int
	memoByte int
	memoGen  int
}

// Gen returns the current modification generation. Callers can cache this
// value and compare it later to detect whether the buffer has changed.
func (b *Buffer) Gen() int { return b.gen }

// bumpGen records that the content changed. Everything else keyed on the
// generation is invalidated by the stamp alone; the flattened copy is dropped
// here instead, because leaving it stamped would pin a copy of the whole file
// until something next asked for a flattening.
func (b *Buffer) bumpGen() {
	b.gen++
	b.flat, b.flatOK = "", false
}

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
// The cached rune counts pin down three fixed points — the start of the
// buffer, the gap, and the end — and the memo adds a fourth that follows
// whatever the caller last asked for. The walk starts from whichever of
// those is nearest. Editing at the gap costs O(1), and so does walking
// through the file in order, which is what a structural command does when
// it reads each match in turn.
func (b *Buffer) runeToBytePos(runePos int) int {
	if runePos < 0 {
		panic("buffer: negative rune position")
	}

	// The memo is only usable within the segment it landed in, since the two
	// segments are not contiguous in data.
	anchorRune, anchorByte := -1, -1
	if b.memoGen == b.gen {
		anchorRune, anchorByte = b.memoRune, b.memoByte
	}

	var off int
	if runePos <= b.preGapRunes {
		// In the pre-gap segment a logical byte offset is also the physical
		// one. An anchor from the post-gap segment is out of range here, and
		// segByteOffset drops it.
		off = segByteOffset(b.data[:b.gapStart], runePos, b.preGapRunes, anchorRune, anchorByte)
	} else {
		// Rebase the anchor into post-gap-local coordinates. A pre-gap anchor
		// goes negative, which segByteOffset reads as "no anchor".
		off = b.gapStart + segByteOffset(b.data[b.gapEnd:], runePos-b.preGapRunes,
			b.totalRunes-b.preGapRunes, anchorRune-b.preGapRunes, anchorByte-b.gapStart)
	}

	// A clamped lookup memoizes a pair that does not describe runePos, but
	// segByteOffset drops it on the way back in as out of range, and the
	// generation cannot change without this being overwritten.
	b.memoRune, b.memoByte, b.memoGen = runePos, off, b.gen
	return off
}

// segByteOffset returns the byte offset within segment p at which the
// target-th rune of that segment begins, given that p holds segRunes runes.
// anchorRune/anchorByte name a third known point inside p; an anchor outside
// p is ignored, which is how an anchor belonging to the other gap segment,
// or left over from a clamped lookup, is discarded. A target past the end of
// p clamps to len(p).
func segByteOffset(p []byte, target, segRunes, anchorRune, anchorByte int) int {
	// Start from whichever known point is nearest to the target.
	fromRune, fromByte, dist := 0, 0, target
	if d := segRunes - target; d < dist {
		fromRune, fromByte, dist = segRunes, len(p), d
	}
	if anchorRune >= 0 && anchorRune <= segRunes && anchorByte >= 0 && anchorByte <= len(p) {
		d := target - anchorRune
		if d < 0 {
			d = -d
		}
		if d < dist {
			fromRune, fromByte = anchorRune, anchorByte
		}
	}
	if target >= fromRune {
		return fromByte + nthRuneByteOffset(p[fromByte:], target-fromRune)
	}
	return byteOffsetFromEnd(p[:fromByte], fromRune-target)
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
	before := b.totalRunes
	b.insert(bytePos, []byte(text))
	b.bumpGen()
	b.recordEdit(pos, 0, b.totalRunes-before)
}

// Delete removes n codepoints starting at codepoint position pos.
func (b *Buffer) Delete(pos, n int) {
	if n <= 0 {
		return
	}
	byteStart := b.runeToBytePos(pos)
	byteEnd := b.runeToBytePos(pos + n)
	before := b.totalRunes
	b.delete(byteStart, byteEnd-byteStart)
	b.bumpGen()
	b.recordEdit(pos, before-b.totalRunes, 0)
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
//
// The result is cached until the buffer changes, so asking repeatedly costs
// nothing; see the flat field for what that cache is worth and what it costs.
//
// It assembles the two segments itself rather than deferring to ReadAt. The
// endpoints of a whole-buffer read are known without converting anything, so
// ReadAt's two runeToBytePos calls are not merely wasted: they leave the memo
// pinned at the end of the buffer, and the next caller asking about a position
// near dot then walks in from a fixed point instead of from where it left off.
// A loop that flattens the buffer and then converts a position -- which is
// what a regexp address does, once per match -- pays that walk every time.
func (b *Buffer) String() string {
	if b.flatOK {
		return b.flat
	}
	var s string
	switch {
	case b.gapStart == 0:
		s = string(b.data[b.gapEnd:])
	case b.gapEnd == len(b.data):
		s = string(b.data[:b.gapStart])
	default:
		// Builder hands its bytes to the string without the copy that a
		// string(...) conversion of an assembled slice would make.
		var sb strings.Builder
		sb.Grow(b.byteLen())
		sb.Write(b.data[:b.gapStart])
		sb.Write(b.data[b.gapEnd:])
		s = sb.String()
	}
	b.flat, b.flatOK = s, true
	return s
}

// ByteOffset returns the byte offset at which codepoint runePos begins,
// measured in the buffer's logical content -- that is, the offset that
// codepoint has in the string String returns. It panics if runePos is
// negative; offsets past the end clamp to the end of the content.
//
// This is the one place the codepoint-only rule is relaxed, and only for a
// caller that already holds a flattened copy of the buffer and needs to index
// into it. Counting the bytes itself costs a pass over everything before
// runePos; this shares runeToBytePos's fixed points and memo, so walking
// forward through a file costs the distance between successive questions.
func (b *Buffer) ByteOffset(runePos int) int {
	return b.runeToBytePos(runePos)
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
