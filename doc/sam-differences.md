# Where Dave Differs from Sam

Dave's command language is Sam's, and the intent is that a command means the
same thing in both. Where it does not, it is recorded here.

The reference implementation is 9front's `sam` (`sys/src/cmd/sam`). Where 9front
and plan9port differ, 9front wins; the only place that matters for Dave is
`u_cmd`, and Dave follows 9front there.

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

Two Sam behaviors Dave declines to copy.

### `f` does not mark the file modified

Sam's `getname` calls `state(f, Dirty)` when the new name differs from the old,
so renaming a file marks it as having unsaved changes — reasonably, since the
buffer no longer corresponds to anything on disk under that name.

Dave changes the name and leaves the modify bit alone. The rename is undoable
either way; only the dirtying is missing, and it is not worth the churn.

### `u` past 32767

Sam stores a command's count in a `short` (`sam/parse.h`), so `u32768` wraps
negative and redoes instead of undoing. That is an artifact of the C, not a
behavior, and Dave saturates the count instead.

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

Two differences small enough that nothing appears to depend on them, recorded so
they are not mistaken for bugs.

**`$` at end of file.** Sam's forward `EOL` matches only immediately before a
real newline, so `/c$/` does not match the last line of a file that does not end
in one. Dave's `$` matches there. Go cannot express Sam's rule, and Dave's
answer is the more useful one for a file whose final line is unterminated.

**Dot after `;`.** Sam leaves `f->dot` set to the left side of a `;` after
evaluating the address; Dave restores it. Every command that uses an address
sets dot itself afterwards, so the difference is not observable from the command
language.
