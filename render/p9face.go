package render

// Plan 9 bitmap font support.
//
// Implements the Face interface using Plan 9 .font files and their subfont
// images. Ported from ~/src/nadir/internal/font/ and
// ~/src/nadir/internal/draw/font.go — do not import from nadir.
//
// File layout:
//   Plan9Face  — implements Face; owns SDL2 texture cache per subfont strip
//   p9Font     — parses .font text file; lazily loads subfont ranges
//   p9Subfont  — holds decoded glyph strip + per-char metrics
//   p9Image    — decodes Plan 9 image format (compressed or raw, k1/k2/k4/k8)

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// ---- Plan9Face --------------------------------------------------------------

// Plan9Face satisfies Face using Plan 9 bitmap fonts.
type Plan9Face struct {
	font     *p9Font
	ren      *sdl.Renderer
	textures map[*p9SubfontImage]*sdl.Texture
	scale    float64 // 1.0 or 1.5
}

// NewPlan9Face loads a Plan 9 .font file and returns a Face ready for drawing.
// If embiggen is true, glyphs are rendered at 150% scale.
func NewPlan9Face(path string, ren *sdl.Renderer, embiggen bool) (*Plan9Face, error) {
	scale := 1.0
	if embiggen {
		scale = 1.5
	}
	f, err := loadP9Font(path)
	if err != nil {
		return nil, err
	}
	return &Plan9Face{
		font:     f,
		ren:      ren,
		textures: make(map[*p9SubfontImage]*sdl.Texture),
		scale:    scale,
	}, nil
}

func (pf *Plan9Face) sc(n int) int32 {
	return int32(math.Round(float64(n) * pf.scale))
}

func (pf *Plan9Face) Height() int { return int(pf.sc(pf.font.height)) }
func (pf *Plan9Face) Ascent() int { return int(pf.sc(pf.font.ascent)) }

func (pf *Plan9Face) GlyphAdvance(r rune) int {
	g, err := pf.font.glyph(r)
	if err != nil {
		return 0
	}
	return int(pf.sc(g.advance))
}

func (pf *Plan9Face) MeasureText(s string) int {
	w := 0
	for _, r := range s {
		w += pf.GlyphAdvance(r)
	}
	return w
}

func (pf *Plan9Face) DrawText(s string, x, y int32, col sdl.Color) error {
	baseline := y + pf.sc(pf.font.ascent)
	cursor := x
	for _, r := range s {
		g, err := pf.font.glyph(r)
		if err != nil {
			continue
		}
		if g.w <= 0 || g.h <= 0 {
			cursor += pf.sc(g.advance)
			continue
		}
		tex, err := pf.texture(g.image)
		if err != nil {
			cursor += pf.sc(g.advance)
			continue
		}
		src := sdl.Rect{X: int32(g.x), Y: int32(g.y), W: int32(g.w), H: int32(g.h)}
		dst := sdl.Rect{
			X: cursor + pf.sc(g.left),
			Y: baseline - pf.sc(g.image.ascent) + pf.sc(g.y),
			W: pf.sc(g.w),
			H: pf.sc(g.h),
		}
		tex.SetColorMod(col.R, col.G, col.B)
		tex.SetAlphaMod(col.A)
		pf.ren.Copy(tex, &src, &dst)
		cursor += pf.sc(g.advance)
	}
	return nil
}

func (pf *Plan9Face) Close() {
	for _, tex := range pf.textures {
		tex.Destroy()
	}
	pf.textures = make(map[*p9SubfontImage]*sdl.Texture)
}

// texture returns (building if necessary) the SDL2 texture for img.
// Ink pixels are stored as white+alpha so SetColorMod tints at draw time.
func (pf *Plan9Face) texture(img *p9SubfontImage) (*sdl.Texture, error) {
	if tex, ok := pf.textures[img]; ok {
		return tex, nil
	}
	// Use linear filtering when scaling so upscaled glyphs are smooth.
	if pf.scale > 1.0 {
		sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, "1")
		defer sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, "0")
	}
	w, h := int32(img.width), int32(img.height)
	tex, err := pf.ren.CreateTexture(uint32(sdl.PIXELFORMAT_RGBA32), sdl.TEXTUREACCESS_STATIC, w, h)
	if err != nil {
		return nil, err
	}
	tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	rgba := make([]byte, int(w)*int(h)*4)
	for i, lum := range img.pix {
		opacity := 255 - lum
		base := i * 4
		rgba[base+0] = 255
		rgba[base+1] = 255
		rgba[base+2] = 255
		rgba[base+3] = opacity
	}
	if err := tex.Update(nil, unsafe.Pointer(&rgba[0]), int(w)*4); err != nil {
		tex.Destroy()
		return nil, err
	}
	pf.textures[img] = tex
	return tex, nil
}

