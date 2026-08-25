# TODO

Open work. Items resolved in the 2026-08-22 review and the performance
pass that followed have been removed — see git log for what was done and
why.

The text engine is where the work has been so far and is in good shape:
correct against sam's semantics, covered by a characterisation corpus,
and no longer quadratic anywhere that matters. **The remaining work is
rendering and layout**, which is where every item below lives except two.

## Rendering and layout

### Rebuilding the buffer's line table dominates every keystroke

Now the largest per-keystroke cost, and by a wide margin: profiling one
keystroke on a 100k-word file puts **92% of it in
`buffer.rebuildLines`** and about 5% in the layout work it was called to
support.

- **Where:** `buffer/lines.go` — `rebuildLines`, via `lineStarts`
- **Smell:** the line-start table is rebuilt from scratch whenever `Gen`
  moves, so it rescans the whole file on every edit. Incremental layout
  made this visible by removing everything that used to be larger.
- **Fix:** extend the table lazily rather than building it whole. Keep a
  `scannedTo` offset and push the scan forward only as far as a query
  needs — `2p` scans two lines, `$` scans everything — and on an edit
  discard only the entries at or after it rather than all of them. This
  does less work rather than the same work faster, which is the right
  shape.
- **Not urgent:** 0.94ms per keystroke at 100k words is well inside a
  16ms frame. This is the next lever if editing latency ever matters
  again, not a problem today.

### `drawBody` re-reads the same line several times per frame

- **Where:** `pane/pane.go:340` — `s := buf.ReadAt(drawHi-1, 1)` then
  `[]rune(s)`, two allocations to look at one rune
- **Smell:** per visible line, per frame. `buf.RuneAt(pos)` already
  exists and does this without allocating. Worse, `drawLineText` reads
  the line and then `drawLineSelection` re-reads slices of it through
  `XForCP` → `measureRange` → `buf.ReadAt` → `[]rune`.
- **Worse than it looks:** `drawLineText` then goes
  `ReadAt` → `[]rune` → `expandTabsRunes` → `string`, four allocations
  per visible line per frame, and `DrawText` decodes that string back
  into runes. A selected line draws the whole line and then the selected
  span over it (`pane/pane.go:470`, `:480`), so selected glyphs are
  drawn twice.
- **Fix:** use `RuneAt` for the peek; read each visible line's runes
  once per draw and pass the slice down to the measure and selection
  paths. Having `Face.DrawText` take `[]rune` would delete the
  rune→string→rune round trip at the same time, and is a smaller change
  than glyph batching.

### Glyphs are drawn one `Copy` call at a time

- **Where:** `render/ttfface.go:119` and `render/p9face.go:105` — both
  faces do `SetColorMod` / `Copy` per glyph
- **Smell:** every glyph is its own texture bind, state change and draw
  call.
- **Fix:** batch runs of same-coloured text into a single
  `RenderGeometry` call. Only worth the complexity if profiling shows
  draw-call overhead actually dominates — do the `drawBody` item above
  first and re-measure.

### Kern lookup is a map hit per character pair

- **Where:** `render/ttfface.go:82` — `kerns map[[2]rune]int32`. The
  Plan 9 face has the same shape at `p9Font.cache`, a `map[rune]` hit
  per rune in both `MeasureText` and `DrawText`.
- **Smell:** a map lookup per rune pair in both `MeasureText` and
  `DrawText`. Fine for static text; noticeable per frame on long lines.
- **Fix:** ASCII fast path — a `[128][128]int32` array for the common
  case, falling through to the map for everything else.

### HiDPI scaling applies to TTF faces and not to Plan 9 ones

- **Where:** `render/window.go:186` — `loadFace` takes the display
  `scale` and uses it only in the TTF branch (`cfg.FontSize*scale`).
- **Smell:** on a HiDPI display a TTF face compensates automatically and
  a Plan 9 face does not, so bitmap fonts render at half size unless
  `plan9_scale` is set by hand.
