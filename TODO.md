# TODO

Known issues found during the 2026-08-22 review that aren't yet fixed.
Not urgent, but recorded so they don't get lost. Each item includes
the smell, where it lives, and a sketch of the fix.

## Correctness

*(All items in this section were fixed on 2026-08-22 — see git log.)*

## Durability

*(Fixed on 2026-08-22 — `WriteAtomic` now fsyncs the file before rename and best-effort fsyncs the parent directory after.)*

## Test-suite gaps

*(All items covered on 2026-08-22 in `cmd/gaps_test.go`. Writing the `#n` non-ASCII test surfaced that `#` was being swallowed as a comment before it could be parsed as a char-offset address. Rather than patch the recognition rule, comment syntax was removed entirely — Sam has no comments, sdave scripts live inside a shell where `#` already works, and multi-line `-f` scripts are rare enough to wrap in a heredoc.)*

## Performance

Ranked by realistic impact per keystroke or per frame on a 100KB+ file.
Plan 9's sam is fast because its buffer is designed so common operations
don't traverse the whole file, and rendering only touches what's
on-screen. Dave has room to close both gaps significantly.

### Tier 1 — the ones that turn small files into big ones

#### ~~`buffer.runeToBytePos` scans from position 0 every call~~ — DONE 2026-08-22
#### ~~`buffer.Len()` scans the whole buffer~~ — DONE 2026-08-22

Both fixed by one change: `Buffer` now carries `preGapRunes` (runes in
`data[:gapStart]`) and `totalRunes`, maintained incrementally in
`moveGap`, `insert`, and `delete`. `Len()` is a field read.
`runeToBytePos` now knows three fixed points — buffer start, the gap,
and the end — and walks from whichever bounds the target most closely,
using `byteOffsetFromEnd` (a `DecodeLastRune` walk) for the backward
direction. Editing at the gap, which is where editing normally happens,
is O(1). `ReadAt` was rewritten on top of it to slice the two segments
directly instead of re-counting runes, and `RuneAt` no longer allocates.

Measured on a 1MB buffer (`go test ./buffer/ -bench .`, before → after):

| benchmark | before | after |
|---|---|---|
| `InsertAtEndOfLargeFile` | 5,074,116 ns | 20.5 ns |
| `InsertAtMiddleOfLargeFile` | 2,026,684 ns | 6,560 ns |
| `InsertScattered` (worst case, gap moves every time) | 3,305,602 ns | 1,257,315 ns |
| `ReadAtNearEnd` | 7,147,918 ns | 518 ns |
| `Len` | 1,400,556 ns | 2.1 ns |

`InsertScattered` is the case the cache can't help with — it's bounded
by the `memmove` in `moveGap`, and still improves 2.6× because the
offset lookup no longer scans. Invariant and randomized model tests are
in `buffer/gap_test.go`; the cached counts are recomputed from `data`
and compared after every operation.

#### `pane.Wrap` does `[]rune(buf.String())` on every relayout
- **Where:** `pane/wrap.go:56`
- **Smell:** `buf.String()` allocates the entire file as a Go string;
  `[]rune(...)` allocates again as a rune slice. On every buffer change
  (every keystroke) the entire file is copied twice. For a 10MB file
  that's 40MB of allocation per keystroke plus the actual wrap work.
- **Fix (either wins big):**
  1. Expose `buffer.RuneIterator` that iterates through both segments
     of the gap without allocating — then `Wrap` and search paths can
     walk directly.
  2. Do incremental layout: only re-wrap the affected line range and
     shift Y coordinates for lines below. Plan 9's layout engine does
     this.
     
     THIS IS THE RECOMMENDED OPTION; DO INCREMENTAL LAYOUT. SIMPLE IS ALWAYS BETTER IN THIS CODEBASE.

### Tier 2 — per-frame or per-command O(n)

#### ~~`pane.drawBody` linearly scans all layout lines to find visible ones~~ — DONE 2026-08-22

Added `Layout.LineAtY(y)` in `pane/wrap.go`, mirroring the existing
`LineAt`: `Wrap` emits `Y = index * lineH`, so `Lines` is strictly
increasing in `Y` and a binary search is valid. It returns the index of
the line box containing `y`, clamped at both ends.

Three linear scans now use it: `drawBody` starts its loop at
`Lines[LineAtY(scrollY):]` instead of iterating from line 0 every
frame, `CPAtXY` replaces a backward scan from the end of the file, and
`input.moveDotToTopOfView` replaces a forward scan. Behavior is
unchanged at every edge — including the click-in-the-top-pad case,
which still resolves to offset 0.

On a 100K-line layout the lookup goes 117,513 ns → 102.8 ns (~1140×);
at 60 fps that scan alone was ~0.7% of a frame budget per pane, growing
linearly with file size. `pane/layout_test.go` (the package's first
tests) checks `LineAtY` against the naive scan it replaced for every
pixel across a range of layout sizes, asserts the result always lands
inside its own line box, and asserts the property `drawBody` actually
depends on: starting at `LineAtY(scrollY)` never skips a line that is
still on screen.

#### ~~`addr.lineRange` re-scans the whole file for every line address~~ — DONE 2026-08-22

