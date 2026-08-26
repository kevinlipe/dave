# Where Dave Differs from Sam

Dave's command language is Sam's, and the intent is that a command means the
same thing in both. Where it does not, it is recorded here.

## Which Sam?

There is no single canonical Sam. Rob Pike's paper describes the language;
plan9port and 9front are two live implementations of it that have drifted apart
in small ways, and neither is the authority the other answers to.

Dave follows the paper where it speaks, the behavior both trees share where it
does not, and makes a recorded call where the two disagree. In practice that has
meant siding with plan9port more often than not — partly because it is the tree
that runs on the machines Dave is developed on, and partly because 9front's
additions are mostly for its own terminal rather than for the language. Every
place the two differ and Dave had to choose is listed below.

Everything on this page is a decision, not a backlog. Divergences still open are
in `TODO.md`.

---

## Deliberate departures

Two places where Dave does something else on purpose.

### Undo runs on one timeline, not per file

Sam's undo is per file. `undo()` reads the sequence number off the *current*
file and steps only the files sharing it (`sam/sam.c:451`), so editing `a`,
editing `b`, and then making `a` current undoes `a`'s change and leaves `b`'s —
and once `a`'s history is spent, `u` does nothing at all even though `b`'s is
not.

Dave keeps one timeline across every file. `u` takes back the last command,
whichever file it touched, and the current file does not change which step is
next. This follows the Sam paper's own description of the command — "the undo
command, `u`, undoes the last command, no matter how many files were affected" —
against Sam's code, which reads more narrowly than the prose.

The grouping that makes a multi-file command a single step is the history entry
itself, so `X/\.go/ ,x/foo/d` across forty files is still one `u`.

**Consequence:** redo truncation is global too. A new edit in any file discards
every pending redo. Keeping truncation per file on a shared timeline would let a
redo replay on top of a newer edit, and the point of truncating at all is that
the history is a line rather than a tree.

### A newline ends a typing run

A run of ordinary typing coalesces into one undo step in both editors. Samterm
batches a run into one message and flushes it on any non-typing key
(`samterm/main.c` `flushtyping`); a newline is not one of them, so Sam takes back
a whole paragraph in a single `u`.

Dave breaks the run at a newline. For prose a paragraph is the unit worth
keeping, and one `u` per line is the granularity most people expect from an
editor they are writing in.

Everything else about the rule matches Sam: a cursor move, a deletion, a paste,
a command, or a step through the history all end the run.

---

## Not reproduced

Sam behaviors Dave declines to copy.

### `f` does not mark the file modified

Sam's `getname` calls `state(f, Dirty)` when the new name differs from the old,
so renaming a file marks it as having unsaved changes — reasonably, since the
buffer no longer corresponds to anything on disk under that name.

Dave changes the name and leaves the modify bit alone. The rename is undoable
either way; only the dirtying is missing, and it is not worth the churn.

### `$%` and `$%dot` are not exported

Sam's `updateenv` puts the file name in `$%` and dot in `$%dot` before
running a shell command, so `<` or `|` can address the file it was invoked
from. plan9port dropped it, and it does not survive the trip to a POSIX
shell anyway — `$%` is not a name `sh` can expand. Dave does not set them.

### `^` and `_`

9front adds two commands that run a shell command and load its output into the
*command* buffer rather than the file: `^` with no address, `_` piping dot
through it. plan9port has neither, and neither appears in the paper.

They are shaped around samterm's command window being a file like any other,
which is not how Dave models it. Dave's command window is a view, not an entry
in the buffer list, so there is nothing for `^` to write into that `<` and `|`
do not already reach.

### `M`

Another 9front command, sending a menu request to samterm. Dave has no
sam/samterm split for it to talk across.

### The append-only check

Sam refuses to write a file that is append-only and not empty. It reads the bit
off the directory entry, where Plan 9 keeps it as `DMAPPEND` — a file mode with
no portable equivalent. plan9port's `statfile` stubs it out and reports 0 for
every file, so the Sam that runs here cannot raise it either. Linux's nearest
relative is the `chattr +a` attribute behind an ioctl, and Windows has nothing.

### `u` past 32767

Sam stores a command's count in a `short` (`sam/parse.h`), so `u32768` wraps
negative and redoes instead of undoing. That is an artifact of the C, not a
behavior, and Dave saturates the count instead.