// ---- p9Font -----------------------------------------------------------------

type p9Font struct {
	height int
	ascent int
	dir    string
	ranges []p9Range
	cache  map[rune]p9GlyphInfo // populated on first lookup per rune
}

type p9Range struct {
	lo, hi rune
	offset int
	path   string
	sf     *p9Subfont
}

type p9GlyphInfo struct {
	image   *p9SubfontImage
	x, y    int
	w, h    int
	left    int
	advance int
}

func loadP9Font(path string) (*p9Font, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open font %q: %w", path, err)
	}
	defer f.Close()

	font := &p9Font{dir: filepath.Dir(path), cache: make(map[rune]p9GlyphInfo)}
	scanner := bufio.NewScanner(f)
	headerSeen := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if !headerSeen {
			if len(fields) != 2 {
				return nil, fmt.Errorf("bad font header %q", line)
			}
			h, err := strconv.Atoi(fields[0])
			if err != nil {
				return nil, fmt.Errorf("bad height %q: %w", fields[0], err)
			}
			a, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("bad ascent %q: %w", fields[1], err)
			}
			font.height = h
			font.ascent = a
			headerSeen = true
			continue
		}
		if len(fields) < 3 || len(fields) > 4 {
			return nil, fmt.Errorf("bad range line %q", line)
		}
		lo, err := strconv.ParseInt(fields[0], 0, 32)
		if err != nil {
			return nil, fmt.Errorf("bad lo rune %q: %w", fields[0], err)
		}
		hi, err := strconv.ParseInt(fields[1], 0, 32)
		if err != nil {
			return nil, fmt.Errorf("bad hi rune %q: %w", fields[1], err)
		}
		var offset int64
		pathField := fields[2]
		if len(fields) == 4 {
			offset, err = strconv.ParseInt(fields[2], 0, 32)
			if err != nil {
				return nil, fmt.Errorf("bad offset %q: %w", fields[2], err)
			}
			pathField = fields[3]
		}
		sfPath := filepath.FromSlash(pathField)
		if !filepath.IsAbs(sfPath) {
			sfPath = filepath.Join(font.dir, sfPath)
		}
		font.ranges = append(font.ranges, p9Range{
			lo:     rune(lo),
			hi:     rune(hi),
			offset: int(offset),
			path:   sfPath,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading font: %w", err)
	}
	if !headerSeen {
		return nil, fmt.Errorf("font file empty or missing header")
	}
	return font, nil
}

func (font *p9Font) glyph(r rune) (p9GlyphInfo, error) {
	if g, ok := font.cache[r]; ok {
		return g, nil
	}
	if g, ok := font.lookupRune(r); ok {
		font.cache[r] = g
		return g, nil
	}
	if r != '\uFFFD' {
		if g, ok := font.lookupRune('\uFFFD'); ok {
			font.cache[r] = g
			return g, nil
		}
	}
	return p9GlyphInfo{}, fmt.Errorf("no glyph for U+%04X", r)
}

func (font *p9Font) lookupRune(r rune) (p9GlyphInfo, bool) {
	for i := range font.ranges {
		fr := &font.ranges[i]
		if r < fr.lo || r > fr.hi {
			continue
		}
		if err := font.ensureLoaded(fr); err != nil {
			continue
		}
		idx := int(r-fr.lo) + fr.offset
		if idx < 0 || idx >= fr.sf.n {
			continue
		}
		return makeP9GlyphInfo(fr.sf, idx), true
	}
	return p9GlyphInfo{}, false
}

func makeP9GlyphInfo(sf *p9Subfont, idx int) p9GlyphInfo {
	fc := &sf.chars[idx]
	next := &sf.chars[idx+1]
	return p9GlyphInfo{
		image:   sf.subImage(),
		x:       int(fc.x),
		y:       int(fc.top),
		w:       int(next.x) - int(fc.x),
		h:       int(fc.bottom) - int(fc.top),
		left:    int(fc.left),
		advance: int(fc.width),
	}
}

func (font *p9Font) ensureLoaded(fr *p9Range) error {
	if fr.sf != nil {
		return nil
	}
	sf, err := loadP9Subfont(fr.path)
	if err != nil {
		return err
	}
	fr.sf = sf
	return nil
}

