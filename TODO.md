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
