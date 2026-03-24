package render

// TTFFace implements Face using golang.org/x/image/font/opentype for
// rasterisation. Each distinct rune gets its own cached SDL texture on
// first use. Adapted from ~/src/nadir/internal/draw/ttfface.go.

import (
	"fmt"
	"image"
	"image/draw"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// glyphEntry caches the SDL texture and its dimensions for a single rune.
type glyphEntry struct {
	tex  *sdl.Texture
	w, h int32
}

// TTFFace satisfies Face using an opentype rasterizer.
type TTFFace struct {
	face     font.Face
	height   int
	ascent   int
	ren      *sdl.Renderer
	glyphs   map[rune]glyphEntry  // cached textures + dimensions
	advances map[rune]int         // cached advance widths
	kerns    map[[2]rune]int32    // cached kern pairs
}

// NewTTFFace parses ttfData as a TrueType/OpenType font and returns a
// TTFFace that renders at pointSize points at the given dpi. Pass the display
// DPI from sdl.GetDisplayDPI so that point sizes match other applications on
// the same system (Linux desktops use 96 DPI; macOS uses 72).
func NewTTFFace(ttfData []byte, pointSize, dpi float64, ren *sdl.Renderer) (*TTFFace, error) {
	parsed, err := opentype.Parse(ttfData)
	if err != nil {
		return nil, fmt.Errorf("render: parse TTF: %w", err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    pointSize,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("render: create face: %w", err)
	}
	m := face.Metrics()
	return &TTFFace{
		face:     face,
		height:   m.Height.Ceil() + 2, // +2px breathing room below descenders
		ascent:   m.Ascent.Ceil(),
		ren:      ren,
		glyphs:   make(map[rune]glyphEntry),
		advances: make(map[rune]int),
		kerns:    make(map[[2]rune]int32),
	}, nil
}

func (tf *TTFFace) Height() int { return tf.height }
func (tf *TTFFace) Ascent() int { return tf.ascent }

func (tf *TTFFace) GlyphAdvance(r rune) int {
	if v, ok := tf.advances[r]; ok {
		return v
	}
	adv, ok := tf.face.GlyphAdvance(r)
	v := 0
	if ok {
		v = adv.Round()
	}
	tf.advances[r] = v
	return v
}

func (tf *TTFFace) kern(a, b rune) int32 {
	k := [2]rune{a, b}
	if v, ok := tf.kerns[k]; ok {
		return v
	}
	v := int32(tf.face.Kern(a, b).Ceil())
	tf.kerns[k] = v
	return v
}

func (tf *TTFFace) MeasureText(s string) int {
	x := 0
	var prev rune
	for i, r := range s {
		if i > 0 {
			x += int(tf.kern(prev, r))
		}
		x += tf.GlyphAdvance(r)
		prev = r
	}
	return x
}

func (tf *TTFFace) DrawText(s string, x, y int32, col sdl.Color) error {
	cursor := x
	var prev rune
	for i, r := range s {
		if i > 0 {
			cursor += tf.kern(prev, r)
		}
		g, err := tf.glyphTexture(r)
		if err != nil {
			prev = r
			continue // skip unrenderable glyphs silently
		}
		if g.tex != nil && g.w > 0 && g.h > 0 {
			dst := sdl.Rect{X: cursor, Y: y, W: g.w, H: g.h}
			g.tex.SetColorMod(col.R, col.G, col.B)
			g.tex.SetAlphaMod(col.A)
			tf.ren.Copy(g.tex, nil, &dst)
		}
		cursor += int32(tf.GlyphAdvance(r))
		prev = r
	}
	return nil
}

func (tf *TTFFace) Close() {
	for _, g := range tf.glyphs {
		if g.tex != nil {
			g.tex.Destroy()
		}
	}
	tf.glyphs = make(map[rune]glyphEntry)
	tf.advances = make(map[rune]int)
	tf.kerns = make(map[[2]rune]int32)
	tf.face.Close()
}

// glyphTexture returns (and caches) the glyphEntry for rune r.
// Returns a zero entry for whitespace / zero-advance glyphs.
func (tf *TTFFace) glyphTexture(r rune) (glyphEntry, error) {
	if g, ok := tf.glyphs[r]; ok {
		return g, nil
	}
	g, err := tf.buildGlyphTexture(r)
	tf.glyphs[r] = g // cache even if zero
	return g, err
}

func (tf *TTFFace) buildGlyphTexture(r rune) (glyphEntry, error) {
	bounds, adv, ok := tf.face.GlyphBounds(r)
	if !ok || adv == 0 {
		return glyphEntry{}, nil
	}
	w := (bounds.Max.X - bounds.Min.X).Ceil()
	if w <= 0 {
		return glyphEntry{}, nil
	}

	// Render the glyph into a full-line-height image.
	// dot.Y is placed at exactly tf.ascent pixels from the top (integer, no
	// sub-pixel rounding) so every glyph lands at the same baseline, which
	// eliminates the "crooked teeth" vertical jitter.
	lineH := tf.height
	img := image.NewRGBA(image.Rect(0, 0, w, lineH))
	dot := fixed.Point26_6{X: -bounds.Min.X, Y: fixed.Int26_6(tf.ascent * 64)}
	dr, mask, maskp, _, ok2 := tf.face.Glyph(dot, r)
	if !ok2 {
		return glyphEntry{}, nil
	}
	draw.DrawMask(img, dr, image.White, image.Point{}, mask, maskp, draw.Over)

	tex, err := tf.ren.CreateTexture(
		uint32(sdl.PIXELFORMAT_RGBA32),
		sdl.TEXTUREACCESS_STATIC,
		int32(w), int32(lineH),
	)
	if err != nil {
		return glyphEntry{}, fmt.Errorf("render: create glyph texture: %w", err)
	}
	tex.SetBlendMode(sdl.BLENDMODE_BLEND)

	pixels := make([]byte, w*lineH*4)
	for py := 0; py < lineH; py++ {
		for px := 0; px < w; px++ {
			src := img.RGBAAt(px, py)
			base := (py*w + px) * 4
			// White ink with per-pixel alpha; coloured at draw time via SetColorMod.
			pixels[base+0] = 255
			pixels[base+1] = 255
			pixels[base+2] = 255
			pixels[base+3] = src.A
		}
	}

	if err := tex.Update(nil, unsafe.Pointer(&pixels[0]), w*4); err != nil {
		tex.Destroy()
		return glyphEntry{}, fmt.Errorf("render: upload glyph texture: %w", err)
	}
	return glyphEntry{tex: tex, w: int32(w), h: int32(lineH)}, nil
}