---

## The window

Sam is two programs: `sam` holds the files and runs the command language, and
`samterm` draws them and reads the keyboard and mouse, the two talking over a
protocol that can run across a network. Dave is one program with one window.
Almost everything on this page follows from that, or from Dave being a 2020s
application on a desktop that already has a window manager and a clipboard.

None of it is open work. It is what "a more keyboard-friendly interface" in the
README actually means, written out.

### Panes, not a window system

Samterm is a tiling window manager inside its own frame. Button 3 offers `new`,
`zerox`, `resize`, `close` and `write` above the list of open files, windows are
created and torn down freely, and `zerox` makes a *second* window onto the same
file — which is why samterm has a key to cycle between a file's windows, and why
the menu line's second column marks a file as having one.

Dave has a command window at the top and at most two file panes below it,
separated by borders you can drag. The buffer menu offers `new`, `pane` (or
`kill pane` when the second one is open), `close` and `write`. There is no
`zerox`: a file is shown in at most one pane at a time. The `+` column still
means what it means in Sam — this file is open in a pane — and `X`, `Y` and the
`"pat"` address still match against it.

### Keys

Every key Samterm binds, and what Dave does with it. The pattern is that
Samterm's arrows and Home/End move the *window* and Dave's move *dot*.

| Key | Samterm | Dave |
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
| Tab | A tab, or spaces to the next stop under `-i` | A tab |
| Enter | A newline, plus the indent of the line above under `-a` | A newline |

Escape is worth spelling out, because it is the one binding that carries real
editing behaviour rather than navigation. Type something, press Escape, and what
you typed becomes dot — ready for a command to act on. The run reaches back over
as many keystrokes as it takes; the arrows and the page keys leave it alone, and
a click, Home, End, or a second Escape ends it. Dave used to bind Escape to
"focus the command window" and had nothing like this; Ctrl-K does the focus job
better, since it goes both ways.

Ctrl-U and Ctrl-W follow Sam's rules, including the two that are easy to miss.
A cursor sitting immediately after a newline deletes just that newline, joining
the line to the one above; and otherwise neither scan crosses a newline, so
Ctrl-W at the start of an indented line stops at the indent. What counts as a
word is Dave's own, though: Sam's `alnum()` is everything except the control
characters, `0x7F`–`0xA0` and ASCII punctuation, so `±` and `«` are word
characters to it, while Dave uses the same letters-digits-underscore rule that
Ctrl-arrow motion and double-click already use.

Autoindent and tabs-as-spaces are 9front's `-a` and `-i` flags to samterm, and
they are not implemented at all. Dave's `tab_width` affects display only: a
typed tab is a tab in the buffer.

### Mouse

