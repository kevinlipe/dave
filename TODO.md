# TODO

Open work, known limitations, and options rejected on purpose. Resolved
items are removed rather than struck through — the git log has what was
done and why.

The text engine is in good shape: correct against sam's semantics, covered
by a characterisation corpus and by a differential harness against the real
sam, and running a port of sam's own matcher rather than Go's regexp.
**The remaining work is rendering and layout**, which is where every open
item below lives except the `CmdWindow`/`Pane` merge.

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

## Elsewhere

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

### A search scans at about 80 MB/s

Dave runs sam's matcher over the gap buffer a rune at a time. Go's
regexp managed 20 GB/s on a pattern with a literal prefix, because it
could `memchr` its way along a string.

This is written down so that nobody fixes it the way it looks like it
should be fixed. A literal-prefix skip does nothing for `[A-Z][a-z]+`,
which costs the same as a literal scan and is what a structural command
actually runs; and an optional interface that `regexp` type-asserts for
is worse than no fast path, since it would appear or vanish with the
dynamic type of the input. The real mismatch is that the matcher steps
strictly forward or strictly backward and never seeks, yet asks through
a random-access interface and pays position bookkeeping on every step:
walking a segment directly costs 3.5 ns a rune against `RuneAt`'s 12.2.
A cursor is the shape that would help every pattern rather than some.

It is 5 ms on an 80k-word manuscript, which is what dave is for.

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
`doc/sam-differences.md`.

Nothing is open. Audited against the C: undo and redo and everything they touch, address
*evaluation*, the structural commands, the shell redirections, the
`e`/`r` I/O paths, `{}` groups, the file list, the read and write paths,
which of sam's two output streams each command prints on, the command
line, and the window layer.

Regexps are no longer audited against the C — dave runs a port of it, and
the differential harness compares the two over the whole pattern
language rather than over the part they had in common. The three
divergences that closed with the port are held there by the `backsearch`
and `anchors` cases.

### Not looked at

Nothing systematic remains. What has never been read closely in samterm
is the drawing layer under the bindings — the frame library, the scroll
bar's exact geometry, and the `Tsnarf`/`Tcut`/`Tpaste` protocol between
the two halves, which dave replaced with the system clipboard rather
than reimplemented. None of it is behaviour a user could name.
