package input

import (
	"testing"

	"dave/buffer"
	"dave/cmd"
	"dave/file"
	"dave/pane"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// Escape selects the run typed since the last Escape, which is what samterm
// does (main.c type(), `if(c==ESC && typeesc>=0)`): type, press Escape, and
// what you just typed is dot, ready for a command to act on. Dave used to bind
// Escape to "focus the command window" instead, so this behaviour did not exist
// anywhere; ctrl-K covers the focus job and goes both ways.
//
// What these pin is the bookkeeping, because that is the part that is easy to
// get wrong: which events end a run and which leave it alone. samterm's rule is
// flushtyping(1) — clearing typeesc — for the mouse and for ctrl-A/ctrl-E, and
// flushtyping(0) — keeping it — for every arrow and page key.

func mkRange(lo, hi int) buffer.Range { return buffer.Range{Start: lo, End: hi} }

func textEvent(s string) *sdl.TextInputEvent {
	ev := &sdl.TextInputEvent{Type: sdl.TEXTINPUT}
	copy(ev.Text[:], s)
	return ev
}

func keyEvent(sym sdl.Keycode) *sdl.KeyboardEvent {
	return &sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Sym: sym}}
}

// typing builds a handler with one focused file pane over text, and returns a
// function that types a string into it a character at a time — one TEXTINPUT
// per rune, which is how SDL delivers it.
func typing(t *testing.T, text string) (*Handler, *file.File, func(string)) {
	t.Helper()
	f, p := mkPane(t, text, 400)
	h := NewHandler(cmd.NewSession(nil), NewCmdWindow(), fixedFace{}, render.Palette{})
	h.SetPanes([]*pane.Pane{p})
	h.activePane = 0
	h.currentFilePane = 0
	return h, f, func(s string) {
		for _, r := range s {
			h.HandleText(textEvent(string(r)))
		}
	}
}

func TestEscapeSelectsTheTypedRun(t *testing.T) {
	h, f, type_ := typing(t, "hello world")
	f.SetDot(mkRange(5, 5))
	type_(", there")

	if err := h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600); err != nil {
		t.Fatalf("escape: %v", err)
	}
	if got, want := f.Dot(), mkRange(5, 12); got != want {
		t.Errorf("dot = %v, want %v — Escape should select what was typed", got, want)
	}
	if got := f.Buffer().ReadAt(5, 7); got != ", there" {
		t.Errorf("selected %q, want %q", got, ", there")
	}
}

// A second Escape has no run to select, so it does nothing rather than
// re-selecting or collapsing dot. samterm guards on `typeesc >= 0` for the same
// reason.
func TestEscapeTwiceIsHarmless(t *testing.T) {
	h, f, type_ := typing(t, "")
	type_("abc")
	h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600)
	before := f.Dot()
	h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600)
	if f.Dot() != before {
		t.Errorf("dot moved on the second Escape: %v then %v", before, f.Dot())
	}
}

// The arrows and the page keys keep the run — samterm calls flushtyping(0) on
// them, which resets the run it sends to the host but leaves typeesc alone —
// while Home and End end it, because they do the job samterm gives ctrl-A and
// ctrl-E and samterm flushes typeesc on those two.
func TestWhichKeysEndTheTypedRun(t *testing.T) {
	for _, c := range []struct {
		key  sdl.Keycode
		ends bool
		what string
	}{
		{sdl.K_LEFT, false, "left arrow"},
		{sdl.K_RIGHT, false, "right arrow"},
		{sdl.K_UP, false, "up arrow"},
		{sdl.K_PAGEUP, false, "page up"},
		{sdl.K_HOME, true, "home"},
		{sdl.K_END, true, "end"},
	} {
		h, _, type_ := typing(t, "")
		type_("abc")
		h.HandleKey(keyEvent(c.key), 800, 600)
		// Read the run directly rather than through Escape: dave's page keys
		// move dot as well as scrolling, where samterm's only scroll, so a
		// surviving run there would still select nothing.
		if ended := h.typeEsc < 0; ended != c.ends {
			verb := map[bool]string{true: "ended", false: "kept"}[ended]
			t.Errorf("%s %s the run, want the opposite", c.what, verb)
		}
	}
}

// Any mouse button ends the run, before samterm has even looked at what the
// button meant (`if(mousep->buttons) flushtyping(1)`).
func TestMouseEndsTheTypedRun(t *testing.T) {
	h, f, type_ := typing(t, "")
	type_("abcdef")
	// Land the click away from the run's start, so a run that survived would
	// select something rather than collapsing to nothing by coincidence.
	h.HandleMouseDown(&sdl.MouseButtonEvent{Button: sdl.BUTTON_LEFT, X: 25, Y: 210},
		sdl.Rect{X: 0, Y: 0, W: 400, H: 200}, []sdl.Rect{{X: 0, Y: 200, W: 400, H: 400}})
	clicked := f.Dot()
	if clicked.Start == 0 {
		t.Fatalf("the click landed at 0, which cannot tell the two cases apart")
	}
	h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600)
	if f.Dot() != clicked {
		t.Errorf("dot = %v, want %v — the click should have ended the run", f.Dot(), clicked)
	}
}

// Backspacing past the start of the run pulls the start back with it, so
// Escape cannot select text that is no longer there — samterm does the same
// (`if(typeesc >= l->p0) typeesc = l->p0`). Without the clamp, Escape here
// would ask for a range starting after where dot now is.
func TestBackspacePullsTheRunStartBack(t *testing.T) {
	h, f, type_ := typing(t, "abc")
	f.SetDot(mkRange(3, 3))
	type_("de")   // run starts at 3
	for range 3 { // erase "ed" and then "c"
		h.HandleKey(keyEvent(sdl.K_BACKSPACE), 800, 600)
	}
	type_("XY") // the same run, which now starts at 2

	h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600)
	if got, want := f.Dot(), mkRange(2, 4); got != want {
		t.Errorf("dot = %v, want %v — the run starts where the backspacing left it", got, want)
	}
	if got := f.String(); got != "abXY" {
		t.Errorf("contents = %q, want %q", got, "abXY")
	}
}

// Typing over a selection starts a fresh run, so Escape selects the replacement
// and not the text it replaced plus everything before it. samterm flushes
// before it cuts, in the `a != l->p1` branch of type().
func TestTypingOverASelectionStartsANewRun(t *testing.T) {
	h, f, type_ := typing(t, "hello world")
	// Open a run somewhere else first, so "starts a new one" is distinguishable
	// from "there was not one open".
	f.SetDot(mkRange(0, 0))
	type_("AB") // run starts at 0; buffer is now "ABhello world"

	f.SetDot(mkRange(8, 13)) // "world"
	type_("bye")

	h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600)
	if got, want := f.Dot(), mkRange(8, 11); got != want {
		t.Errorf("dot = %v, want %v — the run restarted where the selection was", got, want)
	}
	if got := f.String(); got != "ABhello bye" {
		t.Errorf("contents = %q, want %q", got, "ABhello bye")
	}
}

// The command window types too — samterm treats it as one more Text — so
// Escape works there as well. Enter is the exception: it runs the command and
// clears the input line, which leaves no run to select.
func TestEscapeInTheCommandWindow(t *testing.T) {
	h, _, _ := typing(t, "")
	h.activePane = -1
	for _, r := range ",p" {
		h.HandleText(textEvent(string(r)))
	}
	h.HandleKey(keyEvent(sdl.K_ESCAPE), 800, 600)
	if got := h.cmdWin.Dot(); got.Start == got.End {
		t.Errorf("cmdwin dot = %v, want the typed run selected", got)
	}
}
