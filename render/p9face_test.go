package render

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Plan 9 font path parses binary files that dave does not produce: a
// glyph image in Plan 9 image format, a subfont header, and a fontchar table.
// None of it needs SDL, so all of it can be tested directly. These build the
// wire format by hand rather than shipping a binary fixture, so the encoder
// in the test states the format the decoder is expected to accept.

// p9Field renders one 12-byte blank-padded ASCII header field.
func p9Field(v int) []byte {
	s := fmt.Sprintf("%11d ", v)
	return []byte(s)
}

// buildP9Image assembles an uncompressed Plan 9 image. pix is one byte per
// pixel in Plan 9 convention (0 = ink, 255 = paper), row-major, w*h long.
func buildP9Image(t *testing.T, chanStr string, depth, w, h int, pix []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("%-11s ", chanStr)[:12])
	b.Write(p9Field(0))
	b.Write(p9Field(0))
	b.Write(p9Field(w))
	b.Write(p9Field(h))

	stride := (w*depth + 7) / 8
	raw := make([]byte, stride*h)
	for y := 0; y < h; y++ {
		row := raw[y*stride:]
		for x := 0; x < w; x++ {
			// Invert back out of the "0 = ink" convention into the packed
			// representation the reader unpacks from.
			lum := pix[y*w+x]
			switch depth {
			case 1:
				if lum == 0 {
					row[x/8] |= 1 << uint(7-x%8)
				}
			case 2:
				row[x/4] |= ((3 - lum/85) & 3) << uint(6-2*(x%4))
			case 4:
				v := (15 - lum/17) & 0xf
				if x%2 == 0 {
					row[x/2] |= v << 4
				} else {
					row[x/2] |= v
				}
			case 8:
				row[x] = 255 - lum
			}
		}
	}
	b.Write(raw)
	return b.Bytes()
}

// compressP9 wraps raw scanline bytes in the chunked literal-run encoding the
// decompressor accepts. Literal runs only -- back-references are exercised
// separately, against a stream written by hand.
func compressP9(raw []byte, minY, maxY, stride int) []byte {
	var out bytes.Buffer
	for y := minY; y < maxY; y++ {
		row := raw[(y-minY)*stride : (y-minY+1)*stride]
		var chunk bytes.Buffer
		for off := 0; off < len(row); off += 128 {
			end := off + 128
			if end > len(row) {
				end = len(row)
			}
			chunk.WriteByte(byte(128 + (end - off) - 1))
			chunk.Write(row[off:end])
		}
		out.Write(p9Field(y + 1)) // this chunk ends at row y+1
		out.Write(p9Field(chunk.Len()))
		out.Write(chunk.Bytes())
	}
	return out.Bytes()
}

// buildSubfont appends a subfont header and fontchar table to an image.
func buildSubfont(t *testing.T, img []byte, n, height, ascent int, chars []p9Fontchar) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Write(img)
	b.Write(p9Field(n))
	b.Write(p9Field(height))
	b.Write(p9Field(ascent))
	for _, c := range chars {
		b.WriteByte(byte(c.x))
		b.WriteByte(byte(c.x >> 8))
		b.WriteByte(c.top)
		b.WriteByte(c.bottom)
		b.WriteByte(byte(c.left))
		b.WriteByte(c.width)
	}
	return b.Bytes()
}

// ---- image decoding ---------------------------------------------------------

