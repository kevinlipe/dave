# Where dave Differs from sam

dave's command language is sam's, and the intent is that a command means the
same thing in both. Where it does not, it is recorded here.

## Which sam?

There is no single canonical sam. Rob Pike's paper describes the language;
plan9port and 9front are two live implementations of it that have drifted apart
in small ways, and neither is the authority the other answers to.

dave follows the paper where it speaks, the behavior both trees share where it
does not, and makes a recorded call where the two disagree. In practice that has
meant siding with plan9port more often than not — partly because it is the tree
that runs on the machines dave is developed on, and partly because 9front's
additions are mostly for its own terminal rather than for the language. Every
place the two differ and dave had to choose is listed below.

Everything on this page is a decision, not a backlog. Divergences still open are
in `TODO.md`.

---

## Deliberate departures

Two places where dave does something else on purpose.

### Undo runs on one timeline, not per file

sam's undo is per file. `undo()` reads the sequence number off the *current*
file and steps only the files sharing it (`sam/sam.c:451`), so editing `a`,
editing `b`, and then making `a` current undoes `a`'s change and leaves `b`'s —
and once `a`'s history is spent, `u` does nothing at all even though `b`'s is
not.

dave keeps one timeline across every file. `u` takes back the last command,
whichever file it touched, and the current file does not change which step is
next. This follows the sam paper's own description of the command — "the undo
command, `u`, undoes the last command, no matter how many files were affected" —
against sam's code, which reads more narrowly than the prose.

The grouping that makes a multi-file command a single step is the history entry
itself, so `X/\.go/ ,x/foo/d` across forty files is still one `u`.

**Consequence:** redo truncation is global too. A new edit in any file discards
every pending redo. Keeping truncation per file on a shared timeline would let a
redo replay on top of a newer edit, and the point of truncating at all is that
the history is a line rather than a tree.

### A newline ends a typing run

A run of ordinary typing coalesces into one undo step in both editors. Samterm
batches a run into one message and flushes it on any non-typing key
(`samterm/main.c` `flushtyping`); a newline is not one of them, so sam takes back
a whole paragraph in a single `u`.

dave breaks the run at a newline. For prose a paragraph is the unit worth
keeping, and one `u` per line is the granularity most people expect from an
editor they are writing in.

Everything else about the rule matches sam: a cursor move, a deletion, a paste,
a command, or a step through the history all end the run.

---

## Not reproduced

sam behaviors dave declines to copy.

### `f` does not mark the file modified

sam's `getname` calls `state(f, Dirty)` when the new name differs from the old,
so renaming a file marks it as having unsaved changes — reasonably, since the
buffer no longer corresponds to anything on disk under that name.

dave changes the name and leaves the modify bit alone. The rename is undoable
either way; only the dirtying is missing, and it is not worth the churn.

### `$%` and `$%dot` are not exported

sam's `updateenv` puts the file name in `$%` and dot in `$%dot` before
running a shell command, so `<` or `|` can address the file it was invoked
from. plan9port dropped it, and it does not survive the trip to a POSIX
shell anyway — `$%` is not a name `sh` can expand. dave does not set them.

### `^` and `_`

9front adds two commands that run a shell command and load its output into the
*command* buffer rather than the file: `^` with no address, `_` piping dot
through it. plan9port has neither, and neither appears in the paper.

They are shaped around samterm's command window being a file like any other,
which is not how dave models it. dave's command window is a view, not an entry
in the buffer list, so there is nothing for `^` to write into that `<` and `|`
do not already reach.

### `M`

Another 9front command, sending a menu request to samterm. dave has no
sam/samterm split for it to talk across.

### The append-only check

sam refuses to write a file that is append-only and not empty. It reads the bit
off the directory entry, where Plan 9 keeps it as `DMAPPEND` — a file mode with
no portable equivalent. plan9port's `statfile` stubs it out and reports 0 for
every file, so the sam that runs here cannot raise it either. Linux's nearest
relative is the `chattr +a` attribute behind an ioctl, and Windows has nothing.

### `u` past 32767

sam stores a command's count in a `short` (`sam/parse.h`), so `u32768` wraps
negative and redoes instead of undoing. That is an artifact of the C, not a
behavior, and dave saturates the count instead.

---

## The window

sam is two programs: `sam` holds the files and runs the command language, and
`samterm` draws them and reads the keyboard and mouse, the two talking over a
protocol that can run across a network. dave is one program with one window.
Almost everything on this page follows from that, or from dave being a 2020s
application on a desktop that already has a window manager and a clipboard.

None of it is open work. It is what "a more keyboard-friendly interface" in the
README actually means, written out.

### Panes, not a window system

Samterm is a tiling window manager inside its own frame. Button 3 offers `new`,
`zerox`, `resize`, `close` and `write` above the list of open files, windows are
created and torn down freely, and `zerox` makes a *second* window onto the same
file — which is why samterm has a key to cycle between a file's windows, and why
the menu line's second column marks a file as having one.

