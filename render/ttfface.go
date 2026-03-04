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

// TTFFace satisfies Face using an opentype rasterizer.
type TTFFace struct {
	face     font.Face
	height   int
	ascent   int
	ren      *sdl.Renderer
	textures map[rune]*sdl.Texture
}

// NewTTFFace parses ttfData as a TrueType/OpenType font and returns a
// TTFFace that renders at pointSize points (DPI=72 so points == pixels).
func NewTTFFace(ttfData []byte, pointSize float64, ren *sdl.Renderer) (*TTFFace, error) {
	parsed, err := opentype.Parse(ttfData)
	if err != nil {
		return nil, fmt.Errorf("render: parse TTF: %w", err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: pointSize,
		DPI:  72, // DPI=72 makes Size in points equal Size in pixels
	})
	if err != nil {
		return nil, fmt.Errorf("render: create face: %w", err)
	}
	m := face.Metrics()
	return &TTFFace{
		face:     face,
		height:   m.Height.Ceil(),
		ascent:   m.Ascent.Ceil(),
		ren:      ren,
		textures: make(map[rune]*sdl.Texture),
	}, nil
}

func (tf *TTFFace) Height() int { return tf.height }
func (tf *TTFFace) Ascent() int { return tf.ascent }

func (tf *TTFFace) GlyphAdvance(r rune) int {
	adv, ok := tf.face.GlyphAdvance(r)
	if !ok {
		return 0
	}
	return adv.Ceil()
}

func (tf *TTFFace) MeasureText(s string) int {
	x := 0
	var prev rune
	for i, r := range s {
		if i > 0 {
			x += tf.face.Kern(prev, r).Ceil()
		}
		adv, ok := tf.face.GlyphAdvance(r)
		if ok {
			x += adv.Ceil()
		}
		prev = r
	}
	return x
}

func (tf *TTFFace) DrawText(s string, x, y int32, col sdl.Color) error {
	cursor := x
	var prev rune
	for i, r := range s {
		if i > 0 {
			cursor += int32(tf.face.Kern(prev, r).Ceil())
		}
		tex, err := tf.glyphTexture(r)
		if err != nil {
			prev = r
			continue // skip unrenderable glyphs silently
		}
		if tex != nil {
			_, _, w, h, err2 := tex.Query()
			if err2 == nil && w > 0 && h > 0 {
				dst := sdl.Rect{X: cursor, Y: y, W: w, H: h}
				tex.SetColorMod(col.R, col.G, col.B)
				tex.SetAlphaMod(col.A)
				tf.ren.Copy(tex, nil, &dst)
			}
		}
		cursor += int32(tf.GlyphAdvance(r))
		prev = r
	}
	return nil
}

func (tf *TTFFace) Close() {
	for _, tex := range tf.textures {
		if tex != nil {
			tex.Destroy()
		}
	}
	tf.textures = make(map[rune]*sdl.Texture)
	tf.face.Close()
}

// glyphTexture returns (and caches) the SDL texture for rune r.
// Returns (nil, nil) for whitespace / zero-advance glyphs.
func (tf *TTFFace) glyphTexture(r rune) (*sdl.Texture, error) {
	if tex, ok := tf.textures[r]; ok {
		return tex, nil
	}
	tex, err := tf.buildGlyphTexture(r)
	tf.textures[r] = tex // cache even if nil
	return tex, err
}

func (tf *TTFFace) buildGlyphTexture(r rune) (*sdl.Texture, error) {
	bounds, adv, ok := tf.face.GlyphBounds(r)
	if !ok || adv == 0 {
		return nil, nil
	}
	w := (bounds.Max.X - bounds.Min.X).Ceil()
	h := (bounds.Max.Y - bounds.Min.Y).Ceil()
	if w <= 0 || h <= 0 {
		return nil, nil
	}

	// Render the glyph into a tight RGBA image.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	dot := fixed.Point26_6{X: -bounds.Min.X, Y: -bounds.Min.Y}
	dr, mask, maskp, _, ok2 := tf.face.Glyph(dot, r)
	if !ok2 {
		return nil, nil
	}
	draw.DrawMask(img, dr, image.White, image.Point{}, mask, maskp, draw.Over)

	// Place the glyph at the correct y inside a full-line-height texture so
	// that DrawText can position by the top of the line box.
	lineH := tf.height
	yOff := tf.ascent + bounds.Min.Y.Ceil()
	if yOff < 0 {
		yOff = 0
	}

	tex, err := tf.ren.CreateTexture(
		uint32(sdl.PIXELFORMAT_RGBA32),
		sdl.TEXTUREACCESS_STATIC,
		int32(w), int32(lineH),
	)
	if err != nil {
		return nil, fmt.Errorf("render: create glyph texture: %w", err)
	}
	tex.SetBlendMode(sdl.BLENDMODE_BLEND)

	pixels := make([]byte, w*lineH*4)
	for py := 0; py < h; py++ {
		destY := py + yOff
		if destY < 0 || destY >= lineH {
			continue
		}
		for px := 0; px < w; px++ {
			src := img.RGBAAt(px, py)
			base := (destY*w + px) * 4
			if base+3 >= len(pixels) {
				continue
			}
			// White ink with per-pixel alpha; coloured at draw time via SetColorMod.
			pixels[base+0] = 255
			pixels[base+1] = 255
			pixels[base+2] = 255
			pixels[base+3] = src.A
		}
	}

	if err := tex.Update(nil, unsafe.Pointer(&pixels[0]), w*4); err != nil {
		tex.Destroy()
		return nil, fmt.Errorf("render: upload glyph texture: %w", err)
	}
	return tex, nil
}
