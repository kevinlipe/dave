# Dave Command Language Reference

Dave's command language is Sam's command language. This is the complete reference.

---

## Regular Expressions

Dave uses Go's `regexp` package with the following extensions:

| Pattern | Meaning |
|---------|---------|
| `.`     | Any character except newline |
| `@`     | Any character including newline (Dave extension) |
| `\n`    | Literal newline |
| `*`     | Zero or more of preceding |
| `+`     | One or more of preceding |
| `?`     | Zero or one of preceding |
| `[abc]` | Character class |
| `[^abc]`| Negated character class |
| `(re)`  | Grouping / capture |
| `\1`    | Backreference to first captured group (in `s` command) |
| `&`     | In replacement text: the entire matched string |

The empty pattern `//' reuses the last pattern. Searching backward reverses the direction of `+`, `*` etc. and reverses concatenation order conceptually — in practice, use `?pat?` for backward search.

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
| `"re"`  | File whose menu line matches *re* (used to address another file) |
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
| `s2/re/text/` | Substitute the *n*-th match in the range. |
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

### Display Commands

| Command | Effect |
|---------|--------|
| `p`     | Print the addressed range. Set dot. |
| `=`     | Print the line address and character address of the range. |
| `=#`    | Print just the character address of the range. |

### File Commands

| Command | Effect |
|---------|--------|
| `b` *files* | Set current file to the first named file that is already in the buffer list. |
| `B` *files* | Like `b`, but also loads files not yet in the buffer list. |
| `D` *files* | Delete named files from the buffer list. Modified files require confirmation. |
| `e` *file* | Replace current file contents with the named Unix file. |
| `r` *file* | Replace the addressed range with contents of the named Unix file. |
| `w` *file* | Write the addressed range (default `0,$`) to the named Unix file. If no file given, writes to the file's associated name. |
| `f` *file* | Set the current file's associated name to *file*. Print the menu line. |
| `n`     | Print the menu (list of open files with status marks). |

### Undo

| Command | Effect |
|---------|--------|
| `u`     | Undo the last change. Repeat to undo further. |
| `u n`   | Undo the last *n* changes. |

Undo works across multi-file commands: a single `u` reverses the entire command regardless of how many files or changes were involved.

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

---

## Notes

**Parallel evaluation.** When a command produces multiple changes to a file (as in `x/re/c/text/`), all addresses are computed against the original file. Changes are then applied in order. Successive insertions at the same point are concatenated.

**Null matches.** In an `x` loop, null matches adjacent to a non-null match are skipped to prevent infinite loops (e.g., `x/a*/` on `"ab"` matches `"a"`, skips the null at position 1, matches `""` at position 2).

**Delimiter.** The `/` in commands is a delimiter — any printable non-alphanumeric character may be used instead. Trailing delimiters may be omitted. Use `\/` to include a literal delimiter inside a pattern or replacement. `\n` represents a literal newline; a literal newline may not appear inside a delimited expression.
