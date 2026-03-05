# dave

A text editor named after Sam & Dave, the Stax Records legends. Dave is a spiritual successor to [Rob Pike's `sam` editor](http://sam.cat-v.org), reimagined for daily use on Linux and Windows, with first-class soft word wrap for prose editing.

## What it is

Dave uses Sam's command language and structural regular expressions. If you know `sam`, you know Dave. The main additions are word wrap, a more keyboard-friendly interface, and a config file.

The command window at the top accepts Sam commands. Right-click opens the buffer menu. Middle-click (or Shift+right-click) opens the editing menu (Cut, Paste, Snarf, Look, Send, Undo). These use the system clipboard. "Snarf" is Plan 9 terminology for copy (copy without deleting, like Ctrl+C elsewhere).

## Building

```
make all         # build dave and sdave
make sdave       # build sdave only
make static      # build a statically-compiled exe including SDL2
make install     # install dave and sdave via go install
```

Requires Go and SDL2. The binary embeds Go Mono as a fallback font; no fonts need to be installed.

## Fonts

Dave supports two font rendering modes. Set `font_mode` in the config file:

- **TTF** (default) — any TrueType or OpenType font installed on the system, rendered via Go's `x/image` stack. Set `font` to the family name (e.g. `Consolas`, `JetBrains Mono`). Falls back to an embedded Go Mono if the named font isn't found.
- **Plan 9 bitmap fonts** — the original bitmap fonts from Plan 9/inferno, rendered pixel-perfect with no antialiasing. Set `font_mode = plan9` and point `plan9_font` at a Plan 9 font file. Pairs especially well with low-DPI displays, but works nicely everywhere. Set `plan9_embiggen = true` to render at 150% scale.

## Window management

Sam manages its own windows, dividing the display into a column of resizable frames. Dave does not. Each Dave window shows one file at a time. Right-clicking opens a list of open buffers; selecting one brings it into view. To compare two files side by side, open two OS windows.

This is a deliberate simplification. The OS already provides a window manager. Reimplementing one inside the editor adds complexity without adding capability.

## Configuration

`~/.config/dave/config` on Linux, `%APPDATA%\dave\config` on Windows. If it doesn't exist launch, `dave` creates it.

## Invocation

```
dave [file ...]     # open files (no panes opened automatically)
dave -d [file ...]  # headless REPL mode, no GUI
```

## Command language (brief)

Addresses: `0` start, `$` end, `.` dot, `n` line, `#n` char offset, `/re/` next match, `a1,a2` range, `,` whole file.

| Command | Effect |
|---|---|
| `a/text/` `i/text/` `c/text/` | append / insert / change |
| `d` | delete dot |
| `s/re/repl/` | substitute (add `g` flag for all) |
| `p` `=` | print dot / print address |
| `x/re/ cmd` | run cmd for each match |
| `y/re/ cmd` | run cmd for text between matches |
| `g/re/ cmd` `v/re/ cmd` | run cmd if dot matches / doesn't match |
| `X/re/ cmd` `Y/re/ cmd` | run cmd across matching / non-matching files |
| `e` `r` `w` `f` | edit / read / write / rename |
| `B` `b` `D` | add / switch to / remove file |
| `u` `u`*n* | undo last / last n commands |
| `q` | quit (twice to force) |
| `< cmd` `> cmd` `\| cmd` | shell in / out / pipe |

See the [Sam paper](https://doc.cat-v.org/plan_9/4th_edition/papers/sam/) for the full reference; Dave's command language is intended to be identical.

## sdave

`sdave` is a companion stream editor (like `ssam` for `sam`). It applies Dave's command language to files or standard input non-interactively.

```
sdave [-n] [-e script] [-f scriptfile] [file ...]
```

Without `-e` or `-f`, the first positional argument is the script. Without `-n`, the entire buffer is printed after the script runs.

```
sdave 's/UNIX/& system/g'          # replace in stdin, print result
sdave -n ',10p' file               # print first 10 lines
sdave -n ',x/TODO/ p' *.go        # print every line containing TODO
sdave 'y/[a-zA-Z]+/ c/\n/'        # one word per line
```
