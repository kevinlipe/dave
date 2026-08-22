# TODO

Known issues found during the 2026-08-22 review that aren't yet fixed.
Not urgent, but recorded so they don't get lost. Each item includes
the smell, where it lives, and a sketch of the fix.

## Correctness

### `e` marks the file modified after loading from disk
- **Where:** `cmd/exec_file.go` `execE`
- **Smell:** `e file` replaces the buffer with disk content by routing the
  change through `ctx.addChange`, whose `commit` unconditionally calls
  `SetModified(true)`. Per sam, `e` should leave the file *clean* — the
  buffer now matches disk.
- **Fix:** Either bypass the transcript for `e` (replace buffer directly,
  push a manually-built inverse for undo), or have `execE` clear
  modified after commit. The former is cleaner because `e`'s inverse
  transcript is trivially the old contents.
- **Test gap:** no `e` tests exist.

### `substAll` can skip the final dot update on empty replacement at position 0
- **Where:** `cmd/exec_subst.go:85`
- **Smell:** `if lastDot.Start > 0 || lastDot.End > 0` uses "numbers non-zero"
  as a proxy for "a match happened." Fails for `s/^/replacement/g` on an
  empty file, or `s/foo//g` where the only match starts at 0 and expands
  to empty.
- **Fix:** Track a `matched bool` alongside `lastDot` and update dot iff
  matched.
- **Test gap:** no substitute-at-0-with-empty test.

### `commit` on multi-file transactions can leave a half-mutated world
- **Where:** `cmd/session.go` `commit`
- **Smell:** If `p.transcript.Apply(f.Buffer())` succeeds for file 1 and
  fails for file 2 (rare but possible on transcript-ordering errors),
  file 1 is mutated with no undo entry pushed — the entry is only
  appended to `Undo` after the loop completes.
- **Fix:** Either push per-file entries as we go (so partial commit is
  partially undoable), or apply all transcripts to a scratch first and
  commit atomically. The first option is simpler and matches sam's
  file-by-file transactional model.
- **Test gap:** no multi-file-failure test.

### `parseBody` silently accepts `x/re/` with no body
- **Where:** `cmd/parse.go` `parseBody`
- **Smell:** Returns `nil, nil` when `parseOne` returns nil, producing a
  Command with `Body: nil` that executes as a no-op. Sam prints `?` on
  malformed commands.
- **Fix:** Return an error if `parseOne` returns `nil` at end-of-input
  inside a required body.
- **Test gap:** no test for missing bodies.

### `X` file loops leave `Current` pointing at the last visited file
- **Where:** `cmd/exec_struct.go` `execXFile` / `execYFile`
- **Smell:** After `X/foo/ p`, `s.Current` is whichever file was last
  visited, not what it was before. Possibly intentional (sam does this
  too), but nothing documents it. Users can be surprised when a
  subsequent bare-address command targets an unexpected file.
- **Fix (if we want to diverge):** save/restore Current across the loop.
  If we keep the current behavior, document it in the command ref.

## Durability

### `WriteAtomic` doesn't fsync
- **Where:** `file/file.go` `WriteAtomic`
- **Smell:** After a power loss, the temp file's data may not have
  reached disk before rename, so the destination could point at
  zero-length or truncated content.
- **Fix:** `fh.Sync()` before `fh.Close()`, and fsync the parent
  directory after the rename. This is cheap on modern SSDs and a
  no-op cost on typical edits.
- **Priority:** low — a text editor crash-losing the last save is
  annoying but not catastrophic.

## Test-suite gaps (unchanged from review)

None of these are bugs on their own, but every bug in this list lived
in an untested region. Adding coverage here would catch the next round
before it ships:

- `r`, `e`, `k`, `'` (mark and mark-address)
- `<`, `>`, `|` shell commands
- Undo restoring an entire `x` loop as a single unit
- Structural commands (`x`/`y`/`g`/`v`) inside `{ ... }`
- Parser errors: `x/re` with no body, `{` never closed, missing `/`
- Empty-pattern reuse `//` in the command path (regexp package tests it, cmd doesn't)
- Backward-search `?re?` used as an address on a command
- `s/re/repl\nmore/` with a newline in the replacement
- Non-ASCII content through `w` (partially added), `=`, and `#n` addresses

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
