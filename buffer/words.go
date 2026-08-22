package buffer

import "unicode"

// IsWordRune reports whether r is part of a word: a letter, a digit, or an
// underscore.
//
// This lives here because it is a property of text, not of layout or of
// input. It used to be defined separately in pane and in input, and the two
// drifted -- pane's was Unicode-aware and input's was ASCII-only, so an
// accented letter was part of a word when deciding where a line wrapped and
// not when deciding what a double-click selected. One definition cannot
// drift from itself.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
