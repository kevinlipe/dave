package menu

import (
	"testing"

	"dave/file"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// Everything in this package except Draw takes at most a render.Face, so the
// menu's construction, geometry and hit-testing can be exercised without a
// window. Draw is the only part that needs a live renderer and is not covered.

// fixedFace is a deterministic render.Face: every glyph is 10px wide.
type fixedFace struct{}

func (fixedFace) Height() int                                    { return 20 }
func (fixedFace) Ascent() int                                    { return 16 }
func (fixedFace) GlyphAdvance(rune) int                          { return 10 }
func (fixedFace) MeasureText(s string) int                       { return 10 * len([]rune(s)) }
func (fixedFace) DrawText(string, int32, int32, sdl.Color) error { return nil }
func (fixedFace) Close()                                         {}

var face render.Face = fixedFace{}

func labels(m *Menu) []string {
	out := make([]string, len(m.items))
	for i, it := range m.items {
		out[i] = it.Label
	}
	return out
}

func mkFile(t *testing.T, name string) *file.File {
	t.Helper()
	return file.NewNamed(name)
}

// ---- construction -----------------------------------------------------------

func TestNewEditMenu(t *testing.T) {
	m := NewEditMenu(0, 0)
	want := []string{"cut", "paste", "snarf", "look", "send", "undo"}
	got := labels(m)
	if len(got) != len(want) {
		t.Fatalf("got %d items %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
	if !m.IsOpen() {
		t.Error("a new menu should be open")
	}
}

// The second item is "pane" or "kill pane" depending on whether a second pane
// is already open, since the same slot does both jobs.
func TestNewBufferMenuPaneLabel(t *testing.T) {
	for _, c := range []struct {
		hasSecond bool
		want      string
	}{
		{false, "pane"},
		{true, "kill pane"},
	} {
		m := NewBufferMenu(nil, nil, c.hasSecond, nil, 0, 0)
		if got := labels(m)[1]; got != c.want {
			t.Errorf("hasSecondPane=%v: item 1 = %q, want %q", c.hasSecond, got, c.want)
		}
	}
}

func TestNewBufferMenuActionsThenDivider(t *testing.T) {
	m := NewBufferMenu(nil, nil, false, nil, 0, 0)
	want := []string{"new", "pane", "close", "write", "~~dave~~"}
	got := labels(m)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
	if m.items[4].Kind != ItemDave {
		t.Errorf("the ~~dave~~ divider has kind %v, want ItemDave", m.items[4].Kind)
	}
}

// The active file goes last so it sits under the cursor: a click-and-release
// with no movement re-selects the file already open, which is a no-op. Sam
// does the same.
func TestNewBufferMenuPutsActiveFileLast(t *testing.T) {
	a, b, c := mkFile(t, "a.txt"), mkFile(t, "b.txt"), mkFile(t, "c.txt")
	m := NewBufferMenu([]*file.File{a, b, c}, nil, false, b, 0, 0)

	var files []*file.File
	for _, it := range m.items {
		if it.Kind == ItemFile {
			files = append(files, it.File)
		}
	}
	if len(files) != 3 {
		t.Fatalf("got %d file items, want 3", len(files))
	}
	if files[len(files)-1] != b {
		t.Error("the active file is not last")
	}
	// And it appears exactly once.
	n := 0
	for _, f := range files {
		if f == b {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the active file appears %d times, want 1", n)
	}
}

// An activeFile that is not in the list is still shown -- it is the file in
// the pane -- but a nil one adds nothing.
func TestNewBufferMenuNilActiveFile(t *testing.T) {
	a := mkFile(t, "a.txt")
	m := NewBufferMenu([]*file.File{a}, nil, false, nil, 0, 0)
	n := 0
	for _, it := range m.items {
		if it.Kind == ItemFile {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d file items, want 1", n)
	}
}

// The menu is positioned so its last item lands on the click point.
func TestNewBufferMenuPositionsLastItemUnderCursor(t *testing.T) {
	m := NewBufferMenu(nil, nil, false, nil, 100, 500)
	last := len(m.items) - 1
	r := m.itemRect(last, face)
	if r.Y != 500 {
		t.Errorf("last item at y=%d, want 500 (the click point)", r.Y)
	}
	if m.x != 100 {
		t.Errorf("x = %d, want 100 (unchanged)", m.x)
	}
}

// ---- geometry ---------------------------------------------------------------

func TestWidthFitsTheWidestLabelAndHasAFloor(t *testing.T) {
	// Every label is short, so the 120px minimum applies.
	if got := NewEditMenu(0, 0).width(face); got != 120 {
		t.Errorf("short labels: width = %d, want the 120 floor", got)
	}
	// A long label wins: 20 runes * 10px + 2*padX.
	m := &Menu{items: []Item{{Kind: ItemAction, Label: "12345678901234567890"}}}
	if want := int32(20*10 + padX*2); m.width(face) != want {
		t.Errorf("long label: width = %d, want %d", m.width(face), want)
	}
}

// A separator is shorter than a row, and contributes its own height.
func TestHeightCountsSeparatorsSeparately(t *testing.T) {
	m := &Menu{items: []Item{
		{Kind: ItemAction, Label: "a"},
		{Kind: ItemSeparator},
		{Kind: ItemAction, Label: "b"},
	}}
	if want := int32(itemH*2 + sepH); m.height() != want {
		t.Errorf("height = %d, want %d", m.height(), want)
	}
}

func TestItemRectStacksAndAccountsForSeparators(t *testing.T) {
	m := &Menu{x: 10, y: 20, items: []Item{
		{Kind: ItemAction, Label: "a"},
		{Kind: ItemSeparator},
		{Kind: ItemAction, Label: "b"},
	}}
	for i, wantY := range []int32{20, 20 + itemH, 20 + itemH + sepH} {
		if got := m.itemRect(i, face); got.Y != wantY {
			t.Errorf("item %d at y=%d, want %d", i, got.Y, wantY)
		}
	}
	if got := m.itemRect(1, face).H; got != sepH {
		t.Errorf("separator height = %d, want %d", got, sepH)
	}
	// An out-of-range index is the zero rect, not a panic.
	if got := m.itemRect(99, face); got != (sdl.Rect{}) {
		t.Errorf("itemRect(99) = %+v, want the zero rect", got)
	}
}

func TestClampKeepsTheMenuOnScreen(t *testing.T) {
	build := func(x, y int32) *Menu {
		return &Menu{x: x, y: y, items: []Item{
			{Kind: ItemAction, Label: "a"},
			{Kind: ItemAction, Label: "b"},
		}}
	}
	const winW, winH = 400, 300
	w, h := build(0, 0).width(face), build(0, 0).height()

	m := build(390, 290) // off the right and bottom edges
	m.Clamp(winW, winH, face)
	if m.x != winW-w || m.y != winH-h {
		t.Errorf("bottom-right: got (%d,%d), want (%d,%d)", m.x, m.y, winW-w, winH-h)
	}

	m = build(-50, -50) // off the top and left
	m.Clamp(winW, winH, face)
	if m.x != 0 || m.y != 0 {
		t.Errorf("top-left: got (%d,%d), want (0,0)", m.x, m.y)
	}

	m = build(10, 10) // already fits: untouched
	m.Clamp(winW, winH, face)
	if m.x != 10 || m.y != 10 {
		t.Errorf("fitting menu moved to (%d,%d), want (10,10)", m.x, m.y)
	}
}

// A menu taller than the window clamps to the top rather than scrolling off it.
func TestClampPrefersTheTopWhenTooTall(t *testing.T) {
	items := make([]Item, 40)
	for i := range items {
		items[i] = Item{Kind: ItemAction, Label: "x"}
	}
	m := &Menu{x: 0, y: 100, items: items}
	m.Clamp(400, 300, face)
	if m.y != 0 {
		t.Errorf("y = %d, want 0", m.y)
	}
}

// ---- hit testing ------------------------------------------------------------

// The ~~dave~~ divider and separators are decoration: they never highlight and
// never return a result, so dragging across one selects nothing.
func TestHoverSkipsDecoration(t *testing.T) {
	m := &Menu{x: 0, y: 0, items: []Item{
		{Kind: ItemAction, Label: "a"},
		{Kind: ItemDave, Label: "~~dave~~"},
		{Kind: ItemSeparator},
		{Kind: ItemAction, Label: "b"},
	}}
	m.HandleMouseMotion(5, itemH/2, face) // over "a"
	if m.hovered != 0 {
		t.Errorf("hovered = %d, want 0", m.hovered)
	}
	m.HandleMouseMotion(5, itemH+itemH/2, face) // over ~~dave~~
	if m.hovered != -1 {
		t.Errorf("hovering ~~dave~~ gave hovered = %d, want -1", m.hovered)
	}
	m.HandleMouseMotion(5, 2*itemH+sepH/2, face) // over the separator
	if m.hovered != -1 {
		t.Errorf("hovering a separator gave hovered = %d, want -1", m.hovered)
	}
	m.HandleMouseMotion(500, 5, face) // outside the menu entirely
	if m.hovered != -1 {
		t.Errorf("hovering outside gave hovered = %d, want -1", m.hovered)
	}
}

func TestMouseUpReturnsTheChosenItem(t *testing.T) {
	f := mkFile(t, "a.txt")
	m := &Menu{x: 0, y: 0, open: true, items: []Item{
		{Kind: ItemAction, Label: "new"},
		{Kind: ItemDave, Label: "~~dave~~"},
		{Kind: ItemFile, Label: "a.txt", File: f},
	}}

	res, closed := m.HandleMouseUp(5, itemH/2, face)
	if res.Action != "new" || res.File != nil || !closed {
		t.Errorf("action click: %+v closed=%v, want Action=new", res, closed)
	}
	if m.IsOpen() {
		t.Error("menu still open after a selection")
	}

	m.open = true
	res, closed = m.HandleMouseUp(5, 2*itemH+itemH/2, face)
	if res.File != f || res.Action != "" || !closed {
		t.Errorf("file click: %+v closed=%v, want File=a.txt", res, closed)
	}
}

// Releasing over the divider, or off the menu, dismisses without selecting.
func TestMouseUpOnDecorationOrOutsideDismisses(t *testing.T) {
	for _, c := range []struct {
		x, y int32
		what string
	}{
		{5, itemH + itemH/2, "the ~~dave~~ divider"},
		{5, 1000, "below the menu"},
		{1000, 5, "right of the menu"},
	} {
		m := &Menu{x: 0, y: 0, open: true, items: []Item{
			{Kind: ItemAction, Label: "new"},
			{Kind: ItemDave, Label: "~~dave~~"},
		}}
		res, closed := m.HandleMouseUp(c.x, c.y, face)
		if res.Action != "" || res.File != nil {
			t.Errorf("%s: got %+v, want an empty result", c.what, res)
		}
		if !closed || m.IsOpen() {
			t.Errorf("%s: menu did not close", c.what)
		}
	}
}

// IsOpen is nil-safe, because main.go holds the active menu as a possibly-nil
// pointer and asks it every frame.
func TestIsOpenNilSafe(t *testing.T) {
	var m *Menu
	if m.IsOpen() {
		t.Error("a nil menu reports open")
	}
	m2 := NewEditMenu(0, 0)
	m2.Dismiss()
	if m2.IsOpen() {
		t.Error("a dismissed menu reports open")
	}
}
