# Dave Command Language Reference

Dave's command language is Sam's command language. This is the complete reference.

---

## Regular Expressions

Dave uses Sam's pattern syntax, with one addition:

| Pattern | Meaning |
|---------|---------|
| `.`     | Any character except newline |
| `@`     | Any character including newline (Dave extension) |
| `\n`    | Newline |
| `^`     | Start of line (matches at start of text and after every newline) |
| `$`     | End of line (matches before every newline and at end of text) |
| `*`     | Zero or more of preceding |
| `+`     | One or more of preceding |
| `?`     | Zero or one of preceding |
| `[abc]` | Character class |
| `[^abc]`| Negated character class — never matches a newline |
| `(re)`  | Grouping / capture |

**`\n` is the only escape.** Every other `\X` is the literal character X, as in Sam: `\.` is a dot, `\*` an asterisk, `\\` a backslash — and `\d` is the letter `d`, `\w` the letter `w`, `\t` the letter `t`. Perl's shorthand classes and anchors (`\d`, `\w`, `\s`, `\b`, `\A`, …) are deliberately not available; they would silently select different text from the same pattern in Sam. Use `[0-9]`, `[[:alpha:]]` and the like.

**A negated class never matches a newline.** `[^ ]+` stops at the end of a line rather than running on to the next, as in Sam. A plain class matches a newline only if it says so: `[\n]` and `[ab\n]` do.

**Matching is leftmost-longest.** Among matches starting at the earliest position, the longest wins, so `/foo|foobar/` matches `foobar`. This is Sam's rule, not Perl's — the order of alternatives does not decide the result.

**Braces are ordinary characters.** `a{2,3}` matches the text `a{2,3}`; there is no counted repetition, as there is none in Sam. POSIX classes such as `[[:digit:]]` are kept, and the non-greedy forms are accepted but moot under leftmost-longest.

The empty pattern `//` reuses the last pattern. Use `?pat?` for backward search.

### Replacement text

In the `s` command's replacement:

| Text | Meaning |
|------|---------|
| `&`    | The entire matched string |
| `\1`–`\9` | The text matched by the *n*-th `(…)` group |
| `\n`   | A newline |
| `\X`   | The literal character X — so `\&` is an ampersand and `\\` a backslash |

`$` has no special meaning; it stands for itself.

---

## Addresses

An address identifies a substring (possibly empty) within a file. The default address is always dot (`.`).

### Simple Addresses

| Address | Meaning |
|---------|---------|
| `n`     | Line *n*; `0` is before the first line |
| `#n`    | Character offset *n*; `#0` is the start of the file |
| `/re/`  | Next match of *re* searching forward from dot |
| `?re?`  | Next match of *re* searching backward from dot |
| `"re"`  | The file whose menu line matches *re*, and its dot |
| `0`     | The null string before the first full line |
| `$`     | The null string at the end of the file |
| `.`     | Dot (the current selection) |
| `'`     | The marked position (see `k` command) |

### Compound Addresses

| Address  | Meaning |
|----------|---------|
| `a1,a2`  | From the start of *a1* to the end of *a2*. Missing *a1* means `0`; missing *a2* means `$` |
| `a1;a2`  | Like `a1,a2` but *a2* is evaluated with dot set to *a1* |
| `a1+a2`  | *a2* evaluated forward from the end of *a1* |
| `a1-a2`  | *a2* evaluated backward from the start of *a1* |
| `,`      | Shorthand for `0,$` (the whole file) |

If *a2* is a bare line or character address, a missing number defaults to `1`. The `+` may be elided when both addresses are present and unambiguous.

**Useful idioms:**

```
a1+-    the complete line containing the end of a1
a1-+    the complete line containing the start of a1
0/re/   first match in the file
.//     second following occurrence of the last pattern
.,/re/  extend dot forward to the next match
```

---

## Commands

