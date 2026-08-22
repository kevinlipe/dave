package cmd

import (
	"io"
	"strings"
	"testing"

	"dave/buffer"
	"dave/file"
)

// Benchmarks for structural commands, where one command runs its body once
// per match and so multiplies whatever the body costs.

func benchText(lines int) string {
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString("the quick brown fox jumps over the lazy dog\n")
	}
	return sb.String()
}

func benchStructural(b *testing.B, content, cmdStr string) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s := NewSession(io.Discard)
		f := file.New()
		f.Buffer().Insert(0, content)
		f.SetDot(buffer.Range{Start: 0, End: f.Len()})
		s.AddFile(f)
		b.StartTimer()
		if err := s.ExecString(cmdStr); err != nil {
			b.Fatal(err)
		}
	}
}

// An x loop whose body contains another regexp command. Without the compile
// cache the body's pattern is recompiled once per match.
//
// The substitution has to be one that matches inside every x match, since s
// reports an error when it finds nothing.
func BenchmarkXWithSubstBody(b *testing.B) {
	benchStructural(b, benchText(400), `,x/[a-z]+/ s/[a-z]/X/`)
}

func BenchmarkXWithGuardBody(b *testing.B) {
	benchStructural(b, benchText(400), `,x/[a-z]+/ g/o/ d`)
}

// Control: the same loop with a body that compiles no regexp of its own.
func BenchmarkXPlainBody(b *testing.B) {
	benchStructural(b, benchText(400), `,x/[a-z]+/ =`)
}
