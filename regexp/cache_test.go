package regexp

import (
	"fmt"
	"testing"
)

// The compile cache must be invisible except in cost. The one way it can
// change behavior is the interaction with the "last pattern" that // reuses:
// a cache hit is a use, and if it does not update last, // starts reusing
// the wrong pattern.

func TestCacheHitReturnsSameCompiledPattern(t *testing.T) {
	ResetLastPattern()
	a, err := Compile("foo")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile("foo")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("compiling the same pattern twice returned different instances; cache is not being used")
	}
}

func TestCacheHitUpdatesLastPattern(t *testing.T) {
	ResetLastPattern()
	MustCompile("alpha")
	MustCompile("beta")
	MustCompile("alpha") // a cache hit -- must still count as the last use

	r, err := Compile("")
	if err != nil {
		t.Fatal(err)
	}
	if r.Pattern() != "alpha" {
		t.Errorf("// after re-using alpha reused %q, want \"alpha\"", r.Pattern())
	}
}

// Reuse must follow the most recent use even when every compile is a hit.
func TestEmptyPatternFollowsMostRecentUseAcrossHits(t *testing.T) {
	ResetLastPattern()
	MustCompile("one")
	MustCompile("two")
	for _, want := range []string{"one", "two", "one", "one", "two"} {
		MustCompile(want)
		r, err := Compile("")
		if err != nil {
			t.Fatal(err)
		}
		if r.Pattern() != want {
			t.Fatalf("// reused %q, want %q", r.Pattern(), want)
		}
	}
}

// A pattern that does not compile must not be cached, and must keep failing.
func TestInvalidPatternNotCached(t *testing.T) {
	ResetLastPattern()
	const bad = "a("
	for i := 0; i < 3; i++ {
		if _, err := Compile(bad); err == nil {
			t.Fatalf("Compile(%q) succeeded on attempt %d", bad, i+1)
		}
	}
	// A failed compile must not have become the last pattern either.
	if _, err := Compile(""); err == nil {
		t.Error("// reused a pattern after only failed compiles")
	}
}

// Overflowing the cache must not lose correctness, only the cached copies.
func TestCacheOverflowKeepsResultsCorrect(t *testing.T) {
	ResetLastPattern()
	clear(cache)

	// Push well past the cap so the drop-whole path runs more than once.
	for i := 0; i < maxCached*3; i++ {
		pat := fmt.Sprintf("pattern%d", i)
		r := MustCompile(pat)
		if r.Pattern() != pat {
			t.Fatalf("compiled %q, got pattern %q", pat, r.Pattern())
		}
		if lo, _ := r.FindForward(pat, 0, len(pat)); lo != 0 {
			t.Fatalf("%q should match its own text, got lo=%d", pat, lo)
		}
		if len(cache) > maxCached {
			t.Fatalf("cache grew to %d, above the %d cap", len(cache), maxCached)
		}
	}
}

// Sam extensions must survive a round trip through the cache unchanged.
func TestCachedPatternKeepsSamSemantics(t *testing.T) {
	ResetLastPattern()
	const s = "a\nb"
	for i := 0; i < 3; i++ {
		if lo, hi := MustCompile("a@b").FindForward(s, 0, len(s)); lo != 0 || hi != 3 {
			t.Fatalf("attempt %d: a@b matched [%d,%d), want [0,3)", i+1, lo, hi)
		}
		if lo, hi := MustCompile(`a\nb`).FindForward(s, 0, len(s)); lo != 0 || hi != 3 {
			t.Fatalf(`attempt %d: a\nb matched [%d,%d), want [0,3)`, i+1, lo, hi)
		}
	}
}

func BenchmarkCompileCached(b *testing.B) {
	MustCompile("[a-z]+")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Compile("[a-z]+")
	}
}

func BenchmarkCompileUncached(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(cache)
		_, _ = Compile("[a-z]+")
	}
}
