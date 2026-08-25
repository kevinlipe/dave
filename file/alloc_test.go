package file

import (
	"io"
	"strings"
	"testing"
)

// The write path exists in this shape so that writing a file does not cost a
// copy of the file. These guard that: the allocation count must not depend on
// how big the file is.

func bigFile(t testing.TB, lines int, crlf bool) *File {
	t.Helper()
	raw := strings.Repeat("the quick brown fox jumps over the lazy dog\n", lines)
	if crlf {
		raw = strings.ReplaceAll(raw, "\n", "\r\n")
	}
	f := New()
	f.Buffer().Insert(0, f.DecodeContent(raw))
	return f
}

func TestWriteContentDoesNotAllocatePerByte(t *testing.T) {
	for _, crlf := range []bool{false, true} {
		// Built outside the measured closure: we are timing the write, not
		// the construction.
		sm, lg := bigFile(t, 10, crlf), bigFile(t, 10000, crlf)
		small := testing.AllocsPerRun(10, func() { sm.WriteContent(io.Discard) })
		large := testing.AllocsPerRun(10, func() { lg.WriteContent(io.Discard) })
		if large != small {
			t.Errorf("crlf=%v: %v allocs for 10 lines, %v for 10000 — "+
				"the write path is allocating with the file, not streaming it",
				crlf, small, large)
		}
	}
}

func BenchmarkWriteContent(b *testing.B) {
	f := bigFile(b, 20000, false)
	b.SetBytes(int64(f.Len()))
	b.ReportAllocs()
	for b.Loop() {
		f.WriteContent(io.Discard)
	}
}

func BenchmarkWriteContentCRLF(b *testing.B) {
	f := bigFile(b, 20000, true)
	b.SetBytes(int64(f.Len()))
	b.ReportAllocs()
	for b.Loop() {
		f.WriteContent(io.Discard)
	}
}

// The shape the write path had before: flatten the whole buffer, then write
// the string. Kept as the thing to beat.
func BenchmarkWriteContentFlattenedFirst(b *testing.B) {
	f := bigFile(b, 20000, false)
	b.SetBytes(int64(f.Len()))
	b.ReportAllocs()
	for b.Loop() {
		io.WriteString(io.Discard, f.Buffer().String())
	}
}
