package cmd

import (
	"strings"
	"unicode/utf8"
)

// unescapeText converts escape sequences in command text (a/i/c/s replacement).
// \n → newline, \\ → backslash. Other backslash sequences are passed through.
func unescapeText(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func runeToByteOff(s string, runeN int) int {
	off := 0
	for i := 0; i < runeN && off < len(s); i++ {
		_, sz := utf8.DecodeRuneInString(s[off:])
		off += sz
	}
	return off
}

// runeCounter converts byte offsets into a string to codepoint offsets.
//
// Structural commands need this for every match they find, and the matches
// arrive in increasing order. Counting from the start each time is O(M*N)
// for M matches in N bytes -- on a file with thousands of matches that is
// the dominant cost of the whole command. Counting forward from the previous
// answer instead makes the whole sequence one pass over the text.
type runeCounter struct {
	s     string
	byteP int // byte offset of the last answer
	runeP int // codepoint offset of the last answer
}

// at returns the codepoint offset of bytePos. Callers are expected to move
// forward; going backwards is still correct, just not incremental.
func (c *runeCounter) at(bytePos int) int {
	if bytePos < c.byteP {
		c.byteP, c.runeP = 0, 0
	}
	c.runeP += utf8.RuneCountInString(c.s[c.byteP:bytePos])
	c.byteP = bytePos
	return c.runeP
}
