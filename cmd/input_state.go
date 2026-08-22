package cmd

// InputState tracks the accumulation state of a multi-line command input.
// The zero value is ready to receive a new command.
//
// The REPL updates this state one line at a time via Feed, rather than
// re-scanning the whole buffer. This avoids heuristics about context.
type InputState struct {
	braceDepth int  // number of unclosed { braces
	edStyle    bool // currently accumulating an ed-style text block (a/i/c followed by bare newline)
}

// Feed processes one line (without its terminating newline) and updates the
// state. It returns true when the accumulated input is a complete command and
// should be executed.
//
// The caller is responsible for appending (line + "\n") to its accumulation
// buffer before or after calling Feed.
func (st *InputState) Feed(line string) bool {
	if st.edStyle {
		// Inside an ed-style text block: a lone "." ends it.
		if line == "." {
			st.edStyle = false
		}
		// Either way, not complete until edStyle is false.
		return !st.edStyle && st.braceDepth == 0
	}

	// Scan the line for brace depth changes and ed-style triggers,
	// skipping content inside delimiters.
	i := 0
	for i < len(line) {
		ch := line[i]
		switch ch {
		case '#':
			// See parse.go isCommentStart — '#' is a comment only when
			// followed by whitespace, another '#', or end of line.
			if isCommentStart(line, i) {
				i = len(line)
			} else {
				i++
			}
		case '{':
			st.braceDepth++
			i++
		case '}':
			if st.braceDepth > 0 {
				st.braceDepth--
			}
			i++
		case '/', '?', '"':
			// Skip delimited content (pattern or text argument).
			// A literal newline inside a delimiter ends the scan
			// (trailing delimiter may be omitted).
			delim := ch
			i++
			for i < len(line) && line[i] != delim {
				if line[i] == '\\' && i+1 < len(line) {
					i++ // skip the escaped character
				}
				i++
			}
			if i < len(line) {
				i++ // consume closing delimiter
			}
		default:
			// Check for ed-style text: a bare 'a', 'i', or 'c' at the end
			// of the line (nothing follows on this line). We know it's a
			// command letter rather than part of a pattern because we're not
			// inside a delimiter right now.
			//
			// "Bare" means: this is the last non-space character on the line.
			// We detect this by checking whether the rest of the line is empty
			// after this character.
			if (ch == 'a' || ch == 'i' || ch == 'c') && i+1 == len(line) {
				// The command letter is the last character on the line.
				// This is an ed-style text block.
				st.edStyle = true
				i++
			} else {
				i++
			}
		}
	}

	return st.braceDepth == 0 && !st.edStyle
}

// Reset clears the state back to the initial ready state.
func (st *InputState) Reset() {
	st.braceDepth = 0
	st.edStyle = false
}

// IsReady reports whether the state is in the initial (no pending input) state.
func (st *InputState) IsReady() bool {
	return st.braceDepth == 0 && !st.edStyle
}
