# Dave — Editor Specification

## Overview

Dave is a text editor for prose and code, named after Sam & Dave, the Stax Records legends. It is a spiritual successor to Rob Pike's `sam` editor, reimagined for single-user daily-driver use on Linux and Windows, with first-class support for word-wrapped prose editing.

Dave is implemented in Go with SDL2 for rendering. All Sam semantics — the address language, command language, and structural regular expressions — are implemented directly in Go. There is no scripting layer. The codebase should be small enough to fit entirely in one person's head.

The Sam paper (https://doc.cat-v.org/plan_9/4th_edition/papers/sam/) is the canonical reference for command language semantics. The deadpixi fork (https://github.com/deadpixi/sam) is a useful reference for modernization decisions including two-button mouse support.

The Nadir codebase (sibling directory, path to be confirmed at implementation time) contains reusable Go code for SDL2 rendering, TTF font loading, Plan 9 bitmap font support, and the main event loop. Reference it freely; Dave is a separate project but should not duplicate solved problems.

---

## Design Principles

1. **Files and panes are independent.** Files are open or not open. Panes are views. A file can be open with no pane displaying it. A pane displays exactly one file at a time.

2. **At most two panes.** Dave shows one pane normally, two panes when explicitly requested. There are no other window configurations.

3. **The command language is the power interface.** Mouse operations cover common cases. The command language covers everything else.

4. **No arbitrary limits.** Following Sam: no fixed limits on file count, file size, or line length.

5. **Word wrap is a first-class feature.** Dave exists partly because Sam has no word wrap. Soft word wrap is always on by default and togglable per-pane.

6. **Readable over clever.** The codebase should be written to be understood, not optimized prematurely. Sam's own implementation notes are a useful guide here.

---

## Technology Stack

- **Language:** Go
- **Rendering:** SDL2 (via go-sdl2 CGo bindings)
- **Font support:** TTF via SDL_ttf; Plan 9 bitmap fonts (code from Nadir)
- **Platform targets:** Linux (primary), Windows (secondary)
- **Build output:** Single static binary, no runtime dependencies
- **Third-party Go deps:** go-sdl2, go-sdl2/sdl\_ttf, and nothing else if possible

SDL2 and SDL_ttf should be statically linked or bundled where possible to keep the binary self-contained.

---

## File Model

Dave maintains an internal list of open files. Each file has:

- Its contents (in memory; see Buffer section below)
- Its associated filename on disk
- A **modified bit** — whether contents differ from disk
- A **dot** — the current selection, stored as a character range `(p1, p2)`
- A **menu line** — a concise status string used in the buffer menu, formatted as:
  - Character 1: `'` if modified, `-` if clean
  - Character 2: `.` if this is the current file, `-` otherwise  
  - A space, then the filename

Files are not read from disk until a command requires their contents (lazy loading, as in Sam).

The command `B filename ...` adds files to Dave's list. `D filename` removes them (will not remove a modified file without a second `D`). `D` with no argument removes the current file.

---

## Pane Model

Dave has a **pane list** of length 0, 1, or 2.

- Dave starts with **no panes open** and the command window visible. The user must explicitly open a file into a pane via the buffer menu or command language.
- A pane is created by selecting a file from the buffer menu, or by the `b` / `B` command in the command window. **New** in the buffer menu creates a new empty unnamed buffer and opens it in a pane.
- A second pane (sidebar) is created via the buffer menu's **Sidebar** item. It is destroyed via **Kill sidebar**.
- The split orientation (horizontal or vertical) is a user setting, defaulting to vertical (left/right).
- Each pane independently tracks:
  - Which file it is displaying
  - Its own scroll position
  - Its own value of dot (dot in the file is updated to match the active pane's dot when switching)
- The **active pane** receives keyboard input and command language operations. Clicking in a pane makes it active.
- When there is only one pane, it is displayed centered with left/right margins (olivetti-style). The margin width is configurable.
- When there are two panes, each takes half the window, with a minimal divider between them. Centering applies within each pane's half.
- When no panes are open, the full window area below the command window is empty.

---

## Buffer and Text Storage

Dave should use a **piece table** or **gap buffer** for text storage. The recommended starting point is a gap buffer for simplicity; this can be revisited if performance becomes a problem with large files.

The buffer must support:
- Insert at position
- Delete range
- Read substring by character position
- Forward and backward sequential access (for regex matching)

Positions are always **character offsets** (Unicode codepoints), not byte offsets. Dave is UTF-8 throughout.

### Transaction Log and Undo

Following Sam's model, changes are not applied directly. Instead:

- When a command begins, a mark is placed in a **transcript**
- Each change (insert or delete) is appended to the transcript as a record
- When the command completes, the transcript is applied to the buffer contents
- The inverse transcript is saved for undo

`u` undoes the last command. `u2`, `u3` etc. undo multiple commands. Undo covers all files affected by a command (multi-file undo).

---

## Rendering

### Fonts

Dave supports two font modes, switchable at runtime:

- **TTF mode:** Any TTF/OTF font, rendered via SDL_ttf. Default.
- **Plan 9 bitmap font mode:** Uses the Plan 9 font loader from Nadir. Fonts must be present on the system; Dave does not bundle them.

Font and mode are set in the config file (see Configuration section).

### Colors

Dave's default color scheme is Sam's classic two-color appearance: pale yellow background, black text, with selection shown in reverse video using a blue-grey highlight. These are the colors as used in the original Plan 9 sam and preserved in the deadpixi fork as the default.

| Element | Color name | Hex | RGB |
|---|---|---|---|
| Background | Light yellow | `#FFFFEA` | 255, 255, 234 |
| Text | Black | `#000000` | 0, 0, 0 |
| Selection background | Blue-grey | `#9FEEEE` | 159, 238, 238 |
| Selection text | Black | `#000000` | 0, 0, 0 |
| Pane border / divider | Dark grey | `#888888` | 136, 136, 136 |
| Pane header background | Slightly darker yellow | `#EEEEDD` | 238, 238, 221 |
| Pane header text | Black | `#000000` | 0, 0, 0 |
| Scrollbar (file windows) | Olive | `#99994C` | 153, 153, 76 |
| Command window background | Pale blue | `#EAFFFF` | 234, 255, 255 |
| Command window border (active) | Blue-purple | `#8888CC` | 136, 136, 204 |
| Command window scrollbar | Blue-purple | `#8888CC` | 136, 136, 204 |
| Menu background | White | `#FFFFFF` | 255, 255, 255 |
| Menu text | Black | `#000000` | 0, 0, 0 |
| Menu highlight | `#9FEEEE` | 159, 238, 238 | (same as selection) |
| Command window selection | Pale teal | `#9FEEEE` | 159, 238, 238 |

All colors are overridable in the config file using hex values:

```
color_background    = #FFFFEA
color_text          = #000000
color_selection_bg  = #9FEEEE
color_selection_fg  = #000000
color_border        = #888888
color_header_bg     = #EEEEDD
color_header_fg     = #000000
color_scrollbar     = #99994C
color_cmdwin_bg     = #EAFFFF
color_cmdwin_fg     = #000000
color_cmdwin_border = #8888CC
color_cmdwin_scroll = #8888CC
```

### Scrollbar

Each pane has a scrollbar on the left edge, following Sam's convention. The scrollbar is a colored strip (olive `#99994C` for file panes, blue-purple `#8888CC` for the command window) with a lighter bubble indicating the visible portion of the file.

Scrollbar interactions:
- **Left click** in the scrollbar: jump to the proportional position in the file corresponding to where you clicked
- **Left drag** in the scrollbar: scroll continuously as you drag
- **Scroll wheel** anywhere in a pane: scroll that pane

### Word Wrap

Soft word wrap is on by default. It wraps at word boundaries, not mid-word, and never inserts newline characters into the buffer. Long lines without spaces wrap at the pane edge as a fallback.

Word wrap is togglable per-pane with a keybind. The setting is not persisted across sessions (always starts wrapped).

### Display

- Active pane has a visible border or background tint to distinguish it
- Dot (current selection) is highlighted in reverse video
- Modified files show an indicator in the pane header (the menu line status character)
- A minimal scroll indicator on the left edge of each pane shows position in file
- No line numbers by default (configurable)

### Pane Header

Each pane has a single-line header showing the file's menu line. Clicking the header has no special behavior in v1.

---

## Mouse Interface

Dave uses a three-button mouse model with two-button fallback.

| Action | Effect |
|---|---|
| Left click | Set dot to click position (null selection) |
| Left drag | Set dot to dragged range |
| Double-click word | Select word |
| Double-click bracket/paren/quote | Select contents |
| Right click | Open buffer menu |
| Shift + right click | Open editing menu (button 2 fallback) |
| Middle click | Open editing menu |
| Scroll wheel | Scroll active pane |
| Click in inactive pane | Make that pane active |

### Buffer Menu (Right Click)

A popup list, positioned at the mouse cursor, containing:

- **New** — create a new empty unnamed buffer and open it in the current pane
- **Sidebar** — create second pane (label changes to **Kill sidebar** when a second pane is already open)
- **—** separator
- One line per open file, showing the file's menu line

The menu is context-aware: whichever pane the right-click occurred in receives the file selected from the list. This means right-clicking in the main pane and selecting a file loads that file into the main pane; right-clicking in the sidebar loads it into the sidebar.

When the sidebar is first created, it displays the same file as the main pane. The user right-clicks in the sidebar to load a different file if desired. The same file may be open in both panes simultaneously — each pane independently tracks its scroll position and dot.

### Editing Menu (Middle Click / Shift+Right Click)

| Item | Action |
|---|---|
| **Cut** | Delete dot, save contents to snarf buffer |
| **Paste** | Replace dot with snarf buffer contents |
| **Snarf** | Copy dot to snarf buffer without deleting |
| **Look** | Search forward for next literal occurrence of dot's text (no regexp) |
| **Send** | Send dot's text to the command window as if typed and executed |

**Look** wraps around end of file. **Send** is equivalent to typing the selected text into the command window and pressing Enter — useful for executing commands written inline in a document.

---

## Keyboard Interface

All menu operations have keyboard equivalents. Sam was designed around mouse use, but Dave is a daily driver on a laptop, so keyboard navigation must be complete enough to work without reaching for the trackpad.

### Editing Keys

| Key | Action |
|---|---|
| Any printable character | Insert at dot; replaces selection if dot is non-null |
| `Backspace` | Delete character before dot; delete selection if non-null |
| `Delete` | Delete character after dot; delete selection if non-null |
| `Enter` / `Return` | Insert newline at dot |
| `Tab` | Insert tab character at dot |

### Cursor Movement

Dave uses standard cursor movement keys. All movement collapses a selection to a null dot at the appropriate end, unless `Shift` is held, in which case the selection is extended.

| Key | Action |
|---|---|
| `Left` | Move dot one character left |
| `Right` | Move dot one character right |
| `Up` | Move dot one visual line up (respects word wrap) |
| `Down` | Move dot one visual line down (respects word wrap) |
| `Ctrl+Left` | Move dot one word left |
| `Ctrl+Right` | Move dot one word right |
| `Home` | Move dot to start of visual line |
| `End` | Move dot to end of visual line |
| `Ctrl+Home` | Move dot to start of file (equivalent to address `0`) |
| `Ctrl+End` | Move dot to end of file (equivalent to address `$`) |
| `Page Up` | Scroll up one screen; dot moves to top of new view |
| `Page Down` | Scroll down one screen; dot moves to top of new view |
| `Shift+Left` | Extend selection one character left |
| `Shift+Right` | Extend selection one character right |
| `Shift+Up` | Extend selection one visual line up |
| `Shift+Down` | Extend selection one visual line down |
| `Shift+Home` | Extend selection to start of visual line |
| `Shift+End` | Extend selection to end of visual line |
| `Shift+Ctrl+Home` | Extend selection to start of file |
| `Shift+Ctrl+End` | Extend selection to end of file |

**Word definition for `Ctrl+Left` / `Ctrl+Right`:** a word boundary is any transition between alphanumeric/underscore characters and anything else. This is consistent with Sam's double-click word selection.

**Visual line vs logical line:** when word wrap is on, `Up`/`Down`/`Home`/`End` operate on *visual* lines (wrapped display lines), not logical lines (newline-delimited). When word wrap is off, they operate on logical lines. This distinction matters for long prose lines.

### Command Window

`Escape` moves keyboard focus to the command window. Since the command window is always visible, this is purely a focus action — it does not show or hide anything. Dave has no other application-defined key actions outside of text editing and cursor movement.

---

## Command Window

The command window is always visible at the top of the Dave window, with its pale blue (`#EAFFFF`) background and blue-purple border (`#8888CC`). It is not toggleable — it is always present, following Sam's layout exactly as seen in the screenshot in the Sam paper.

On startup the command window is **empty**. There is no prompt string, no welcome message, no version info. Just the cursor.

Text typed in the command window is interpreted as Dave command language when Enter is pressed. The command window supports full cut-and-paste editing, so commands can be composed and edited before execution. Previous commands remain visible and can be re-executed by clicking into them and pressing Enter.

The command window has a fixed height (approximately 3-5 lines) and does not grow. It has its own scrollbar if content exceeds its height.

---

## Command Language

Dave's command language is Sam's command language, implemented faithfully in Go. The Sam paper is the normative reference. This section summarizes scope and any Dave-specific deviations.

### Addresses

| Address | Meaning |
|---|---|
| `n` | Line n |
| `0` | Beginning of file |
| `$` | End of file |
| `.` | Current dot |
| `/regexp/` | Next match forward |
| `-/regexp/` | Next match backward |
| `+` / `-` | Next/previous line relative to dot |
| `#n` | Character offset n |
| `a1,a2` | From start of a1 to end of a2 |
| `,` | Abbreviation for `0,$` (whole file) |
| `"regexp"` | File whose menu line matches regexp |

### Commands

| Command | Effect |
|---|---|
| `a/text/` | Append text after dot |
| `i/text/` | Insert text before dot |
| `c/text/` | Change dot to text |
| `d` | Delete dot |
| `s/regexp/replacement/` | Substitute first match in dot |
| `p` | Print dot to command window |
| `=` | Print address of dot |
| `x/regexp/ cmd` | For each match in dot, set dot and run cmd |
| `y/regexp/ cmd` | For each non-match in dot, set dot and run cmd |
| `g/regexp/ cmd` | If dot contains match, run cmd (dot unchanged) |
| `v/regexp/ cmd` | If dot contains no match, run cmd |
| `X/regexp/ cmd` | For each file whose menu line matches, run cmd |
| `Y/regexp/ cmd` | For each file whose menu line doesn't match, run cmd |
| `{ cmd... }` | Group commands |
| `e filename` | Replace current file contents with named file |
| `r filename` | Replace dot with contents of named file |
| `w filename` | Write current file to disk (filename optional) |
| `f filename` | Set current file's name |
| `b filename` | Make named file current |
| `B filename...` | Add files to Dave's list and make last one current |
| `D filename...` | Remove files from Dave's list |
| `u` / `un` | Undo last n commands |
| `q` | Quit. If any open files have unsaved changes, prints their names and does nothing. A second `q` quits unconditionally. |

### Shell Commands

The `<`, `>`, and `|` commands hand off to the system shell:

- **Linux:** `sh -c <command>`
- **Windows:** controlled by the `use_pwsh` config option (see Configuration section)

The shell selection is resolved once at config load time, not checked on every command execution. These commands are **in scope for v1 on both platforms.**

- `<`, `>`, `|` (pipe to/from Unix programs) are **in scope for v1** on Linux; Windows support is a stretch goal
- The `New` command (create window) and `Zerox` command (clone window) are **not implemented** — Dave's pane model replaces them
- `Reshape` and `Close` window commands are **not implemented**
- The `m` (move) and `t` (copy between files) commands should be implemented

### Structural Regular Expressions

Dave implements Sam's full structural regexp model: `x`, `y`, `g`, `v` commands as described in the Sam paper. The implementation should be correct over clever — a clear, readable Go implementation is preferred over an optimized one.

Regular expression syntax: Go's `regexp` package (`RE2` syntax). This differs slightly from Sam's `egrep` syntax; the differences should be documented in Dave's help text. Notably:
- `@` matches any character (Sam extension — implement this)
- `\n` matches newline (implement this)
- `.` does not match newline (same as Sam)

---

## Configuration

Dave reads a config file from `~/.config/dave/config` on Linux and `%APPDATA%\dave\config` on Windows. Format is simple `key = value`, one per line, comments with `#`.

| Key | Default | Meaning |
|---|---|---|
| `font` | _(embedded Go Mono)_ | TTF font name as known to the system (e.g. `Go Mono`, `Inconsolata`, `Courier New`). If unset, the Go Mono font embedded in the binary is used directly with no system lookup. |
| `font_size` | `14` | Font size in points |
| `font_mode` | `ttf` | `ttf` or `plan9` |
| `plan9_font` | _(none)_ | Full path to Plan 9 font file if font_mode = plan9 |

**Implementation note — font name lookup:** SDL_ttf's `TTF_OpenFont` takes a file path, not a font name. Dave must resolve the configured font name to a file path before opening it. On Linux, use fontconfig (`fc-match` or the fontconfig C API via CGo) to resolve the name to a path. On Windows, enumerate installed fonts via the GDI `EnumFontFamilies` API or look up the font file path in the registry under `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`. If the named font cannot be found, fall back to the embedded Go Mono and log a warning.

**Implementation note — embedded Go Mono:** The Go Mono TTF font file should be embedded directly in the Dave binary using Go's `//go:embed` directive. This is the default font and requires no system lookup. It is always available regardless of what fonts are installed on the host system. The font file is available from `golang.org/x/image/font/gofont/gomono`.
| `word_wrap` | `true` | Soft word wrap on or off |
| `margin` | `80` | Olivetti margin width in characters (only applies when word_wrap = true) |
| `split` | `vertical` | Default split orientation (`vertical` or `horizontal`) |
| `tab_width` | `4` | Tab stop width in spaces |
| `line_numbers` | `false` | Show line numbers |
| `use_pwsh` | `true` | (Windows only) use `pwsh` (PowerShell Core) for shell commands; set to `false` to use `powershell.exe` instead. UTF-8 output encoding is forced in both cases. Ignored on Linux. |

Color overrides may also be set in the config file using the `color_*` keys listed in the Colors section above.

---

## Invocation

```
dave [file1 file2 ...]
dave -d [file1 file2 ...]
```

- **With no arguments:** opens with the command window visible and no panes. The user opens files via the command language or buffer menu.
- **With filenames:** opens all named files into Dave's file list. No panes are opened automatically — the user selects which file to view via the buffer menu. Files are loaded lazily (not read from disk until needed).
- **New files:** if a named file does not exist on disk, Dave creates an empty buffer with that name. The file is created on disk on the first explicit write (`w` command or equivalent). No error is shown at startup for non-existent files.

### `-d` Mode — Headless Interactive

`-d` starts Dave without an SDL window as a fully interactive terminal session. It is the complete Dave editor experience minus the GUI:

- Full session state: file list, dot per file, undo history
- Command prompt in the terminal — type commands, press Enter to execute
- Results printed to terminal output
- On both Linux and Windows, uses the terminal Dave was launched from
- `q` exits, printing modified file names if any exist; a second `q` exits unconditionally

---

## Project Structure

Suggested Go package layout:

```
dave/
  main.go           — entry point, argument parsing, config loading
  sdave/
    main.go         — sdave stream editor entry point
  buffer/           — text storage, gap buffer, transcript, undo
  file/             — file model, disk I/O, menu line
  addr/             — address language parser and evaluator
  cmd/              — command language parser and executor
  regexp/           — regexp wrapper (Go regexp + Sam extensions)
  pane/             — pane model, word wrap, scroll
  render/           — SDL2 rendering, font loading (TTF + Plan 9)
  input/            — keyboard and mouse event handling
  menu/             — buffer menu and editing menu
  config/           — config file parsing
```

---

## Reuse from Nadir

The following should be reviewed for direct reuse or close adaptation:

- SDL2 initialization and main event loop
- TTF font loading and text rendering
- Plan 9 bitmap font loader and renderer
- Window creation and resize handling

Dave is a separate binary and repository. Copy and adapt rather than import from Nadir; the shared surface area is small enough that duplication is preferable to coupling.

---

## Testing

Unit tests should be written **alongside** the code for the following packages. These are the parts of Dave where bugs are silent, cumulative, or extremely hard to diagnose through the UI. Everything else can be tested manually during development.

### `buffer/` — Text Storage and Undo

This is the most critical package to test. Bugs here corrupt data. Write tests for:

- **Insert at position:** insert at start, end, and middle of buffer; insert empty string; insert multi-byte UTF-8 sequences
- **Delete range:** delete at start, end, middle; delete entire buffer contents; delete zero-length range (no-op)
- **Read substring:** read from arbitrary offsets; read across gap boundary (gap buffer specific); read entire buffer; read empty range
- **Sequential access:** forward scan from position 0; backward scan from end; scan starting mid-buffer
- **UTF-8 correctness:** confirm all positions are codepoint offsets, not byte offsets; test with multi-byte characters (em-dash, smart quotes, common prose punctuation)
- **Transaction log:** verify that a command's changes are not visible until the transcript is applied; verify that a partially-executed command leaves the buffer unchanged on error (transcript rollback)
- **Undo:** single undo restores buffer and dot exactly; `u2` undoes two commands in sequence; undo of a multi-file command restores all affected files; undo cannot be undone; undo of `e filename` restores previous contents, name, dot, and modified bit

### `addr/` — Address Language

The Sam paper's address table is your test specification. Write a test for every address form:

- **Line addresses:** `0`, `1`, `$`, `n` for valid n, n beyond end of file (error)
- **Character addresses:** `#0`, `#n`, `#n` beyond end of file (error)
- **Dot:** `.` returns current dot exactly
- **Regexp addresses:** `/pat/` finds next match forward from dot; `-/pat/` finds previous match backward; `0+/pat/` finds first match in file; `$-/pat/` finds last match in file; failed search returns error not panic
- **Relative addresses:** `.+1`, `.-1`, `+`, `-`, `+-` (line containing end of dot)
- **Compound addresses:** `1,5`; `.,$ `; `,` (whole file); addresses out of order produce error
- **File addresses:** `"regexp"` selects file whose menu line matches; no match produces error; more than one match produces error
- **Default address insertion:** bare regexp gets implicit `+` prefix; commands with no address get implicit `.`

### `cmd/` — Command Language

Test each command in isolation with known buffer contents and known dot, and verify the resulting buffer contents, dot position, and modified bit:

- `a/text/` — appended text appears after dot; dot set to new text
- `i/text/` — inserted text appears before dot; dot set to new text
- `c/text/` — dot replaced; dot set to new text
- `d` — dot deleted; dot set to null string at deletion point
- `s/regexp/replacement/` — first match in dot replaced; dot set to result; `&` in replacement expands to matched text; failed match leaves buffer unchanged
- `p` — dot contents printed to command window output; buffer unchanged
- `x/regexp/ cmd` — command runs once per match; dot set correctly for each; null matches handled (at least one character between adjacent null matches); overlapping matches not generated
- `y/regexp/ cmd` — command runs for text between matches; semantics match Sam paper example (`c/AAA/ y/A/ c/-/` produces `-A-A-A-`)
- `g/regexp/ cmd` — command runs once if match exists; dot unchanged; command does not run if no match
- `v/regexp/ cmd` — complement of `g`
- `X/regexp/ cmd` and `Y/regexp/ cmd` — run against correct set of files based on menu line matching
- `{ cmd1 cmd2 }` — both commands execute; second command sees dot left by first
- `u` / `un` — verified via buffer undo tests above; test here that the command parser accepts `u`, `u2`, `u3` correctly
- `B`, `D`, `b`, `e`, `r`, `w`, `f` — file operation commands; test that file list, current file, contents, name, and modified bit are updated correctly in each case
- **Error handling:** confirm that errors mid-command roll back all changes made so far; confirm error messages are non-empty strings and do not panic

### `regexp/` — Regexp Extensions

Go's `regexp` package does not natively support all of Sam's extensions. Test the wrapper:

- `@` matches any character including newline
- `\n` in a pattern matches a literal newline character
- `.` does not match newline (same as Go default)
- Backward search finds correct leftmost-longest match when scanning in reverse
- Backward search with `^` and `$` has exchanged meanings (per Sam paper)
- Empty pattern reuses last used pattern (Sam behavior)

### Test Helpers

Write a small test helper that:
- Creates an in-memory buffer from a string literal
- Sets dot to a specified range
- Runs a command language string against it
- Returns resulting buffer contents, dot, and any error

This makes command language tests one or two lines each and readable as documentation. Example:

```go
result := runCmd(t, "Hello, Peter.", dot(7, 13), "c/world/")
assertEqual(t, result.contents, "Hello, world.")
assertEqual(t, result.dot, range(7, 12))
```

---



## Out of Scope for v1

The following are explicitly deferred:

- Syntax highlighting
- Auto-indent or language-aware features  
- Plugin or extension system
- Network/remote editing (Sam's two-process model)
- Tabs (use buffers instead)
- Multiple cursors
- `<`, `>`, `|` commands on Windows

---

## sdave — Stream Interface

`sdave` is a companion binary, analogous to `ssam` for Sam. It applies Dave's command language to files or standard input non-interactively, like `sed` but using Dave's address and command language.

```
sdave [-n] [-e script] [-f scriptfile] [file ...]
```

- With no file arguments: reads from standard input
- `-e script`: run this command language string as the script
- `-f scriptfile`: read the script from a file
- `-n`: suppress default output (without `-n`, the entire buffer is printed after the script runs)
- Multiple `-e` flags may be combined

**Examples:**

```
sdave -n ,10p file              # print first 10 lines
sdave 's/UNIX/& system/g'       # replace in stdin
sdave 'y/[a-zA-Z]+/ c/\n/'     # one word per line
sdave -n ',x/Peter/ p'          # print every occurrence of Peter
```

**Implementation:** `sdave` is a separate `main` package in the Dave repository that imports and uses the `buffer/`, `addr/`, `cmd/`, and `regexp/` packages directly. It is not a wrapper around the Dave GUI binary. No SDL, no rendering, no pane model — just the text engine.

**Behavior note:** `sdave` consumes all input before running the script. This is inherent to the Sam command language, which treats the entire file as an addressable object. It is not suitable as a true streaming filter for very large inputs, but is correct and useful for prose, code, and typical pipeline work.

**Naming:** `sdave` — stream dave. Analogous to `ssam`.

---

- **Sam paper:** https://doc.cat-v.org/plan_9/4th_edition/papers/sam/
- **deadpixi/sam fork:** https://github.com/deadpixi/sam (modernization reference, two-button mouse handling)
- **Structural Regular Expressions paper:** https://doc.cat-v.org/bell_labs/structural_regexps/
- **go-sdl2:** https://github.com/veandco/go-sdl2
- **Nadir codebase:** _(sibling directory — confirm path at implementation time)_
