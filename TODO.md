# TODO

Known issues found during the 2026-08-22 review that aren't yet fixed.
Not urgent, but recorded so they don't get lost. Each item includes
the smell, where it lives, and a sketch of the fix.

## Correctness

*(All items in this section were fixed on 2026-08-22 — see git log.)*

## Durability

*(Fixed on 2026-08-22 — `WriteAtomic` now fsyncs the file before rename
and best-effort fsyncs the parent directory after.)*

## Test-suite gaps

*(All items covered on 2026-08-22 in `cmd/gaps_test.go`. Writing the
`#n` non-ASCII test surfaced a real bug — `#` was being swallowed as a
comment before it could be parsed as a char-offset address; now fixed.)*

## Performance

Ranked by realistic impact per keystroke or per frame on a 100KB+ file.
Plan 9's sam is fast because its buffer is designed so common operations
don't traverse the whole file, and rendering only touches what's
on-screen. Dave has room to close both gaps significantly.

### Tier 1 — the ones that turn small files into big ones

#### `buffer.runeToBytePos` scans from position 0 every call
- **Where:** `buffer/buffer.go:55`
- **Smell:** Walks `data[:gapStart]` counting runes on every `Insert`,
  `Delete`, and `ReadAt`. Typing a character 90% through a 1MB file
  forces a scan of 900KB of text just to find the insert byte offset.
- **Fix:** Maintain a `preGapRunes int` cache; update it in `moveGap`
  (add/subtract as bytes shuffle across the gap) and after
  `insert`/`delete`. Then `runeToBytePos(runePos)` becomes a comparison
  and a bounded walk within one side. Classic gap-buffer optimization;
  roughly a 100× speedup on large-file edits.
- **Priority:** highest-leverage change in the codebase.

#### `buffer.Len()` scans the whole buffer
- **Where:** `buffer/buffer.go:43`
- **Smell:** Called constantly (every relayout, every address eval,
  every draw). Walks both sides of the gap counting runes.
- **Fix:** Cache total rune count, invalidate/update in `Insert` and
  `Delete`. Cheap to add once `preGapRunes` exists — total is
  `preGapRunes + postGapRunes`.

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

### Tier 2 — per-frame or per-command O(n)

#### `pane.drawBody` linearly scans all layout lines to find visible ones
- **Where:** `pane/pane.go:279`
- **Smell:** For a 100K-line file that's 100K iterations per frame at
  60 fps.
- **Fix:** The layout's `Lines` are sorted by `Y`; binary-search for
  the first `Y >= scrollY`. Layout already has `LineAt` doing the
  equivalent for codepoint offset; add `LineAtY`. `CPAtXY`
  (`pane/pane.go:155`) has the same problem in reverse.

#### `addr.lineRange` re-scans the whole file for every line address
- **Where:** `addr/addr.go:95`
- **Smell:** Reads `f.String()` and byte-scans for newlines. Called by
  every `2p`, every ranged address, every `,x` (via the `,` shorthand).
- **Fix:** Maintain a line-start table on the buffer, invalidated by
  `Gen`. Rebuild lazily on first line query per generation. For a
  100K-line file this converts every ranged command from O(n) to
  O(log n).

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

1. `preGapRunes` cache in the gap buffer (Tier-1 items 1 + 2). One
   change, dozens of speedups fall out.
2. `LineAtY` binary search in `pane` (Tier-2 item 1). Trivial change,
   immediate frame-time improvement.
3. Line-start table in `addr` (Tier-2 item 2). Ranged commands become
   near-free.

Items 1 and "incremental layout" are the ones that separate "fast
small-file editor" from "editable at any size."