Most commands accept an optional address prefix; if omitted, dot is used. Commands marked **\*** do not accept an address. After a command that modifies text, dot is set to the result — the new text for insertions, the empty string at the deletion point for deletions.

### Text Commands

| Command | Effect |
|---------|--------|
| `a/text/` | Append *text* after the addressed range. Set dot to inserted text. |
| `i/text/` | Insert *text* before the addressed range. Set dot to inserted text. |
| `c/text/` | Replace the addressed range with *text*. Set dot to inserted text. |
| `d`       | Delete the addressed range. Set dot to the empty string at deletion point. |
| `s/re/text/` | Substitute first match of *re* with *text*. `&` in *text* = matched string. `\1` etc. = captured groups. |
| `s/re/text/g` | Substitute all non-overlapping matches in the range. |
| `sn/re/text/` | Substitute the *n*-th match in the range. |
| `sn/re/text/g` | Substitute the *n*-th match and every match after it. |
| `m a1`    | Move the addressed range to after *a1*. Set dot. |
| `t a1`    | Copy the addressed range to after *a1*. Set dot. |

For multi-line text in `a`, `i`, `c`, the command may be followed by a newline and lines of text terminated by a line containing only `.` — same as ed.

### Structural (Looping) Commands

| Command | Effect |
|---------|--------|
| `x/re/cmd` | For each non-overlapping match of *re* in the range, set dot to the match and run *cmd*. |
| `y/re/cmd` | For each substring between matches of *re*, set dot to the substring and run *cmd*. |
| `g/re/cmd` | If the range contains a match of *re*, run *cmd* (dot unchanged). |
| `v/re/cmd` | If the range does not contain a match of *re*, run *cmd* (dot unchanged). |

`x` with an immediately following space uses `.*\n` as the pattern (match each line). So `,x g/Emacs/p` prints all lines containing `Emacs`.

Changes within an `x` loop are computed against the file's state before any changes in that command, then applied in sequence. A single `u` undoes the entire `x` command.

### File Loop Commands

| Command | Effect |
|---------|--------|
| `X/re/cmd` | For each file whose menu line matches *re*, make it current and run *cmd*. |
| `Y/re/cmd` | For each file whose menu line does not match *re*, make it current and run *cmd*. |

After an `X` or `Y` loop, the current file is whichever one was visited last (matching Sam). If you need to run a command in another file without disturbing the current selection, save it explicitly with `b` before and after.

### Display Commands

| Command | Effect |
|---------|--------|
| `p`     | Print the addressed range. Set dot. |
| `=`     | Print the line address and character address of the range. |
| `=#`    | Print just the character address of the range. |

### File Commands

| Command | Effect |
|---------|--------|
| `b` *files* | Set current file to the first named file that is already in the buffer list. Prints the file's menu line. |
| `B` *files* | Like `b`, but also loads files not yet in the buffer list. |
| `D` *files* | Delete named files from the buffer list. A modified file is refused once and dropped on the second `D`. |
| `e` *file* | Replace current file contents with the named Unix file. |
| `r` *file* | Replace the addressed range with contents of the named Unix file. |
| `w` *file* | Write the addressed range (default `0,$`) to the named Unix file. If no file given, writes to the file's associated name. |
| `f` *file* | Set the current file's associated name to *file*. Print the menu line. |
| `n`     | Print the menu (list of open files with status marks). |

Every command that takes a *file* argument expands a leading `~` or `~/` to the user's home directory (Dave extension; Sam does not do this). On Windows, home is `%USERPROFILE%` (i.e. `C:\Users\Name`), not `AppData`. `~user` is not supported.

`D` is the one destructive command that asks. Everything else is guarded by undo instead; `D` cannot be, since dropping a file discards the history that would restore it. The refusal is per round of changes, not per session: edit the file again after a refusal and the next `D` refuses again.

`b`, `B`, `D`, `e`, `f`, `n`, `q`, `u`, `X` and `Y` take no address, and an address on one is an error rather than something quietly ignored — `,D` reports the address instead of looking as though it closes every file.

