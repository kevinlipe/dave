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

### Redrawing happens on event batches that changed nothing

- **Where:** `main.go` — the event loop drains events then calls
  `drawFrame()` unconditionally.
- **Smell:** mouse motion with no button held is the clear case:
  `HandleMouseMotion` correctly returns at `input/input.go:271` having
  done nothing, and the frame is repainted anyway. Moving the pointer
  across the window produces a stream of identical frames.
- **Fix:** compare cheap state across the batch — buffer generations,
  scroll offsets, dot, focus, menu open — and skip the draw when none
  moved. Derive it rather than having each handler set a dirty flag, for
  the same reason CRLF encoding became a property of the writer instead
  of something every write path had to remember.
- **Note:** the loop itself is already right — `WaitEvent` blocks,
  `PollEvent` drains, one draw per batch, no timers and no cursor blink,
  so dave idles at zero.

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

### ~~The command window redrew its whole scrollback every frame~~ — done

`CmdWindow.Draw` walked every line in the buffer and converted each to
runes *before* asking whether it was on screen, with no `break` past the
bottom. The command window is append-only, so that cost grew with every
command run for the whole session, on every frame — including the frames
mouse motion triggers. `EnsureVisible` (every keystroke) and `CPAtXY`
(every click and drag event) had the same scan.

All three now read a cached table of per-line codepoint offsets, built
with the line split it was already doing. Drawing starts at an
arithmetic first-visible index and breaks past the bottom;
`EnsureVisible` binary-searches; `CPAtXY` is a lookup.
`input/cmdwin_test.go` — the package's first tests for this type — pins
the table against the running computation it replaced, and is
mutation-verified against byte-vs-codepoint offsets, an uncounted
newline, both `lineAt` boundary errors, and a cache that never
invalidates.

`main.go` also pushed the window title to the window server on every
frame, rebuilding it with a `MenuLine` per pane, for a string that
changes when a filename or modified flag does. It is compared against
the last one now.

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

### `CmdWindow` is a second implementation of `Pane`

- **Where:** `input/cmdwin.go` against `pane/pane.go`
- **Smell:** both are a scrollable, selectable, editable text view over a
  buffer, written twice. `CmdWindow` independently has `ScrollBy`,
  `ScrollY`, `EnsureVisible`, `CPAtXY`, `Dot`, `ExtendDrag`, `Snarf`,
  `Cut`, `Paste`, `deleteSelection`, `deleteBackward`, `deleteForward`
  and its own draw with selection and caret. `Pane` has all the same
  over `VisualLine{Lo,Hi}` offsets; `CmdWindow` has them over a cached
  `[]string`.
- **What it costs:** the per-frame fix above had to be made twice, in
  two shapes. The kerning split lives in `cmdwin.go` twice over — the
  hit-test at `:174` sums advances, the caret and selection at `:457`
  and `:469` use `MeasureText`. Cut/Paste is asymmetric: methods on
  `CmdWindow`, but inlined into `applyMenuResult` for panes
  (`main.go:384`), so a new action gets written twice. There is a dead
  `isCmd` parameter on `Pane.drawScrollbar` (`pane/pane.go:337`), whose
  only caller passes `false` — someone started this once.
- **Shape of the fix:** not "CmdWindow becomes a Pane" but "CmdWindow
  owns a Pane for the text-view part", keeping execute-on-Enter,
  `AppendOutput` and its keyboard handling. The differences are smaller
  than they look: no olivetti is just `wordWrap=false`, which `Pane`
  already turns into `margin = 0`; append-only is not a `Pane` concern
  at all. The one that needs real work is the palette — `Pane`
  hardcodes `pal.Text`, `pal.Background`, `pal.BodySelection*` and
  `pal.Scrollbar` at about eight sites, and those would have to become
  roles chosen at construction.
- **Merge the view, not the model.** `Pane` is built on a `*file.File`
  while `CmdWindow` holds a bare buffer, and it is tempting to close
  that gap too. Sam did: `Tstartcmdfile` runs `cmd = newfile()`
  (`sam/mesg.c:230`), so sam's command window is a real `File` with a
  tag in the file list, its output arrives by `loginsert`
  (`mesg.c:582`), and cuts in it journal through `logdelete` and bump
  `seq` exactly like a document. Sam pays for that with **19 explicit
  `f == cmd` exclusions** across `sam.c`, `file.c`, `rasp.c`, `xec.c`,
  `address.c` and `multi.c` — every place "the command window is a
  file" is a lie that has to be corrected: it is skipped by undo
  (`sam.c:454`), by `X` (`xec.c:523`), by the dirty/save scan, by
  multi-file operations. Sam earns that back because it is two
  processes: making `cmd` a file means the host/terminal sync protocol
  covers it for free. Dave is one process and has no such protocol, so
  the model half of the trade buys much less here. The duplication that
  actually hurts is in the view, and that half can be merged on its own.
- **Not urgent:** the performance argument for it is gone, since the
  divergence it caused is now fixed on both sides. What is left is
  avoiding future divergence, which is real but not pressing. **Do not
  attempt it before `CmdWindow` has tests for behaviour** — `Draw`,
  `HandleKey`, `execute`, cut/paste and drag selection are all
  uncovered, and the 13 places in `input/input.go` and `main.go` that
  branch on cmdwin-versus-pane are each a chance to get it subtly wrong
  in a way only hand-testing the GUI would find.
- **On undo, dave already does what sam does.** A command window should
  not be undoable: pressing Enter *runs* the command, and undoing the
  text of it does not unrun it — that takes `u`, typed in the same
  window, so the command window is the instrument of undo rather than a
  subject of it. Getting an earlier command back needs no history
  either, since every command is still sitting in the buffer as text and
  any line can be re-executed by putting the cursor on it.
  Sam agrees, and suppresses the transaction at the edit: `fileupdate`
  sets `notrans = TRUE` for `cmd` and skips the dot/mark bookkeeping
  undo would replay (`sam/file.c:408`), with `sam.c:454` skipping it
  again at the undo step. The `loginsert` that carries cmd-window output
  is transport, not history — in two processes, entering the log is how
  a change reaches samterm at all. Dave is one process and has no such
  pipeline, so it has nothing to suppress. Nothing to copy here; keep
  the behaviour.

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

There is no single canonical sam. Both trees are read —
`~/src/9front/sys/src/cmd/sam` and `~/src/plan9/src/cmd/sam` — and where
they differ the call is made case by case and recorded in
`doc/sam-differences.md`. plan9port has won more of those calls than
9front: it is the tree that runs here, and it is what the differential
harness compares against.

Accepted differences are documented for users in
`doc/sam-differences.md`. What follows is open.

Nothing is open in the command language, the file list or disk I/O.
Audited: undo and redo and everything they touch, regexps, addresses,
the structural commands, the shell redirections, the `e`/`r` I/O paths,
`{}` groups, the file list, the read and write paths, and which of sam's
two output streams each command prints on.

### Not looked at

Read in the C, never exercised: the rescue path that writes `$HOME/sam.save`
when sam dies with modified files; the append-only check on write; sam's
other command-line flags. Nothing in samterm has been compared at all —
dave's window layer is deliberately a different design, but that is an
assumption nobody has tested rather than a decision anyone recorded.
