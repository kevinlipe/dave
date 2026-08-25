# Differential cases

Each case is a pair: `<name>.in` is the file the editor opens, `<name>.sam` is
the script fed to it on standard input. The harness (`cmd/differential_test.go`)
runs both through plan9port's `sam -d` and through dave's engine, then compares
the file each one wrote and their normalised output.

Adding a case is writing the two files. The harness appends `w`, `q` and `q`
itself, so a script should not.

Two rules for what belongs here:

**Only commands both editors have.** A case using a dave extension — `@`, a
POSIX character class, `(?i)` — fails on sam's side and says nothing about
conformance. `doc/sam-differences.md` lists what is dave's alone.

**Prefer `=#` over `=`.** plan9port's `printposn` prints a line address as
`l1,l2; #p1,#p2` while 9front prints lines only, so a bare `=` diffs on a
difference between the two sams rather than on anything dave did. `=#` agrees.

A failure is a candidate, not a verdict: the oracle is plan9port and dave's
reference is 9front. Settle it against `~/src/9front/sys/src/cmd/sam` before
changing dave.
