package input

import (
	"testing"

	"dave/buffer"

	"github.com/veandco/go-sdl2/sdl"
)

// ctrl-U deletes to the start of the line and ctrl-W deletes the word behind
// the cursor — samterm's ctlu and ctlw (main.c), and every Unix line
// discipline's. Dave had neither.
//
// The bounds are the whole of the behaviour and they are plain functions over a
// buffer, so they are tested here rather than through the key handler. The two
// cases that are easy to get wrong are both about newlines: a cursor sitting
// immediately after one deletes just that newline, joining the lines, and
// otherwise neither scan may cross one.

func ctrlKeyEvent(sym sdl.Keycode) *sdl.KeyboardEvent {
	return &sdl.KeyboardEvent{
		Type:   sdl.KEYDOWN,
		Keysym: sdl.Keysym{Sym: sym, Mod: sdl.KMOD_LCTRL},
	}
}

func TestLineStartBound(t *testing.T) {
	for _, c := range []struct {
		text string
		pos  int
		want int
		what string
	}{
		{"hello", 5, 0, "the whole line"},
		{"hello", 0, 0, "nothing behind the cursor"},
		{"one\ntwo", 7, 4, "back to the start of the second line, not into the first"},
		{"one\ntwo", 4, 3, "immediately after a newline deletes just the newline"},
		{"one\n", 4, 3, "at the end of a file ending in a newline, likewise"},
		{"  indented", 10, 0, "leading blanks are part of the line"},
		{"one\n\ntwo", 5, 4, "an empty line: the newline that made it"},
	} {
		buf := buffer.NewFromString(c.text)
		if got := LineStartBound(buf, c.pos); got != c.want {
			t.Errorf("%s: LineStartBound(%q, %d) = %d, want %d", c.what, c.text, c.pos, got, c.want)
		}
	}
}

func TestWordBackBound(t *testing.T) {
	for _, c := range []struct {
		text string
		pos  int
		want int
		what string
	}{
		{"hello world", 11, 6, "the word behind the cursor"},
		{"hello world ", 12, 6, "trailing blanks go with the word"},
		{"hello world", 8, 6, "from inside a word, back to its start"},
		{"hello", 0, 0, "nothing behind the cursor"},
		{"a.b", 3, 2, "punctuation is not part of the word"},
		{"foo(bar", 7, 4, "nor is a bracket"},
		{"foo   ", 6, 0, "blanks then the word"},
		// The newline rules, which are why this is not just skipWordBoundary.
		{"one\ntwo", 4, 3, "immediately after a newline deletes just the newline"},
		{"one\n   ", 7, 4, "blanks at the start of a line stop at the line start"},
		{"one\ntwo", 7, 4, "a word does not reach back across a newline"},
		{"snake_case", 10, 0, "underscore is a word character"},
		{"héllo wörld", 11, 6, "so is anything unicode calls a letter"},
	} {
		buf := buffer.NewFromString(c.text)
		if got := WordBackBound(buf, c.pos); got != c.want {
			t.Errorf("%s: WordBackBound(%q, %d) = %d, want %d", c.what, c.text, c.pos, got, c.want)
		}
	}
}

// The keys are wired up, they delete through the session so `u` takes them
// back, and they end the typed run the way any other deletion does.
func TestCtrlUAndCtrlWThroughTheHandler(t *testing.T) {
	h, f, type_ := typing(t, "")
	type_("hello world")

	h.HandleKey(ctrlKeyEvent(sdl.K_w), 800, 600)
	if got := f.String(); got != "hello " {
		t.Fatalf("after ctrl-W: %q, want %q", got, "hello ")
	}
	h.HandleKey(ctrlKeyEvent(sdl.K_u), 800, 600)
	if got := f.String(); got != "" {
		t.Fatalf("after ctrl-U: %q, want empty", got)
	}
	if got := f.Dot(); got != (buffer.Range{}) {
		t.Errorf("dot = %v, want the start of the file", got)
	}

	// One undo step each, not one per character and not folded into the typing
	// run before them.
	if err := h.session.ExecString("u"); err != nil {
		t.Fatalf("u: %v", err)
	}
	if got := f.String(); got != "hello " {
		t.Errorf("after u: %q, want the ctrl-U taken back whole", got)
	}
}

// A plain u or w is not a deletion. SDL delivers the character itself as text
// input, so the key handler must leave it alone.
func TestPlainUAndWAreNotDeletions(t *testing.T) {
	h, f, type_ := typing(t, "")
	type_("hello")
	h.HandleKey(keyEvent(sdl.K_u), 800, 600)
	h.HandleKey(keyEvent(sdl.K_w), 800, 600)
	if got := f.String(); got != "hello" {
		t.Errorf("contents = %q, want %q — a plain u or w deleted something", got, "hello")
	}
}

// Over a selection they delete the selection and stop, which is what samterm
// does with any key when dot is not null.
func TestCtrlUOverASelectionDeletesTheSelection(t *testing.T) {
	h, f, _ := typing(t, "hello world")
	f.SetDot(buffer.Range{Start: 0, End: 5})
	h.HandleKey(ctrlKeyEvent(sdl.K_u), 800, 600)
	if got := f.String(); got != " world" {
		t.Errorf("contents = %q, want %q", got, " world")
	}
}

// They work in the command window too — sam's is a Text like any other.
func TestCtrlUInTheCommandWindow(t *testing.T) {
	h, _, _ := typing(t, "")
	h.activePane = -1
	for _, r := range ",x/foo/d" {
		h.HandleText(textEvent(string(r)))
	}
	h.HandleKey(ctrlKeyEvent(sdl.K_w), 800, 600)
	if got := h.cmdWin.Buf().String(); got != ",x/foo/" {
		t.Errorf("after ctrl-W: %q, want %q", got, ",x/foo/")
	}
	h.HandleKey(ctrlKeyEvent(sdl.K_u), 800, 600)
	if got := h.cmdWin.Buf().String(); got != "" {
		t.Errorf("after ctrl-U: %q, want empty", got)
	}
}