### Undo

| Command | Effect |
|---------|--------|
| `u`     | Undo the last command. Repeat to undo further. |
| `un`    | Undo the last *n* commands. |
| `u0`    | Undo nothing. |
| `u-n`   | Redo *n* commands. |
| `u-`    | Redo one command. |

The count is signed and may have any number of digits. It is written with no space before it: `u5`, not `u 5`. A space ends the command, so `u 5` undoes once and then treats `5` as an address. `u` takes no address; `,u` is an error.

Running off either end of the history is silent: `u100` with three commands to undo takes back those three and stops, and a redo with nothing to redo does nothing. Neither reports anything.

**One command is one step.** An `x` loop over a hundred matches comes back with a single `u`, not `u100`, and so does a `{ }` group. A command that spans several files — an `X` loop, say — is also one step: `u` reverses it in every file it touched.

**There is one history, shared by every file.** `u` takes back the last command, whichever file it touched, and the current file does not change which step is next. (Sam is per-file here: it reads the sequence number off the current file, so an edit in another file can be unreachable until you switch to it. Dave follows the sam paper instead — "the undo command, u, undoes the last command, no matter how many files were affected".)

**A new edit discards the redo history.** After undoing, anything that changes any file throws away what you could have redone. The history is a line, not a tree.

Undo restores the file's dot, mark, name and modified bit along with its text, so `e otherfile` followed by `u` puts the buffer back exactly as it was, name included. `f newname` is undoable in its own right.

Commands that add or delete files — `b`, `B`, `D` — are not undoable, and a file dropped with `D` takes its history with it.

### Shell Commands

| Command | Effect |
|---------|--------|
| `< cmd` | Replace the addressed range with the output of the shell command. |
| `> cmd` | Send the addressed range as input to the shell command (no substitution). |
| `| cmd` | Pipe the addressed range through the shell command; replace with output. |

On Linux, shell commands run via `sh -c`. On Windows, via `pwsh -Command` (PowerShell Core) if available, otherwise `powershell.exe -Command`.

### Grouping

```
addr {
    cmd1
    cmd2
    ...
}
```

Braces group commands under a single address. Each sub-command executes with dot set to the result of the address. Commands within the braces are sequenced but changes are not visible to each other within the group — they are all computed against the pre-command file state, then applied in order.

Braces may be nested arbitrarily.

### Miscellaneous

| Command | Effect |
|---------|--------|
| `k`     | Mark dot in the current file (retrieve with `'` address). |
| `q`     | Quit. If modified files exist, prints their names and requires a second `q` to confirm. |

---

## Menu Line Format

Each file in the buffer list has a menu line of the form:

```
' filename     (modified, never written)
. filename     (current file)
- filename     (unmodified, not current)
+ filename     (one window open)
* filename     (multiple windows open)
```

The `"re"` address form matches against this menu line.

`"re"` evaluates to the matched file's dot, and everything after it in the address is evaluated in that file: `"other"3p` prints line 3 of the file matching `other`, and leaves it current. A range cannot straddle a file switch — both sides of a `,` are evaluated against the file the command started in, so `"other",$` is an error.

---

## Notes

**Parallel evaluation.** When a command produces multiple changes to a file (as in `x/re/c/text/`), all addresses are computed against the original file. Changes are then applied in order. Successive insertions at the same point are concatenated.

**Null matches.** In an `x` loop, null matches adjacent to a non-null match are skipped to prevent infinite loops (e.g., `x/a*/` on `"ab"` matches `"a"`, skips the null at position 1, matches `""` at position 2).

**Delimiter.** The `/` in commands is a delimiter — any printable non-alphanumeric character may be used instead. Trailing delimiters may be omitted. Use `\/` to include a literal delimiter inside a pattern or replacement. `\n` represents a literal newline; a literal newline may not appear inside a delimited expression.