// Every supported depth must unpack to the same one-byte-per-pixel image.
// k1 is the depth real Plan 9 fonts use; the others exist in the format.
func TestReadP9ImageAllDepths(t *testing.T) {
	const w, h = 8, 2
	for _, c := range []struct {
		chanStr string
		depth   int
		pix     []byte
	}{
		{"k1", 1, []byte{
			0, 255, 0, 255, 0, 255, 0, 255,
			255, 0, 255, 0, 255, 0, 255, 0,
		}},
		{"k2", 2, []byte{
			0, 85, 170, 255, 0, 85, 170, 255,
			255, 170, 85, 0, 255, 170, 85, 0,
		}},
		{"k4", 4, []byte{
			0, 17, 34, 51, 204, 221, 238, 255,
			255, 238, 221, 204, 51, 34, 17, 0,
		}},
		{"k8", 8, []byte{
			0, 1, 2, 3, 252, 253, 254, 255,
			255, 254, 253, 252, 3, 2, 1, 0,
		}},
	} {
		data := buildP9Image(t, c.chanStr, c.depth, w, h, c.pix)
		img, err := readP9Image(bufio.NewReader(bytes.NewReader(data)))
		if err != nil {
			t.Errorf("%s: %v", c.chanStr, err)
			continue
		}
		if img.depth != c.depth {
			t.Errorf("%s: depth = %d, want %d", c.chanStr, img.depth, c.depth)
		}
		if got := img.rect.Dx(); got != w {
			t.Errorf("%s: width = %d, want %d", c.chanStr, got, w)
		}
		if !bytes.Equal(img.pix, c.pix) {
			t.Errorf("%s: pix = %v, want %v", c.chanStr, img.pix, c.pix)
		}
	}
}

// A compressed image must decode to exactly what the same pixels produce
// uncompressed -- the property that matters, since real font files are
// compressed and the test fixtures above are not.
func TestReadP9ImageCompressedMatchesRaw(t *testing.T) {
	const w, h, depth = 16, 4, 1
	pix := make([]byte, w*h)
	for i := range pix {
		if (i/3)%2 == 0 {
			pix[i] = 0
		} else {
			pix[i] = 255
		}
	}
	rawFile := buildP9Image(t, "k1", depth, w, h, pix)
	want, err := readP9Image(bufio.NewReader(bytes.NewReader(rawFile)))
	if err != nil {
		t.Fatalf("uncompressed: %v", err)
	}

	stride := (w*depth + 7) / 8
	header := rawFile[:60]
	body := rawFile[60:]
	var comp bytes.Buffer
	comp.WriteString("compressed\n")
	comp.Write(header)
	comp.Write(compressP9(body, 0, h, stride))

	got, err := readP9Image(bufio.NewReader(bytes.NewReader(comp.Bytes())))
	if err != nil {
		t.Fatalf("compressed: %v", err)
	}
	if !bytes.Equal(got.pix, want.pix) {
		t.Errorf("compressed pix = %v\nuncompressed  = %v", got.pix, want.pix)
	}
}

func TestReadP9ImageRejectsBadHeaders(t *testing.T) {
	good := buildP9Image(t, "k1", 1, 8, 1, make([]byte, 8))
	for _, c := range []struct {
		data []byte
		what string
	}{
		{[]byte("short"), "truncated magic"},
		{good[:40], "truncated header"},
		{good[:len(good)-1], "truncated pixels"},
		{buildP9Image(t, "r8", 8, 8, 1, make([]byte, 8)), "non-greyscale channel"},
		{buildP9Image(t, "k3", 8, 8, 1, make([]byte, 8)), "unsupported depth"},
	} {
		if _, err := readP9Image(bufio.NewReader(bytes.NewReader(c.data))); err == nil {
			t.Errorf("%s: decoded without error, want a failure", c.what)
		}
	}
}

// A zero-area image would make the texture path index pix[0] on an empty
// slice, so it has to be rejected here.
func TestReadP9ImageRejectsEmptyRect(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("%-11s ", "k1")[:12])
	b.Write(p9Field(0))
	b.Write(p9Field(0))
	b.Write(p9Field(0)) // maxX == minX
	b.Write(p9Field(4))
	if _, err := readP9Image(bufio.NewReader(bytes.NewReader(b.Bytes()))); err == nil {
		t.Error("empty rect decoded without error, want a failure")
	}
}

// ---- the decompressor -------------------------------------------------------

// A back-reference copies from the 1024-byte ring of recently emitted bytes.
// c < 128 means "back-ref": count is (c>>2)+3, offset is low bits plus the
// next byte, plus one.
func TestDecompressP9ChunkBackReference(t *testing.T) {
	var src bytes.Buffer
	src.WriteByte(128 + 3 - 1) // three literals
	src.Write([]byte{'a', 'b', 'c'})
	// count = (0>>2)+3 = 3, offset = 2 + 1 = 3 -> repeat the last three bytes.
	src.WriteByte(0)
	src.WriteByte(2)

	dst := make([]byte, 6)
	if err := decompressP9Chunk(src.Bytes(), dst); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if want := "abcabc"; string(dst) != want {
		t.Errorf("got %q, want %q", dst, want)
	}
}