dave has a command window at the top and at most two file panes below it,
separated by borders you can drag. The buffer menu offers `new`, `pane` (or
`kill pane` when the second one is open), `close` and `write`. There is no
`zerox`: a file is shown in at most one pane at a time. The `+` column still
means what it means in sam — this file is open in a pane — and `X`, `Y` and the
`"pat"` address still match against it.

### Keys

Every key Samterm binds, and what dave does with it. The pattern is that
Samterm's arrows and Home/End move the *window* and dave's move *dot*.

| Key | Samterm | dave |
|-----|---------|------|
| ↑ / ↓ | Scroll a third of a screen | Move dot one displayed line |
| Shift-↑ | Scroll one line | Extend the selection |
| PageUp / PageDown | Scroll a screen | Scroll a screen and put dot at the top of it |
| Home / End | Scroll to the start / end of the file | Move dot to the start / end of the line; Ctrl for the file |
| ← / → | Move dot one character | Move dot one character; Shift extends, Ctrl moves by word |
| Ctrl-A / Ctrl-E | Move dot to the start / end of the line | — (Home and End) |
| Ctrl-U / Ctrl-W | Delete to the line start / delete the word back | The same |
| Ctrl-B | Jump to the command window | — (Ctrl-K) |
| Ctrl-G | Cycle a file's windows | — (a file has one pane) |
| Ctrl-K | — | Cycle focus: command window → pane → pane |
| Escape | Select the run just typed | The same |
| Tab | A tab, or spaces to the next stop under `-i` | A tab, always |
| Enter | A newline, plus the indent of the line above under `-a` | A newline, always |

Escape is worth spelling out, because it is the one binding that carries real
editing behaviour rather than navigation. Type something, press Escape, and what
you typed becomes dot — ready for a command to act on. The run reaches back over
as many keystrokes as it takes; the arrows and the page keys leave it alone, and
a click, Home, End, or a second Escape ends it. dave used to bind Escape to
"focus the command window" and had nothing like this; Ctrl-K does the focus job
better, since it goes both ways.

Ctrl-U and Ctrl-W follow sam's rules, including the two that are easy to miss.
A cursor sitting immediately after a newline deletes just that newline, joining
the line to the one above; and otherwise neither scan crosses a newline, so
Ctrl-W at the start of an indented line stops at the indent. What counts as a
word is dave's own, though: sam's `alnum()` is everything except the control
characters, `0x7F`–`0xA0` and ASCII punctuation, so `±` and `«` are word
characters to it, while dave uses the same letters-digits-underscore rule that
Ctrl-arrow motion and double-click already use.

### Tab stops, and no autoindent

Three decisions, grouped because they are the same question asked three ways:
what should dave do about indentation? The answer is nothing beyond showing it.

**`tab_width` is 4, and sam's is 8.** Both sam trees set `maxtab = 8` and let
`$tabstop` override it; dave defaults to 4 and takes `tab_width` from the config
file. This is a choice and not an oversight — 4 reads better in the wrapped,
centred column dave is built around, where 8 columns of indent eats a
noticeable share of a 80-character measure. Set `tab_width = 8` if you want
sam's. The stop is elastic in both: a tab advances to the next multiple rather
than emitting a fixed run of spaces. sam sizes it as `maxtab` times the width of
`0` and dave as `tab_width` space widths, which is the same thing in a monospace
font and differs in a proportional one — which dave supports and sam does not.

**No autoindent.** 9front's samterm takes `-a`, which copies the leading blanks
of the current line after every typed newline (and not in the command window).
plan9port has no such flag. dave does not do it: Enter starts a line at column
zero.

**No tabs-as-spaces.** 9front also takes `-i`, which turns a typed tab into
spaces up to the next stop. plan9port has no such flag either. A typed tab in
dave is a tab in the buffer, and the only thing `tab_width` changes is how wide
it looks.

The two flags are 9front's alone, so declining them leaves dave matching the
tree it actually runs against. They are also editor-for-code features in an
editor whose distinguishing work is prose, and 9front's `-i` in particular
computes its column by counting characters from the line start rather than
display columns, so it lands wrong on any line already indented with tabs. If
either arrives later it will be a config key, since dave has a config file and
sam does not — but that is not planned.

### Mouse

