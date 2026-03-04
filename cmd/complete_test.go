package cmd

import "testing"

// feed simulates feeding lines one at a time and returns whether the final
// state is complete after the last line.
func feedLines(lines ...string) bool {
	var st InputState
	var complete bool
	for _, l := range lines {
		complete = st.Feed(l)
	}
	return complete
}

func TestFeedSimpleCommands(t *testing.T) {
	cases := []struct {
		lines    []string
		complete bool
	}{
		{[]string{"d"}, true},
		{[]string{"p"}, true},
		{[]string{",p"}, true},
		{[]string{"s/foo/bar/"}, true},
		{[]string{"q"}, true},
		{[]string{""}, true},
	}
	for _, c := range cases {
		got := feedLines(c.lines...)
		if got != c.complete {
			t.Errorf("feedLines(%v) = %v, want %v", c.lines, got, c.complete)
		}
	}
}

func TestFeedBraceGroup(t *testing.T) {
	// Opening brace alone is not complete.
	if feedLines("{") {
		t.Error("'{' alone should not be complete")
	}
	// Brace with content but unclosed.
	if feedLines("{", "d") {
		t.Error("'{\\nd' should not be complete")
	}
	// Closed brace completes.
	if !feedLines("{", "d", "}") {
		t.Error("'{\\nd\\n}' should be complete")
	}
	// Inline braces complete on the same line.
	if !feedLines("{ d }") {
		t.Error("'{ d }' should be complete")
	}
}

func TestFeedNestedBraces(t *testing.T) {
	if feedLines("{", "{", "}") {
		t.Error("nested: two opens, one close should not be complete")
	}
	if !feedLines("{", "{", "}", "}") {
		t.Error("nested: two opens, two closes should be complete")
	}
}

func TestFeedBracesInsideDelimiters(t *testing.T) {
	// Braces inside /.../ should not count.
	if !feedLines("s/{foo}/{bar}/") {
		t.Error("braces inside delimiters should not affect depth")
	}
	if !feedLines("/re{/ p") {
		t.Error("brace inside regexp address should not affect depth")
	}
	// Important: c/bar/ should not trigger ed-style
	if !feedLines("x/foo/ c/bar/") {
		t.Error("c/bar/ should be complete (has delimiter, not ed-style)")
	}
}

func TestFeedEdStyleText(t *testing.T) {
	// 'a' at end of line triggers ed-style.
	if feedLines("a") {
		t.Error("'a' alone should not be complete (ed-style)")
	}
	if feedLines("a", "some text") {
		t.Error("'a\\nsome text' should not be complete")
	}
	if !feedLines("a", "some text", ".") {
		t.Error("'a\\nsome text\\n.' should be complete")
	}

	// 'i' and 'c' also trigger ed-style.
	if !feedLines("i", "hello", ".") {
		t.Error("'i\\nhello\\n.' should be complete")
	}
	if !feedLines("c", "world", ".") {
		t.Error("'c\\nworld\\n.' should be complete")
	}
}

func TestFeedDelimiterFormNotEdStyle(t *testing.T) {
	// a/text/ is NOT ed-style — the 'a' is followed by '/', not end-of-line.
	if !feedLines("a/text/") {
		t.Error("a/text/ should be complete (delimiter form)")
	}
	if !feedLines("i/text/") {
		t.Error("i/text/ should be complete")
	}
	if !feedLines("c/text/") {
		t.Error("c/text/ should be complete")
	}
}

func TestFeedEdStyleAfterAddress(t *testing.T) {
	// "2a" with no delimiter IS ed-style in Sam — the text follows on
	// subsequent lines, terminated by ".".
	if feedLines("2a") {
		t.Error("'2a' should not be complete — bare 'a' after address is ed-style")
	}
	if !feedLines("2a", "some text", ".") {
		t.Error("'2a\\nsome text\\n.' should be complete")
	}

	// "2a/text/" with delimiter is NOT ed-style — complete immediately.
	if !feedLines("2a/text/") {
		t.Error("'2a/text/' should be complete (has delimiter)")
	}
}

func TestFeedComments(t *testing.T) {
	if !feedLines("# a comment") {
		t.Error("comment line should be complete")
	}
	// Comment after command.
	if !feedLines("d # delete") {
		t.Error("command with comment should be complete")
	}
}

func TestFeedStructuralWithBlock(t *testing.T) {
	// x/re/ { ... } spanning lines.
	lines := []struct {
		line     string
		complete bool
	}{
		{"x/re/ {", false},
		{"  g/foo/ p", false},
		{"  g/bar/ d", false},
		{"}", true},
	}
	var st InputState
	for _, l := range lines {
		got := st.Feed(l.line)
		if got != l.complete {
			t.Errorf("after Feed(%q): complete=%v, want %v", l.line, got, l.complete)
		}
	}
}

func TestFeedEdStyleSimulation(t *testing.T) {
	// Simulate exactly what the REPL does.
	lines := []struct {
		line     string
		complete bool
	}{
		{"a", false},
		{"first line", false},
		{"second line", false},
		{".", true},
	}
	var st InputState
	for _, l := range lines {
		got := st.Feed(l.line)
		if got != l.complete {
			t.Errorf("after Feed(%q): complete=%v, want %v", l.line, got, l.complete)
		}
	}
}

func TestFeedEdStyleInsideBraceGroup(t *testing.T) {
	lines := []struct {
		line     string
		complete bool
	}{
		{"{", false},
		{"a", false},
		{"some text", false},
		{".", false}, // edStyle ends, but braceDepth still 1
		{"}", true},
	}
	var st InputState
	for _, l := range lines {
		got := st.Feed(l.line)
		if got != l.complete {
			t.Errorf("after Feed(%q): complete=%v, want %v", l.line, got, l.complete)
		}
	}
}

func TestFeedReset(t *testing.T) {
	var st InputState
	st.Feed("{")
	if st.IsReady() {
		t.Error("should not be ready with open brace")
	}
	st.Reset()
	if !st.IsReady() {
		t.Error("should be ready after Reset")
	}
}