// An overlapping back-reference reads bytes as it writes them, which is how
// runs are encoded: offset 1, count 5 repeats one byte five times.
func TestDecompressP9ChunkOverlappingRun(t *testing.T) {
	var src bytes.Buffer
	src.WriteByte(128 + 1 - 1)
	src.WriteByte('x')
	src.WriteByte(byte((5 - p9NMATCH) << 2)) // count 5
	src.WriteByte(0)                         // offset 1
	dst := make([]byte, 6)
	if err := decompressP9Chunk(src.Bytes(), dst); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if want := "xxxxxx"; string(dst) != want {
		t.Errorf("got %q, want %q", dst, want)
	}
}

// A corrupt stream must be an error, never a panic or a silent short read.
func TestDecompressP9ChunkRejectsCorruptStreams(t *testing.T) {
	for _, c := range []struct {
		src  []byte
		what string
	}{
		{[]byte{}, "empty source"},
		{[]byte{128 + 4 - 1, 'a'}, "literal run past end of source"},
		{[]byte{0}, "back-ref with no offset byte"},
		{[]byte{128 + 1 - 1, 'a', 0, 2}, "back-ref longer than the output"},
	} {
		dst := make([]byte, 8)
		if err := decompressP9Chunk(c.src, dst); err == nil {
			t.Errorf("%s: decompressed without error, want a failure", c.what)
		}
	}
}

// ---- subfont ----------------------------------------------------------------

func TestReadP9Subfont(t *testing.T) {
	const w, h = 12, 6
	img := buildP9Image(t, "k1", 1, w, h, make([]byte, w*h))
	// Two glyphs plus the terminating fontchar that gives the last one its
	// width: chars[i+1].x - chars[i].x.
	chars := []p9Fontchar{
		{x: 0, top: 1, bottom: 5, left: 0, width: 4},
		{x: 4, top: 0, bottom: 6, left: 1, width: 7},
		{x: 11},
	}
	data := buildSubfont(t, img, 2, h, 5, chars)

	sf, err := readP9Subfont(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("readP9Subfont: %v", err)
	}
	if sf.n != 2 || sf.height != h || sf.ascent != 5 {
		t.Errorf("n/height/ascent = %d/%d/%d, want 2/%d/5", sf.n, sf.height, sf.ascent, h)
	}
	if len(sf.chars) != 3 {
		t.Fatalf("chars = %d, want 3 (n+1)", len(sf.chars))
	}

	// Glyph geometry is derived from a fontchar and its successor.
	g := makeP9GlyphInfo(sf, 0)
	if g.x != 0 || g.w != 4 || g.y != 1 || g.h != 4 || g.advance != 4 {
		t.Errorf("glyph 0 = %+v, want x0 w4 y1 h4 advance4", g)
	}
	g = makeP9GlyphInfo(sf, 1)
	if g.x != 4 || g.w != 7 || g.y != 0 || g.h != 6 || g.left != 1 || g.advance != 7 {
		t.Errorf("glyph 1 = %+v, want x4 w7 y0 h6 left1 advance7", g)
	}
}

// left is signed: a negative bearing kerns a glyph back over its predecessor,
// and reading it as unsigned would push it forward instead.
func TestReadP9SubfontNegativeLeftBearing(t *testing.T) {
	img := buildP9Image(t, "k1", 1, 8, 4, make([]byte, 32))
	chars := []p9Fontchar{{x: 0, top: 0, bottom: 4, left: -2, width: 3}, {x: 8}}
	data := buildSubfont(t, img, 1, 4, 3, chars)
	sf, err := readP9Subfont(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("readP9Subfont: %v", err)
	}
	if got := makeP9GlyphInfo(sf, 0).left; got != -2 {
		t.Errorf("left = %d, want -2", got)
	}
}

