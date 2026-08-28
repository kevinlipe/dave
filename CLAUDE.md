# dave

A text editor in Go + SDL2, named after Sam & Dave and a spiritual successor to
Rob Pike's `sam`. Selection-oriented, driven by sam's command language and
structural regular expressions, with first-class soft word wrap for prose.

Two binaries from one module:

- **`dave`** — the editor. One SDL2 window: a command window on top, one or two
  file panes below. No sam/samterm split, no built-in window system.
- **`sdave`** — a stream editor over the same command language, in the shape of
  `ssam`. No SDL, no window.

## Build, test, run

    make all        # dave (or dave.exe) and sdave
    make test       # go test ./...
    make check      # build + test — use before pushing
    make static     # Windows: SDL2 linked statically (needs MSYS2 ucrt64)

`dave` needs SDL2 and cgo. The text engine and `sdave` do not — `go test
./buffer/ ./regexp/ ./file/ ./addr/ ./cmd/ ./sdave/` works without SDL, and
that is usually the fast loop.

    dave [-d] [file ...]        # -d is a headless REPL: no window, reads commands on stdin
    sdave [-n] [-e script] [-f scriptfile] [file ...]

`-d` is the quickest way to exercise the engine by hand. Config lives at
`~/.config/dave/config` (`%APPDATA%\dave\config` on Windows); a missing file is
written with commented defaults, and an **unknown key is an error** — at
startup that discards the whole config for defaults with only a line on stderr.

## Layering

Verified import graph — no dave-internal imports beyond these:

    buffer  regexp  config      (no internal deps)
    file    -> buffer
    addr    -> buffer file regexp
    cmd     -> addr buffer file regexp
    render  -> config
    pane    -> buffer config file render
    menu    -> file render
    input   -> buffer cmd file pane render
    sdave   -> buffer cmd file
    main    -> all of the above

**The text engine (`buffer`, `regexp`, `file`, `addr`, `cmd`) must never import
the rendering layer (`render`, `pane`, `input`, `menu`).** That rule is what
keeps `-d` and `sdave` free of SDL, and it is currently unbroken. Check it after
any change that moves code between layers.

## Invariants

These are expensive to violate and hard to see from any one file.

1. **All positions are codepoint offsets.** Byte offsets exist only inside
   `buffer/`, and any position crossing a package boundary is a rune offset.
   `buffer.Range{Start, End}` throughout. The single exception is
   `Buffer.ByteOffset`, for a caller that holds `Buffer.String()` and has to
   index into it — which means the regexp search, since Go's `regexp` runs
   over a string. It hands out the buffer's own byte coordinates for the
   buffer's own content; nothing else may take a byte offset across the line.
2. **The gap begins and ends on a rune boundary.** `buffer/write.go` writes the
   two gap segments raw and depends on this; so does `runeToBytePos`.
3. **Buffer text is LF-only.** CRLF is stripped on read and restored by a
   writer wrapper at the I/O boundary (`file/crlf.go`), never at a call site —
   three write paths used to each remember, and one forgot. `File.writer(w)` is
   the single point that knows.
4. **Commands evaluate in two passes.** Addresses resolve against the original
   buffer state, then the accumulated transcript applies atomically
   (`cmd/session.go`). The buffer is not updated between commands.
5. **Undo is per-command inverse transcripts**, applied in reverse, on one
   timeline shared by every file — two stacks, sam's `delta` and `epsilon`
   (`cmd/undo.go`). One command is one step, however many mutations or files
   it spanned; `u` steps back, `u-n` forward, and a new edit clears the redo
   stack. Undo restores dot, the mark, the name and the modify bit with the
   text. Two deliberate departures, both in TODO.md: sam's undo is per file
   and anchored on the current one, and sam runs a typing run through a
   newline where dave breaks it.
6. **The command window is not undoable**, deliberately, and matches sam: Enter
   *runs* the command, and undoing its text would not unrun it. `u` does that.

## Conventions

**Tests carry an explanation.** A comment above the test says what it pins and
why it could break — see `input/cursor_test.go`, `file/crlf_test.go`. Prefer a
property against a trusted oracle over a hand-written expectation:
`WriteRangeTo` is checked against `ReadAt`, incremental layout against a full
`Wrap`, `lineAt` against the scan it replaced.

**Mutation-verify new tests.** Break the code deliberately and confirm the test
fails. This has twice caught tests that passed for the wrong reason — a bound
that was actually enforced by a later short read, and a pinned bug asserting the
wrong value. Passing on the first run is not evidence.

**A differential harness runs dave against the real sam.** `make difftest`
feeds the scripts in `cmd/testdata/diff/` to plan9port's `sam -d` and to dave's
engine and compares what each wrote. It is the cheapest way to find a
divergence nobody thought to look for — every bug this project has fixed lived
at a composition point that hand-written tests missed. `go test` skips it when
sam is absent; `make difftest` fails loudly instead. The oracle is plan9port
and the reference is 9front, so a diff is a candidate to settle against the
9front source, not a verdict.

**Golden files are regenerated deliberately**, and the diff gets read:

    go test ./cmd/  -run TestCharacterize     -update
    go test ./pane/ -run TestWrapCharacterize -update

**Commits** are conventional-commit style with a scope (`fix(file):`,
`perf(pane):`). The body explains **what changed and why** — the context, the
reasoning, the option not taken. It does not explain *how*: assume the diff
covers that. Include a measurement when the change is about performance.

Keep it short. A small change deserves a subject line and a sentence or two;
only a change that turns on real reasoning earns several paragraphs. If the
body is walking through the diff, it is too long.

**gofmt**: several files carry pre-existing alignment drift. Format the files
you touch; don't sweep the repo in an unrelated change.

## Gotchas

- **`md/` is gitignored and stale** — all of it. Those early design docs do not
  describe the editor as it is; don't treat any of them as authoritative.
  `TODO.md` is current, and is where open work, known limitations and rejected
  options live — read it before proposing a change to rendering or layout.
- **A clone gets no design docs.** `README.md`, `TODO.md`, `doc/command-ref.md`,
  `doc/sam-differences.md` and this file are the whole written record.
- **`Buffer.String()` is cached, and costs a second copy of the file.** It is
  dropped at the next edit. The cache is what makes a regexp address inside an
  `x` loop linear rather than quadratic, since a command evaluates against one
  unchanging state; the price is that a searched file sits in memory twice.
- **`file.WriteContent` is not called `WriteTo` on purpose.** `go vet`'s
  stdmethods check reads `WriteTo(io.Writer) error` as a broken `io.WriterTo`.
- **Kerning is inconsistent on purpose, for now.** `TTFFace.MeasureText` kerns;
  every path that computes a position sums `GlyphAdvance` and does not. It is
  latent (monospace fonts have empty kern tables) and the kerning is believed to
  be load-bearing for some mono fonts. Leave it alone.
- **`CmdWindow` duplicates much of `Pane`.** Both are a scrollable, selectable
  text view over a buffer. A fix to one usually belongs in the other — check.
  `TODO.md` has the merge scoped, including why it should not start before
  `CmdWindow` has behaviour tests.
- **`menu/` has no tests**, and the two `render.Face` implementations are
  untested above their parsing layer, since that needs a live renderer.