`buffer/lines.go` holds a line-start table — the codepoint offset of
every line start — built lazily on the first line query after the
generation changes. Three string-scanning paths in `addr` now go
through it: `lineRange`, and the line-number derivations in `evalPlus`
and `evalMinus`. `addr` no longer calls `f.String()` for line
addresses at all.

Measured on a 100K-line (4.3MB) file:

| | before | after |
|---|---|---|
| line address, table already built | 8,614 ns | 15.4 ns |
| line address, first after an edit | 573,000 ns | 4,196,000 ns |

The warm case is the point: a ranged command, or a loop body running
per match, resolves many line addresses against one generation and now
scans the file once instead of once per address (and copies it zero
times instead of once per address).

The cold case is a real 7.3× regression — the old scan stopped at the
line it wanted, the table always scans to the end. It is 4ms once per
command on a 4.3MB file, i.e. not perceptible, so it is recorded rather
than fixed.

**Follow-up if it ever matters:** extend the table lazily instead of
building it whole. Keep a `scannedTo` offset and push the scan forward
only as far as the query needs — `2p` scans two lines, `$` scans
everything. That removes the cold regression by doing less work rather
than by doing the same work faster. An attempt to close the gap the
other way (a hand-rolled SWAR scan fusing newline search and rune
counting) got the rebuild from 4.25ms to 2.27ms and was reverted: not
worth the bit-twiddling for a millisecond on a once-per-command path.

#### `execX` and `substAll` do quadratic rune counting per match
- **Where:** `cmd/exec_struct.go:78-79`, `cmd/exec_subst.go:75-76`
- **Smell:** Call `utf8.RuneCountInString(dotText[:m[0]])` on every
  match. Each call walks from position 0. For M matches in N-length
  text that's O(M·N).
- **Fix:** Single pass — matches are already sorted by position; walk
  `dotText` once, incrementally converting byte offsets to rune offsets
  as you go.

### Tier 3 — measurable but smaller

#### `pane.drawBody` peeks at the last rune via `buf.ReadAt(drawHi-1, 1)` then `[]rune(s)`
- **Where:** `pane/pane.go:342`
- **Smell:** Per visible line, per frame. Two allocations to look at
  one rune.
- **Fix:** Use `buf.RuneAt(pos)` (already exists). Also
  `pane.drawLineText` reads the line, then `drawLineSelection` re-reads
  slices via `XForCP` → `measureRange` → `buf.ReadAt` → `[]rune`. Cache
  the visible-line rune slice per draw and pass it down.

#### `file.Save` reads whole buffer, then `strings.ReplaceAll` for CRLF, then writes
- **Where:** `file/file.go:176`
- **Smell:** Three allocations, each the size of the file. Fine for
  small files, painful past a few MB.
- **Fix:** Stream through an `io.Writer` that CRLF-encodes on the fly.

#### Glyph textures rendered one `Copy` call at a time
- **Where:** `render/ttfface.go:121`
- **Smell:** Each `SetColorMod`/`SetAlphaMod`/`Copy` is a small state
  change and draw call.
- **Fix:** Batch runs of same-color text with a single `RenderGeometry`
  call. Only worth the complexity if profiling actually shows draw-call
  overhead — try the cheaper items first.

#### Kern map lookup per character
- **Where:** `render/ttfface.go` `MeasureText` / `DrawText`
- **Smell:** `map[[2]rune]int32` lookup per rune pair. Fine for
  slow-changing text; per frame it's noticeable on long lines.
- **Fix:** ASCII-pair fast path — a `[128][128]int32` array for the
  common case; fall through to the map for non-ASCII.

### Structural / cross-cutting

#### Every relayout is a full re-wrap even for a one-character insert
- **Smell:** For a large file with word-wrap on, this is the single
  biggest editing-latency contributor.
- **Fix (heuristic):** Track "layout was correct as of this buffer
  generation and edit region." If the next edit is inside the same
  visible viewport, only re-wrap from the previous line-start of the
  edit through end-of-visible.
- **Fix (proper):** Real incremental layout — segment the layout into
  paragraph-sized chunks that can be invalidated independently.

#### `buffer.String()` is called in ~15 places outside `buffer/`
- **Smell:** Each call copies the entire file.
- **Fix:** Audit callers and replace with iterators / `ReadAt` slices.
  This is the enabling change for many of the items above (Wrap,
  lineRange, execX, execG guard-check, addr regexp evaluation, …).

#### Regex compilation is not cached across a command
- **Where:** `regexp/regexp.go` `Compile`
- **Smell:** Called every time `execG`, `execX`, `execSubst`, etc. run,
  even inside an outer `x` loop where the same pattern is used per
  iteration. The `//` reuse hack is a package-level "last one" only.
- **Fix:** LRU cache keyed by the original Sam pattern string. Cheap
  and helps every structural command that has a body containing another
  regexp command.

### If picking three to do first

1. ~~`preGapRunes` cache in the gap buffer (Tier-1 items 1 + 2).~~ DONE
   2026-08-22.
2. ~~`LineAtY` binary search in `pane` (Tier-2 item 1).~~ DONE
   2026-08-22.
3. ~~Line-start table in `addr` (Tier-2 item 2).~~ DONE 2026-08-22.

With item 1 done, incremental layout is now the single thing standing
between "fast small-file editor" and "editable at any size" — every
keystroke still re-wraps the whole file, and `pane.Wrap`'s
`[]rune(buf.String())` still copies it twice on the way.