// ---- p9Subfont --------------------------------------------------------------

type p9SubfontImage struct {
	pix    []byte
	width  int
	height int
	ascent int
}

type p9Subfont struct {
	n      int
	height int
	ascent int
	chars  []p9Fontchar
	img    *p9RawImage
	image  *p9SubfontImage // lazily built exported view
}

type p9Fontchar struct {
	x      uint16
	top    uint8
	bottom uint8
	left   int8
	width  uint8
}

func (sf *p9Subfont) subImage() *p9SubfontImage {
	if sf.image == nil {
		sf.image = &p9SubfontImage{
			pix:    sf.img.pix,
			width:  sf.img.rect.Dx(),
			height: sf.img.rect.Dy(),
			ascent: sf.ascent,
		}
	}
	return sf.image
}

func loadP9Subfont(path string) (*p9Subfont, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open subfont %q: %w", path, err)
	}
	defer f.Close()
	sf, err := readP9Subfont(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("subfont %q: %w", path, err)
	}
	return sf, nil
}

func readP9Subfont(br *bufio.Reader) (*p9Subfont, error) {
	img, err := readP9Image(br)
	if err != nil {
		return nil, fmt.Errorf("reading glyph image: %w", err)
	}

	var hdr [36]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, fmt.Errorf("reading subfont header: %w", err)
	}
	parseField := func(b []byte) (int, error) {
		v, err := strconv.Atoi(strings.TrimSpace(string(b)))
		return v, err
	}
	n, err := parseField(hdr[0:12])
	if err != nil {
		return nil, fmt.Errorf("subfont n: %w", err)
	}
	height, err := parseField(hdr[12:24])
	if err != nil {
		return nil, fmt.Errorf("subfont height: %w", err)
	}
	ascent, err := parseField(hdr[24:36])
	if err != nil {
		return nil, fmt.Errorf("subfont ascent: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("subfont n=%d", n)
	}

	raw := make([]byte, (n+1)*6)
	if _, err := io.ReadFull(br, raw); err != nil {
		return nil, fmt.Errorf("reading fontchar table: %w", err)
	}
	chars := make([]p9Fontchar, n+1)
	for i := range chars {
		p := raw[i*6:]
		chars[i] = p9Fontchar{
			x:      binary.LittleEndian.Uint16(p[0:2]),
			top:    p[2],
			bottom: p[3],
			left:   int8(p[4]),
			width:  p[5],
		}
	}
	return &p9Subfont{n: n, height: height, ascent: ascent, chars: chars, img: img}, nil
}

// ---- p9RawImage -------------------------------------------------------------

type p9RawImage struct {
	rect  image.Rectangle
	pix   []byte
	depth int
}

const (
	p9NMEM   = 1024
	p9NMATCH = 3
)

func readP9Image(br *bufio.Reader) (*p9RawImage, error) {
	var magic [11]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return nil, fmt.Errorf("reading image magic: %w", err)
	}
	compressed := string(magic[:]) == "compressed\n"

	var hdr [60]byte
	if compressed {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return nil, fmt.Errorf("reading image header: %w", err)
		}
	} else {
		copy(hdr[:11], magic[:])
		if _, err := io.ReadFull(br, hdr[11:]); err != nil {
			return nil, fmt.Errorf("reading image header: %w", err)
		}
	}

	tf := func(b []byte) string { return strings.TrimSpace(string(b)) }
	depth, err := parseP9Chan(tf(hdr[0:12]))
	if err != nil {
		return nil, err
	}
	minX, _ := strconv.Atoi(tf(hdr[12:24]))
	minY, _ := strconv.Atoi(tf(hdr[24:36]))
	maxX, _ := strconv.Atoi(tf(hdr[36:48]))
	maxY, _ := strconv.Atoi(tf(hdr[48:60]))

	rect := image.Rect(minX, minY, maxX, maxY)
	w, h := rect.Dx(), rect.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid image rect %v", rect)
	}

	stride := (w*depth + 7) / 8
	raw := make([]byte, stride*h)
	if compressed {
		if err := decompressP9(br, raw, minY, maxY, stride); err != nil {
			return nil, err
		}
	} else {
		if _, err := io.ReadFull(br, raw); err != nil {
			return nil, fmt.Errorf("reading pixels: %w", err)
		}
	}

	pix := make([]byte, w*h)
	for y := 0; y < h; y++ {
		unpackP9Row(pix[y*w:], raw[y*stride:], w, depth)
	}
	return &p9RawImage{rect: rect, pix: pix, depth: depth}, nil
}