Button 1 selects and drags, and double-clicking selects a bracketed range or a
word, in both. The delimiters differ a little: sam matches `{[(<«`, `'`, `"`,
`` ` `` and also `\n`, so double-clicking at a line boundary selects the line.
dave matches the same brackets and quotes but not `«»` and not the newline, so
it has no select-the-line gesture.

Button 3 is the file list in both, with the window commands above it. Button 2
is the edit menu, and the entries differ:

| Samterm | dave |
|---------|------|
| cut, paste, snarf | cut, paste, snarf |
| look | look |
| `send` in the command window, otherwise the last search pattern | send |
| plumb | — (dave has no plumber) |
| `<rio>` | — (the system clipboard is already shared) |
| — | undo |

Shift with button 3 also opens the edit menu, for a two-button mouse.

The chords — button 1 held, then 2 to cut and 3 to paste — are 9front's. They
exist in plan9port's source but are compiled out, with the comment `it causes
deadlocks`, so the sam that runs on the platforms dave targets does not have
them either. dave follows plan9port here and has no chords: the button-2 menu
and the keyboard both reach cut and paste, and a gesture that depends on
holding one button while clicking another is worth less than it costs.

### The snarf buffer is the system clipboard

sam keeps a snarf buffer of its own and moves it between its two halves with
`Tsnarf`, `Tcut` and `Tpaste`; plan9port adds a `<rio>` menu entry to pull in
the host's selection, and binds Cmd-X, Cmd-C and Cmd-V on the Mac. dave cuts and
pastes through the system clipboard directly, so text moves between dave and
everything else on the desktop with no import step.

### The command window is a view, not a file

In sam the command window is a file in the buffer list like any other. That is
what `^` and `_` are shaped around, and it is why the command window is
undoable-looking but not undone by `u`. dave's command window is a view over a
buffer that is not in the file list. `u` still undoes the last *command*, never
the text of the command line — the same rule as sam, reached a different way.

### Word wrap, margins, and a config file

sam has neither soft wrap nor a config file. Long lines run off the right edge,
the tab stop comes from `$tabstop`, and everything else is a compile-time
constant or a command-line flag.

dave wraps on word boundaries by default, centres the text column when the
window is wider than the margin, and reads `~/.config/dave/config` for the font,
the colours, the margin, the tab width and the height of the command window.
This is the reason dave exists rather than a difference forced by the port.

### No rescue file

sam writes `$HOME/sam.save` when it dies with modified buffers — on SIGHUP, on
an internal panic, on a read error, and on losing samterm — as a shell script
that feeds each buffer back through `samsave`. dave does not, deliberately.

The mechanism is built for a two-process editor on a network, where losing the
terminal is routine and the editor half survives to write the file. dave is one
process on one machine: the cases left are a crash and a signal, and a rescue
file for those means a second copy of every buffer that has to be found,
trusted, and reconciled by hand — with `samsave` prompting per file, since it
cannot know whether the file changed underneath it in the meantime. `samsave`
itself is a plan9port shell script that dave has no equivalent of.

What this does mean is that closing the window discards modified files, the same
as any editor without a crash-recovery story. `q` warns; the close button does
not.

---

## Regular expressions

dave runs sam's matcher, ported from `sam/regexp.c` — the same instruction set,
the same operator-precedence compiler, and the same two machines, one compiled
forward and one with the operands of every concatenation swapped so it can run
right to left. Patterns therefore mean here what they mean in sam, including
what sam makes of the things it has no syntax for: `[[:digit:]]` is a class of
six characters followed by a literal `]`, `(?i)re` is the error `no operand for
'?'`, and `re*?` is `(re*)?`. See `command-ref.md` for the rules.

There is exactly one addition:

| Construct | Meaning | Notes |
|-----------|---------|-------|
| `@` | Any character, newline included | dave's own addition; sam treats `@` as an ordinary character, so it cannot disagree about a pattern that uses it. Inside a class, `[a@]`, it is the character itself |

Two deliberate departures beyond that, both small:

**A complex pattern does not overflow.** sam's thread list is a fixed 127
entries and it reports `reg. exp. list overflow` when a pattern needs more.
dave's grows. Nobody wants that error, and the pattern that provokes it is
matching correctly right up to the point where sam gives up.

**Nine capture groups, and the rest still match.** sam records `NSUBEXP` = 9
groups plus the whole match; a pattern may contain more, and in both editors
they match and are simply not available to `\1`–`\9`.

---

## Minor

Differences small enough that nothing appears to depend on them, recorded so
they are not mistaken for bugs.

**`=` prints the name, not the path.** 9front's `printposn` prefixes the
working directory whenever the file name is relative, so every position reads as
a full path: `/home/you/src/dave/main.go:#0,#3`. dave prints the name the file
carries — `main.go:#0,#3`. The prefix earns its keep where a position is going to
be plumbed to another program; at a prompt it is mostly a long line to read past,
and `=` is used far more often for the second than the first. The two Sams do not
agree here either: plan9port prints no name at all.

**`$` at end of file.** sam's forward `EOL` matches only immediately before a
real newline, so `/c$/` does not match the last line of a file that does not end
in one. dave's `$` matches there. Go cannot express sam's rule, and dave's
answer is the more useful one for a file whose final line is unterminated.

**Dot after `;`.** sam leaves `f->dot` set to the left side of a `;` after
evaluating the address; dave restores it. Every command that uses an address
sets dot itself afterwards, so the difference is not observable from the command
language.
