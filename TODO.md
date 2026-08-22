# TODO

Open work. Items resolved in the 2026-08-22 review and the performance
pass that followed have been removed — see git log for what was done and
why.

The text engine is where the work has been so far and is in good shape:
correct against sam's semantics, covered by a characterisation corpus,
and no longer quadratic anywhere that matters. **The remaining work is
rendering and layout**, which is where every item below lives except two.

## Rendering and layout

### Every relayout is a full re-wrap, and copies the file twice to do it

The biggest editing-latency problem left, and the one thing standing
between "fast small-file editor" and "editable at any size".

- **Where:** `pane/wrap.go:56` — `runes := []rune(buf.String())`
- **Smell:** `buf.String()` allocates the whole file as a string and
  `[]rune(...)` allocates it again as a rune slice, on *every* buffer
  change. A 10MB file costs 40MB of allocation per keystroke before any
  wrapping happens. Then the entire file is re-wrapped in order to
  redraw a screenful.
- **Fix — incremental layout.** Re-wrap only the affected line range and
  shift `Y` for the lines below; Plan 9's layout engine does this. A
  cheaper first cut: track the buffer generation and edit region the
  layout was correct for, and when the next edit falls inside the same
  visible viewport, re-wrap only from the previous line start through
  end-of-visible. The proper version segments the layout into
  paragraph-sized chunks that can be invalidated independently.
- **Note:** an allocation-free `buffer.RuneIterator` would remove the
  copying, but it leaves the O(file) re-wrap in place. Incremental
  layout fixes both, and simple is better here.

### `drawBody` re-reads the same line several times per frame

- **Where:** `pane/pane.go:340` — `s := buf.ReadAt(drawHi-1, 1)` then
  `[]rune(s)`, two allocations to look at one rune
- **Smell:** per visible line, per frame. `buf.RuneAt(pos)` already
  exists and does this without allocating. Worse, `drawLineText` reads
  the line and then `drawLineSelection` re-reads slices of it through
  `XForCP` → `measureRange` → `buf.ReadAt` → `[]rune`.
- **Fix:** use `RuneAt` for the peek; read each visible line's runes
  once per draw and pass the slice down to the measure and selection
  paths.

### Glyphs are drawn one `Copy` call at a time

- **Where:** `render/ttfface.go:119` — `SetColorMod` / `Copy` per glyph
- **Smell:** every glyph is its own texture bind, state change and draw
  call.
- **Fix:** batch runs of same-coloured text into a single
  `RenderGeometry` call. Only worth the complexity if profiling shows
  draw-call overhead actually dominates — do the two items above first
  and re-measure.

### Kern lookup is a map hit per character pair

- **Where:** `render/ttfface.go:82` — `kerns map[[2]rune]int32`
- **Smell:** a map lookup per rune pair in both `MeasureText` and
  `DrawText`. Fine for static text; noticeable per frame on long lines.
- **Fix:** ASCII fast path — a `[128][128]int32` array for the common
  case, falling through to the map for everything else.

## Elsewhere

### `file.Save` allocates the file three times over

- **Where:** `file/file.go:166`, and `EncodeContent` at `file.go:59`
- **Smell:** reads the whole buffer to a string, `strings.ReplaceAll`
  for CRLF, then writes — three allocations the size of the file. Fine
  small, painful past a few MB. `WriteTo` (`file.go:150`) has the same
  shape.
- **Fix:** stream through an `io.Writer` that CRLF-encodes on the fly.

### `buffer.String()` still copies the whole file at 7 call sites

Down from ~15; the address and command paths no longer use it per match.

- `pane/wrap.go:56` and `file/file.go:150`/`:166` are covered above.
- The rest: `addr/addr.go:159` (`evalRegexp`), `cmd/exec_struct.go:43`
  and `:91` (`execX`, `execY`), `main.go:472` (`lookForward`).
- **Note:** the regexp ones are the awkward set, because Go's `regexp`
  wants a string — removing the copy there means either an
  allocation-free iterator the matcher can drive, or accepting the copy.
  Not urgent now that the per-match quadratics are gone: a search costs
  one copy per command rather than one per match.

## Known divergences from sam

None outstanding. The two found during the address refactor — regexp
search direction after `+`/`-`, and a leading `+`/`-` not taking an
implicit dot — are fixed.