func parseP9Chan(s string) (int, error) {
	if len(s) < 2 || s[0] != 'k' {
		return 0, fmt.Errorf("unsupported image format %q (want k1/k2/k4/k8)", s)
	}
	d, err := strconv.Atoi(s[1:])
	if err != nil {
		return 0, fmt.Errorf("bad depth in %q: %w", s, err)
	}
	switch d {
	case 1, 2, 4, 8:
		return d, nil
	default:
		return 0, fmt.Errorf("unsupported depth %d in %q", d, s)
	}
}

// unpackP9Row expands one packed row to one byte per pixel.
// Plan 9 convention: 0 = ink (opaque), 255 = paper (transparent).
func unpackP9Row(dst, src []byte, w, depth int) {
	switch depth {
	case 1:
		for x := 0; x < w; x++ {
			if (src[x/8]>>uint(7-x%8))&1 != 0 {
				dst[x] = 0
			} else {
				dst[x] = 255
			}
		}
	case 2:
		for x := 0; x < w; x++ {
			v := (src[x/4] >> uint(6-2*(x%4))) & 3
			dst[x] = (3 - v) * 85
		}
	case 4:
		for x := 0; x < w; x++ {
			b := src[x/2]
			var v byte
			if x%2 == 0 {
				v = (b >> 4) & 0xf
			} else {
				v = b & 0xf
			}
			dst[x] = (15 - v) * 17
		}
	case 8:
		for x := 0; x < w; x++ {
			dst[x] = 255 - src[x]
		}
	}
}

func decompressP9(r io.Reader, raw []byte, minY, maxY, stride int) error {
	miny := minY
	for miny != maxY {
		var chunkHdr [24]byte
		if _, err := io.ReadFull(r, chunkHdr[:]); err != nil {
			return fmt.Errorf("reading chunk header: %w", err)
		}
		tf := func(b []byte) string { return strings.TrimSpace(string(b)) }
		chunkMaxY, err := strconv.Atoi(tf(chunkHdr[0:12]))
		if err != nil {
			return fmt.Errorf("parsing chunk maxy: %w", err)
		}
		nb, err := strconv.Atoi(tf(chunkHdr[12:24]))
		if err != nil {
			return fmt.Errorf("parsing chunk nb: %w", err)
		}
		if chunkMaxY <= miny || chunkMaxY > maxY || nb <= 0 {
			return fmt.Errorf("invalid chunk maxy=%d nb=%d", chunkMaxY, nb)
		}
		cbuf := make([]byte, nb)
		if _, err := io.ReadFull(r, cbuf); err != nil {
			return fmt.Errorf("reading chunk data: %w", err)
		}
		outStart := (miny - minY) * stride
		outEnd := (chunkMaxY - minY) * stride
		if err := decompressP9Chunk(cbuf, raw[outStart:outEnd]); err != nil {
			return fmt.Errorf("decompressing chunk rows %d-%d: %w", miny, chunkMaxY, err)
		}
		miny = chunkMaxY
	}
	return nil
}

func decompressP9Chunk(src, dst []byte) error {
	mem := make([]byte, p9NMEM)
	memp := 0
	si, di := 0, 0
	for di < len(dst) {
		if si >= len(src) {
			return fmt.Errorf("source exhausted at output %d/%d", di, len(dst))
		}
		c := int(src[si])
		si++
		if c >= 128 {
			n := c - 128 + 1
			for i := 0; i < n; i++ {
				if si >= len(src) || di >= len(dst) {
					return fmt.Errorf("literal run overflow at di=%d", di)
				}
				b := src[si]
				si++
				dst[di] = b
				di++
				mem[memp] = b
				memp++
				if memp == p9NMEM {
					memp = 0
				}
			}
		} else {
			if si >= len(src) {
				return fmt.Errorf("back-ref missing offset at di=%d", di)
			}
			u := int(src[si])
			si++
			offs := u + ((c & 3) << 8) + 1
			cnt := (c >> 2) + p9NMATCH
			omemp := memp - offs
			if omemp < 0 {
				omemp += p9NMEM
			}
			for i := 0; i < cnt; i++ {
				if di >= len(dst) {
					return fmt.Errorf("back-ref crosses boundary at di=%d", di)
				}
				b := mem[omemp]
				dst[di] = b
				di++
				mem[memp] = b
				memp++
				if memp == p9NMEM {
					memp = 0
				}
				omemp++
				if omemp == p9NMEM {
					omemp = 0
				}
			}
		}
	}
	return nil
}