Button 1 selects and drags, and double-clicking selects a bracketed range or a
word, in both. The delimiters differ a little: Sam matches `{[(<«`, `'`, `"`,
`` ` `` and also `\n`, so double-clicking at a line boundary selects the line.
Dave matches the same brackets and quotes but not `«»` and not the newline, so
it has no select-the-line gesture.

Button 3 is the file list in both, with the window commands above it. Button 2
is the edit menu, and the entries differ:

| Samterm | Dave |
|---------|------|
| cut, paste, snarf | cut, paste, snarf |
| look | look |
| `send` in the command window, otherwise the last search pattern | send |
| plumb | — (Dave has no plumber) |
| `<rio>` | — (the system clipboard is already shared) |
| — | undo |

Shift with button 3 also opens the edit menu, for a two-button mouse.

The chords — button 1 held, then 2 to cut and 3 to paste — are 9front's. They
exist in plan9port's source but are compiled out, with the comment `it causes
deadlocks`, so the Sam that runs on the platforms Dave targets does not have
them either. Dave follows plan9port here and has no chords: the button-2 menu
and the keyboard both reach cut and paste, and a gesture that depends on
holding one button while clicking another is worth less than it costs.

### The snarf buffer is the system clipboard

Sam keeps a snarf buffer of its own and moves it between its two halves with
`Tsnarf`, `Tcut` and `Tpaste`; plan9port adds a `<rio>` menu entry to pull in
the host's selection, and binds Cmd-X, Cmd-C and Cmd-V on the Mac. Dave cuts and
pastes through the system clipboard directly, so text moves between Dave and
everything else on the desktop with no import step.

### The command window is a view, not a file

In Sam the command window is a file in the buffer list like any other. That is
what `^` and `_` are shaped around, and it is why the command window is
undoable-looking but not undone by `u`. Dave's command window is a view over a
buffer that is not in the file list. `u` still undoes the last *command*, never
the text of the command line — the same rule as Sam, reached a different way.

### Word wrap, margins, and a config file

Sam has neither soft wrap nor a config file. Long lines run off the right edge,
the tab stop comes from `$tabstop`, and everything else is a compile-time
constant or a command-line flag.

Dave wraps on word boundaries by default, centres the text column when the
window is wider than the margin, and reads `~/.config/dave/config` for the font,
the colours, the margin, the tab width and the height of the command window.
This is the reason Dave exists rather than a difference forced by the port.

### No rescue file

Sam writes `$HOME/sam.save` when it dies with modified buffers — on SIGHUP, on
an internal panic, on a read error, and on losing samterm — as a shell script
that feeds each buffer back through `samsave`. Dave does not, deliberately.

The mechanism is built for a two-process editor on a network, where losing the
terminal is routine and the editor half survives to write the file. Dave is one
process on one machine: the cases left are a crash and a signal, and a rescue
file for those means a second copy of every buffer that has to be found,
trusted, and reconciled by hand — with `samsave` prompting per file, since it
cannot know whether the file changed underneath it in the meantime. `samsave`
itself is a plan9port shell script that Dave has no equivalent of.

What this does mean is that closing the window discards modified files, the same
as any editor without a crash-recovery story. `q` warns; the close button does
not.

---

## Regular expressions

Dave's patterns follow Sam's syntax. `\n` is the only escape, every other `\X`
is the literal character X, braces are ordinary characters, and matching is
leftmost-longest — see `command-ref.md` for the full rules.

Beyond that, Dave accepts constructs Sam has no syntax for at all. Sam cannot
disagree about what it cannot write, so these cost nothing at the boundary: a
pattern that works in Sam works here and means the same thing.

| Construct | Meaning | Notes |
|-----------|---------|-------|
| `@` | Any character, newline included | Dave's own addition; Sam treats `@` as an ordinary character. Inside a class, `[a@]`, it is the character itself |
| `[[:alpha:]]`, `[[:digit:]]`, … | POSIX character classes | |
| `(?:re)` | Non-capturing group | |
| `(?i)re` | Flag groups — case-insensitive and friends | |
| `re*?`, `re+?` | Non-greedy repetition | Accepted, but leftmost-longest makes it moot: `f.*?r` on `foo bar` matches the whole of `foo bar` |

Note what is **not** here. Perl's shorthand classes and anchors — `\d`, `\w`,
`\s`, `\b`, `\A` — are gone, along with `\t`, `\x41`, `\p{Lu}` and `\Q…\E`. In
Sam every one of those is the literal letter after the backslash, and accepting
Go's reading would mean the same pattern quietly selecting different text in the
two editors. Counted repetition is gone for the same reason, with the added
argument that `{2,3}` puts a second meaning on braces in a language whose
addresses already read `0,$`.

---

## Minor

Differences small enough that nothing appears to depend on them, recorded so
they are not mistaken for bugs.

**`=` prints the name, not the path.** 9front's `printposn` prefixes the
working directory whenever the file name is relative, so every position reads as
a full path: `/home/you/src/dave/main.go:#0,#3`. Dave prints the name the file
carries — `main.go:#0,#3`. The prefix earns its keep where a position is going to
be plumbed to another program; at a prompt it is mostly a long line to read past,
and `=` is used far more often for the second than the first. The two Sams do not
agree here either: plan9port prints no name at all.

**`$` at end of file.** Sam's forward `EOL` matches only immediately before a
real newline, so `/c$/` does not match the last line of a file that does not end
in one. Dave's `$` matches there. Go cannot express Sam's rule, and Dave's
answer is the more useful one for a file whose final line is unterminated.

**Dot after `;`.** Sam leaves `f->dot` set to the left side of a `;` after
evaluating the address; Dave restores it. Every command that uses an address
sets dot itself afterwards, so the difference is not observable from the command
language.
