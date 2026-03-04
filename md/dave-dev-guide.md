# Dave — Developer Guide

This document is for Claude Code. Read it alongside `dave-spec.md`. The spec says what to build. This document says how to build it, in what order, and what to watch out for.

---

## Prime Directive

Build the text engine first. Nothing else matters until the buffer, address language, command language, and regexp packages are correct and tested. Do not touch SDL until Phase 2. Do not render anything until Phase 2. A working headless editor is more valuable than a broken GUI editor.

---

## Build Phases

### Phase 1 — Text Engine (no SDL, no rendering)

**Goal:** `dave -d` works. You can open files, run commands, undo, and quit from the terminal. All unit tests pass.

Build in this exact order:

**1. Module and build setup**

Initialize the Go module. Confirm CGo is available. Do not write any editor code yet — just get the build working with a `main.go` that prints "dave" and exits.

```
go mod init dave
```

Confirm dependencies compile:
- `github.com/veandco/go-sdl2/sdl` (do not call any SDL functions yet — just confirm it links)
- `golang.org/x/image/font/gofont/gomono` (for the embedded font)

Add the `//go:embed` directive for Go Mono in `render/fonts.go` immediately, even though rendering comes later. Getting the embed path right early avoids a surprise later.

**2. `buffer/` — Gap buffer and transcript**

This is the most important package. Get it right before anything else.

Implement in this order:
- Basic gap buffer: `Insert(pos, text)`, `Delete(pos, n)`, `ReadAt(pos, n) string`, `Len() int`
- Sequential reader: forward and backward byte-at-a-time access for regexp scanning
- UTF-8 correctness: all positions are codepoint offsets, not byte offsets. This distinction must be enforced from the start — retrofitting it later is painful.
- Transaction log: `Mark()`, `Append(change)`, `Apply()`, `Rollback()`
- Undo: inverse transcript, `Undo()`

Write tests as you go. Do not proceed to `addr/` until all buffer tests pass. See the Testing section of the spec for the full test list.

**Known pitfall — gap buffer and UTF-8:** A gap buffer naturally operates on bytes. The UTF-8 layer must sit cleanly on top, converting all external codepoint positions to internal byte positions before any buffer operation. Keep this conversion in one place (`buffer/utf8.go` or equivalent). If it leaks into multiple places you will have bugs that are very hard to find.

**Known pitfall — transaction ordering:** Sam's transcript requires changes to be in monotonically increasing position order within a single command. This is enforced by the cache in Sam's implementation. Dave must enforce the same constraint and return an error if changes are out of order. The structural regexp commands (`x`, `y`) depend on this.

**3. `regexp/` — Regexp wrapper**