func TestReadP9SubfontRejectsBadHeaders(t *testing.T) {
	img := buildP9Image(t, "k1", 1, 8, 4, make([]byte, 32))
	chars := []p9Fontchar{{x: 0, top: 0, bottom: 4, width: 4}, {x: 8}}
	good := buildSubfont(t, img, 1, 4, 3, chars)

	var zero bytes.Buffer
	zero.Write(img)
	zero.Write(p9Field(0))
	zero.Write(p9Field(4))
	zero.Write(p9Field(3))

	for _, c := range []struct {
		data []byte
		what string
	}{
		{good[:len(img)+20], "truncated subfont header"},
		{good[:len(good)-3], "truncated fontchar table"},
		{zero.Bytes(), "n = 0"},
	} {
		if _, err := readP9Subfont(bufio.NewReader(bytes.NewReader(c.data))); err == nil {
			t.Errorf("%s: parsed without error, want a failure", c.what)
		}
	}
}

// n is read from the file and sizes an allocation, so it needs a ceiling. The
// error has to come from the bound rather than from the short read that would
// follow it -- otherwise a corrupt header still gets to ask for gigabytes
// before anything notices, which is exactly what the bound is there to stop.
func TestReadP9SubfontBoundsCharCount(t *testing.T) {
	img := buildP9Image(t, "k1", 1, 8, 4, make([]byte, 32))
	var huge bytes.Buffer
	huge.Write(img)
	huge.Write(p9Field(p9MaxSubfontChars + 1))
	huge.Write(p9Field(4))
	huge.Write(p9Field(3))
	// A full fontchar table for the claimed count, so a short read cannot be
	// what rejects this.
	huge.Write(make([]byte, (p9MaxSubfontChars+2)*6))

	_, err := readP9Subfont(bufio.NewReader(bytes.NewReader(huge.Bytes())))
	if err == nil {
		t.Fatal("an oversized char count parsed without error")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("err = %v, want it rejected by the bound", err)
	}
}

// ---- .font files ------------------------------------------------------------

func TestLoadP9Font(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.font")
	body := "# a comment\n\n15 11\n0x0000 0x00ff ascii.bit\n0x2000 0x20ff 32 punct.bit\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing font: %v", err)
	}
	font, err := loadP9Font(path)
	if err != nil {
		t.Fatalf("loadP9Font: %v", err)
	}
	if font.height != 15 || font.ascent != 11 {
		t.Errorf("height/ascent = %d/%d, want 15/11", font.height, font.ascent)
	}
	if len(font.ranges) != 2 {
		t.Fatalf("ranges = %d, want 2", len(font.ranges))
	}
	// Subfont paths are relative to the .font file's directory.
	if want := filepath.Join(dir, "ascii.bit"); font.ranges[0].path != want {
		t.Errorf("range 0 path = %q, want %q", font.ranges[0].path, want)
	}
	// The optional third field is a glyph offset within the subfont.
	if font.ranges[1].offset != 32 {
		t.Errorf("range 1 offset = %d, want 32", font.ranges[1].offset)
	}
	if font.ranges[1].lo != 0x2000 || font.ranges[1].hi != 0x20ff {
		t.Errorf("range 1 = %04x-%04x, want 2000-20ff", font.ranges[1].lo, font.ranges[1].hi)
	}
}

func TestLoadP9FontRejectsBadFiles(t *testing.T) {
	for _, c := range []struct{ body, what string }{
		{"", "empty file"},
		{"# only comments\n", "no header"},
		{"15\n", "header missing ascent"},
		{"notanumber 11\n", "non-numeric height"},
		{"15 11\n0x00\n", "range line with too few fields"},
		{"15 11\nzz 0xff a.bit\n", "non-numeric range start"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.font")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatalf("writing font: %v", err)
		}
		if _, err := loadP9Font(path); err == nil {
			t.Errorf("%s: loaded without error, want a failure", c.what)
		}
	}
}

func TestLoadP9FontMissingFile(t *testing.T) {
	_, err := loadP9Font(filepath.Join(t.TempDir(), "nope.font"))
	if err == nil {
		t.Fatal("loading a missing font succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "open font") {
		t.Errorf("err = %v, want it to mention opening the font", err)
	}
}
