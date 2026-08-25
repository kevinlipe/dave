package file

import (
	"bytes"
	"io"
	"strings"
)

// Line endings are handled at the I/O boundary and nowhere else. A buffer
// always holds LF-only text — DecodeContent strips the \r on the way in — so
// every address, regexp and word motion above this layer lives in a world
// where the only line ending is '\n'. The single bit of state, File.crlf,
// exists so that a file that arrived as CRLF leaves as CRLF.
//
// Encoding is a property of the writer rather than something each write path
// remembers to do, so a new write path cannot forget it.

var crlf = []byte("\r\n")

// writer wraps w so that anything written through it carries this file's
// line endings. An LF file gets w back untouched.
func (f *File) writer(w io.Writer) io.Writer {
	if f.crlf {
		return crlfWriter{w}
	}
	return w
}

// crlfWriter turns every \n written through it into \r\n. It relies on the
// LF-only buffer invariant: a \r already in front of a \n would come out as
// \r\r\n.
type crlfWriter struct{ w io.Writer }

func (c crlfWriter) Write(p []byte) (int, error) {
	done := 0
	for {
		i := bytes.IndexByte(p[done:], '\n')
		if i < 0 {
			break
		}
		i += done
		if _, err := c.w.Write(p[done:i]); err != nil {
			return done, err
		}
		if _, err := c.w.Write(crlf); err != nil {
			return i, err // the \n itself was not consumed
		}
		done = i + 1
	}
	if _, err := c.w.Write(p[done:]); err != nil {
		return done, err
	}
	return len(p), nil // n counts input consumed, not bytes emitted
}

// detectCRLF reports whether raw uses CRLF line endings, by looking at its
// first line ending and nothing else.
//
// A file's first line ending is its line ending; text files do not change
// convention halfway down. The rule this replaced asked whether \r\n appeared
// anywhere, so a single stray CRLF line in an LF file re-encoded all of it on
// the next write — a whole-file diff from a one-line edit. Whatever we pick,
// a mixed file normalises one way or the other on write: the buffer holds no
// record of which lines had \r, and per-line endings are not worth having.
// So the rule may as well be one that is stated in a sentence and is right
// about every file that is not already broken.
func detectCRLF(raw string) bool {
	i := strings.IndexByte(raw, '\n')
	return i > 0 && raw[i-1] == '\r'
}