Wrap Go's `regexp` package to add Sam's extensions:
- `@` matches any character including newline (Go's `.` does not match newline)
- `\n` in a pattern matches a literal newline
- Backward search: reverse the input, run the match, reverse the result positions
- Empty pattern reuses the last compiled pattern (store as package-level state)

Write tests before moving on. The backward search is the tricky part — test it thoroughly against known inputs.

**Known pitfall — backward search:** Sam's paper describes reversing the concatenation operators and exchanging `^` and `$`. Go's regexp engine does not support this natively. The practical approach is to reverse the string being searched and run a forward match, then convert the match positions back to original-string coordinates. This works correctly for most patterns but has edge cases with `^` and `$` anchors — test these explicitly.

**4. `addr/` — Address language**

Implement the address parser and evaluator. The Sam paper's address grammar is your specification. Implement exactly what the paper describes, nothing more.

Address types to implement:
- Line number (`n`, `0`, `$`)
- Character offset (`#n`)
- Dot (`.`)
- Regexp (`/pat/`, `-/pat/`)
- Relative (`+`, `-`, `+n`, `-n`)
- Compound (`a1,a2`, `,`)
- File (`"pat"`)

The evaluator takes an `Address` struct and a `File`, returns a `Range`. All defaults (implicit `.`, implicit `+` before bare regexp) are inserted by the parser, not the evaluator. The evaluator should never need to guess about defaults.

Write tests for every address form. Use the Sam paper's examples as test cases.

**Known pitfall — line addresses are slow:** Sam notes that line addresses require scanning the file to count newlines, since newline positions are not indexed. This is acceptable. Do not add a newline index to optimize this — it adds complexity and line addresses are rare in practice.

**5. `file/` — File model**

Implement the `File` type:
- Name, modified bit, dot
- Wraps a `buffer.Buffer`
- Menu line generation
- Disk read/write (`ReadFrom`, `WriteTo`)
- Lazy loading flag

The file list (Dave's list of open files) lives here as a package-level or context-level structure. It is not global state — it should be passed explicitly to the command executor.

**6. `cmd/` — Command language**

Implement the command parser and executor. Again, the Sam paper is your specification.

Implement commands in this order, testing each before moving to the next:
1. `d`, `p`, `=` — simplest, good for verifying the address evaluator works
2. `a`, `i`, `c` — basic text insertion
3. `s` — substitute, requires regexp
4. `g`, `v` — conditional, requires regexp
5. `x`, `y` — looping, the hardest; requires careful null-match handling
6. `X`, `Y` — file looping
7. `{ }` — grouping
8. `e`, `r`, `w`, `f`, `b`, `B`, `D` — file operations
9. `u` — undo
10. `q` — quit (prints modified file names, requires second invocation to confirm)
11. `<`, `>`, `|` — shell commands (see shell section below)

**Known pitfall — `x` and null matches:** The `x` command must not generate a null match adjacent to a non-null match. After returning a match, if the next match is null and abuts the previous match, skip it and advance one character. This is described precisely in the Sam paper and must be implemented exactly. It is easy to get wrong and hard to debug through the UI.

**Known pitfall — two-pass evaluation:** The command executor must complete all address evaluation before applying any changes. This prevents earlier changes from corrupting the positions of later changes in the same command. The transcript handles this: addresses are evaluated against the original buffer contents, changes are appended to the transcript, and the transcript is applied in one pass at the end. Do not apply changes inline as you evaluate.

**7. `-d` mode REPL**

Wire up a simple read-eval-print loop in `main.go` for the `-d` flag:
- Read a line from stdin
- Pass it to the command parser
- Execute the result
- Print any output to stdout
- Repeat until `q`

This is the first time all packages work together. Expect integration bugs here — this is normal and expected.

At the end of Phase 1, `dave -d` should be a fully functional terminal text editor. Run it against real files. Edit some text. Test undo. Run structural regexp commands. Fix everything that's broken before starting Phase 2.

---

### Phase 2 — SDL Rendering

**Goal:** `dave` opens a window, displays text, accepts mouse input, and looks like the screenshots in the spec.

**Do not start Phase 2 until Phase 1 is complete and stable.**

Build in this order:

**1. `render/` — Basic SDL window and font rendering**

- Initialize SDL2 and create a window
- Load the embedded Go Mono font via SDL_ttf
- Render a string of text to the window
- Handle window resize
- Shut down cleanly on close

At this point, Dave should open a window, display "Hello, Dave." in Go Mono, and close cleanly. Nothing else.

**2. Color constants**

Define all colors from the spec as named constants in `render/colors.go`. Use the exact hex values from the spec. Do not hardcode colors anywhere else in the codebase — always reference the named constants.

**3. Command window rendering**

Render the command window at the top of the screen:
- Pale blue background (`#EAFFFF`)
- Blue-purple border (`#8888CC`)
- Fixed height (approximately 4 lines of text)
- Empty on startup
- Scrollbar on left edge in `#8888CC`

At this point Dave should open with just the command window visible and nothing below it, matching the startup screenshot.

**4. Pane rendering**

Render a single file pane below the command window:
- Yellow background (`#FFFFEA`)
- Text in black
- Scrollbar on left edge in `#99994C`
- Olivetti centering (left/right margins)
- Word wrap (soft, at word boundaries)
- Dot highlighted in `#9FEEEE`
- Pane header showing the file's menu line

**5. `input/` — Keyboard and mouse input**

Wire up SDL event handling:
- Keyboard: typing, backspace, delete, cursor movement keys
- Mouse: left click to set dot, left drag to extend selection, scroll wheel
- Double-click word selection
- Double-click bracket/paren/quote selection

**6. `menu/` — Buffer menu and editing menu**

Implement the right-click buffer menu and shift-right-click editing menu as SDL-rendered popup overlays.

Buffer menu: New, Sidebar, Kill sidebar (when open), separator, file list.
Editing menu: Cut, Paste, Snarf, Look, Send.

**7. Scrollbar interaction**

Left click in scrollbar jumps to proportional position. Left drag scrolls continuously.

**8. Sidebar**

Implement the second pane. Split the window vertically (or horizontally per config). Each pane independently tracks its file, scroll position, and dot. Right-clicking in either pane targets that pane for file selection.

**9. System font lookup (optional)**

If the user has specified a font name in config, resolve it to a file path using fontconfig (Linux) or the Windows font registry. Fall back to embedded Go Mono if resolution fails.

---

### Phase 3 — sdave

Build `sdave/main.go` as a separate binary that imports `buffer/`, `addr/`, `cmd/`, and `regexp/`. It should take about an hour given that all the underlying packages already work. Follow the `ssam` man page in the spec for flag behavior.

---

## Configuration

All configuration options, their defaults, and their meanings are specified in the Configuration section of `dave-spec.md`. Implement exactly what is listed there — do not add options that aren't in the spec, and do not change defaults without updating the spec first.

The `config/` package should be implemented as part of Phase 1 since `-d` mode needs font mode and shell settings. It does not depend on SDL and has no rendering concerns.

---

## Shell Command Implementation

For `<`, `>`, `|` commands:

```go
import "runtime"

func shellCommand() (string, []string) {
    if runtime.GOOS == "windows" {
        if config.UsePwsh {
            return "pwsh", []string{"-Command"}
        }
        return "powershell.exe", []string{"-Command",
            "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;"}
    }
    return "sh", []string{"-c"}
}
```

Resolve this once at config load time and store the result. Do not call `runtime.GOOS` on every command execution.

---

## Package Dependency Rules

Packages may only import packages below them in this list. No circular imports.

```
regexp          (no internal imports)
buffer          (no internal imports)
file            imports: buffer
addr            imports: buffer, file, regexp
cmd             imports: buffer, file, addr, regexp
config          (no internal imports)
render          imports: config
input           imports: config, file, cmd, render
menu            imports: config, file, render
pane            imports: buffer, file, render, config
main            imports: everything
sdave/main      imports: buffer, file, addr, cmd, regexp, config
```

If you find yourself wanting to import `render` from `cmd`, or `menu` from `addr`, stop and reconsider the design. The text engine must have no knowledge of the rendering layer.

---

## What "Done" Means Per Phase

### Phase 1 is done when:
- All unit tests in `buffer/`, `addr/`, `cmd/`, `regexp/` pass
- `dave -d` can open a real file, make edits using the full command language including `x`/`y`/`g`/`v`, undo those edits, write the file, and quit
- The Sam paper's structural regexp examples produce correct output when run via `dave -d`
- `dave -d` correctly refuses to quit with unsaved files and quits on second `q`

### Phase 2 is done when:
- Dave opens a window that looks like Sam's classic appearance
- Text can be typed, selected, cut, copied, pasted with the mouse
- The buffer menu and editing menu work
- Files can be opened, edited, saved, and closed
- The sidebar works
- Word wrap is on by default and matches the config setting
- The scrollbar is interactive

### Phase 3 is done when:
- `sdave 's/foo/bar/g' file.txt` works correctly
- `sdave -n ',10p' file.txt` prints the first 10 lines
- `cat file.txt | sdave 'y/[a-zA-Z]+/ c/\n/'` works in a pipeline

---

## Things Most Likely To Go Wrong

**In Phase 1:**
- UTF-8 position confusion (bytes vs codepoints) — get this right in `buffer/` and enforce it everywhere
- Null match handling in `x` — test with the Sam paper's `B*` example explicitly
- Transaction ordering — the two-pass model is easy to accidentally short-circuit
- Backward regexp search — test with anchors (`^`, `$`) and multi-line patterns

**In Phase 2:**
- Word wrap and cursor movement — visual line vs logical line is a genuine source of bugs; define a clear internal representation before implementing
- SDL event handling on Windows — key repeat rates and modifier key behavior differ from Linux; test both
- Scrollbar click-to-jump math — off-by-one errors are common; use the file length and pane height to compute the ratio carefully

**In Phase 3:**
- The `-n` flag interaction with default output — make sure the default "print everything" behavior is suppressed correctly

---

## Nadir Reference

The Nadir codebase (sibling directory — confirm path before starting) contains working Go code for:
- SDL2 initialization and main event loop
- TTF font loading and text rendering via SDL_ttf
- Plan 9 bitmap font loader and renderer
- Window creation and resize handling

Copy and adapt this code rather than importing it. Do not add Nadir as a Go module dependency. The shared surface area is small enough that duplication is the right call.

When adapting Nadir code, read it first and understand what it does before copying. Some of it may be Acme-specific in ways that need to be stripped out.

---

## Reference Material

Keep these open while building:

- **Sam paper:** https://doc.cat-v.org/plan_9/4th_edition/papers/sam/
  Read the Implementation sections carefully, not just the user-facing sections. The transcript/undo model and the null-match handling for `x` are described precisely there.
- **deadpixi/sam source:** https://github.com/deadpixi/sam
  When uncertain about a behavior, read the C source. It is the ground truth for anything the paper leaves ambiguous.
- **Dave spec:** `dave-spec.md` in this directory
- **go-sdl2:** https://github.com/veandco/go-sdl2