- **Possibly deliberate:** fractional scaling of a bitmap font looks bad
  either way, which is what the `edgeGamma` fudge in `p9face.go` exists
  to paper over, and a manual knob at least lets the user pick a value
  that looks right. Worth deciding on purpose rather than by omission.

### ~~The layout cache does not know the font changed~~ — done

`Pane.Relayout` keys its cache on `(paneW, bufGen)`, which does not
include the face, and `InvalidateLayout` — written for exactly this —
had no production callers. The Plan 9 "scale font" menu item mutated
face metrics in place, so glyphs resized against a layout computed for
the old size. That menu item is gone: `plan9_scale` is config-only now,
applied when the face is built, and `Config` reloads it. `applyConfig`
now invalidates every pane explicitly rather than relying on `SetFace`
doing it as a side effect, which also covers a reload that changes
`tab_width` or `margin` while the font fails to load.

### ~~`render/` has no tests~~ — done

`render/p9face_test.go` covers the Plan 9 binary path: image decoding at
k1/k2/k4/k8, compressed against uncompressed, the back-reference
decompressor including overlapping runs, subfont and fontchar parsing
with signed left bearings, `.font` file parsing, and the malformed-input
cases for each. Mutation-verified — polarity flips, an off-by-one in the
back-reference offset or count, a fontchar width read from the wrong
entry, and byte-order swaps each fail a test. The subfont char count is
now bounded (`p9MaxSubfontChars`) so a corrupt header cannot size an
allocation.

The two `Face` implementations are still untested above the parsing
layer, since that needs a renderer.

## Elsewhere

### ~~`file.Save` allocates the file three times over~~ — done

The write path streams. `buffer.WriteTo`/`WriteRangeTo` (`buffer/write.go`)
write the gap buffer's two segments straight to an `io.Writer` without
flattening it, and CRLF encoding is a writer wrapper (`file/crlf.go`) rather
than a `strings.ReplaceAll`. Writing an 880 KB file went from 884 KB and one
allocation to zero (LF) or 16 bytes (CRLF, the interface box), and from
143 µs to 8 ns. `file/alloc_test.go` guards it.

Encoding also moved from being each write path's duty to a property of the
writer, which is what fixed the CRLF bug in `WriteTo` — three write paths had
to remember to encode and one didn't.

### `buffer.String()` still copies the whole file at 5 call sites

Down from ~15; the address and command paths no longer use it per match, and
the write path no longer uses it at all.

- `pane/wrap.go:55` is now only reached on a full wrap — a resize, a font
  change, word wrap toggled, or an edit older than the buffer's edit log —
  rather than on every keystroke.
- `addr/addr.go:159` (`evalRegexp`), `cmd/exec_struct.go:43` and `:91`
  (`execX`, `execY`) — all three via `file.String()` — and `main.go:472`
  (`lookForward`).
- **Note:** the regexp ones are the awkward set, because Go's `regexp`
  wants a string — removing the copy there means either an
  allocation-free iterator the matcher can drive, or accepting the copy.
  Not urgent now that the per-match quadratics are gone: a search costs
  one copy per command rather than one per match.

## Known limitations

### A file with no newlines re-wraps entirely on every keystroke

Incremental layout re-wraps whole logical lines, so a file with no
newlines in it — minified JSON, say — is a single logical line and gets
no benefit. This is no worse than the old behaviour, and prose files
always have paragraph breaks, so it was left alone deliberately.

The fix, if it is ever wanted, is resumable mid-line wrapping:
checkpoint the wrap state (`curW`, `wordStart`, `inWord`) per visual
line and restart from the last break the edit cannot have moved.
Word-wrap lookback makes that materially harder than the per-line case,
since a change can move a break point backwards.

## Known divergences from sam

None outstanding. The two found during the address refactor — regexp
search direction after `+`/`-`, and a leading `+`/`-` not taking an
implicit dot — are fixed.
