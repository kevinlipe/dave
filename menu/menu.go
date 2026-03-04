// Package menu implements Dave's popup menus: the right-click buffer menu
// and the shift-right-click editing menu.
//
// Menus are rendered as SDL overlays on top of everything else.
package menu

import (
	"dave/file"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// ItemKind identifies a menu item type.
type ItemKind int

const (
	ItemAction    ItemKind = iota // a clickable action
	ItemSeparator                 // a visual divider line
	ItemFile                      // a file entry from the buffer list
)

// Item is one entry in a menu.
type Item struct {
	Kind  ItemKind
	Label string
	File  *file.File // non-nil for ItemFile
}

// Result is what the caller receives when the user picks from a menu.
type Result struct {
	// Action is the label of the chosen action item (e.g. "New", "Sidebar").
	// Empty if no action was chosen or the menu was dismissed.
	Action string
	// File is non-nil if the user picked a file item.
	File *file.File
}

// Menu is a rendered popup overlay.
type Menu struct {
	items   []Item
	x, y    int32  // top-left of the popup in window coords
	hovered int    // index of hovered item, or -1
	open    bool
}

// itemH is the pixel height of one menu row.
const itemH = 22

// sepH is the pixel height of a separator.
const sepH = 8

// padX is horizontal padding inside the menu.
const padX = 10

// NewBufferMenu creates the right-click buffer menu with the given open files.
// hasSidebar controls whether "Sidebar" or "Kill sidebar" is shown.
func NewBufferMenu(files []*file.File, hasSidebar bool, x, y int32) *Menu {
	sidebarLabel := "Sidebar"
	if hasSidebar {
		sidebarLabel = "Kill sidebar"
	}
	items := []Item{
		{Kind: ItemAction, Label: "New"},
		{Kind: ItemAction, Label: sidebarLabel},
		{Kind: ItemSeparator},
	}
	for _, f := range files {
		items = append(items, Item{Kind: ItemFile, Label: f.MenuLine(false), File: f})
	}
	return &Menu{items: items, x: x, y: y, hovered: -1, open: true}
}

// NewEditMenu creates the shift-right-click editing menu.
func NewEditMenu(x, y int32) *Menu {
	items := []Item{
		{Kind: ItemAction, Label: "Cut"},
		{Kind: ItemAction, Label: "Paste"},
		{Kind: ItemAction, Label: "Snarf"},
		{Kind: ItemAction, Label: "Look"},
		{Kind: ItemAction, Label: "Send"},
		{Kind: ItemAction, Label: "Undo"},
	}
	return &Menu{items: items, x: x, y: y, hovered: -1, open: true}
}

// IsOpen reports whether the menu is visible.
func (m *Menu) IsOpen() bool { return m != nil && m.open }

// Dismiss closes the menu without selecting anything.
func (m *Menu) Dismiss() { m.open = false }

// width returns the pixel width of the menu, based on the widest label.
func (m *Menu) width(face render.Face) int32 {
	w := int32(120)
	for _, it := range m.items {
		if it.Kind == ItemSeparator {
			continue
		}
		tw := int32(face.MeasureText(it.Label)) + padX*2
		if tw > w {
			w = tw
		}
	}
	return w
}

// height returns the total pixel height of the menu.
func (m *Menu) height() int32 {
	h := int32(0)
	for _, it := range m.items {
		if it.Kind == ItemSeparator {
			h += sepH
		} else {
			h += itemH
		}
	}
	return h
}

// itemRect returns the bounding rect of item i relative to m.x, m.y.
func (m *Menu) itemRect(i int, face render.Face) sdl.Rect {
	w := m.width(face)
	y := int32(0)
	for j, it := range m.items {
		var h int32
		if it.Kind == ItemSeparator {
			h = sepH
		} else {
			h = itemH
		}
		if j == i {
			return sdl.Rect{X: m.x, Y: m.y + y, W: w, H: h}
		}
		y += h
	}
	return sdl.Rect{}
}

// HandleMouseMotion updates the hovered item.
func (m *Menu) HandleMouseMotion(x, y int32, face render.Face) {
	m.hovered = -1
	for i, it := range m.items {
		if it.Kind == ItemSeparator {
			continue
		}
		r := m.itemRect(i, face)
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			m.hovered = i
			return
		}
	}
}

// HandleMouseUp processes a mouse button release. Returns a Result and whether
// the menu should now be closed.
func (m *Menu) HandleMouseUp(x, y int32, face render.Face) (Result, bool) {
	for i, it := range m.items {
		if it.Kind == ItemSeparator {
			continue
		}
		r := m.itemRect(i, face)
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			m.open = false
			switch it.Kind {
			case ItemAction:
				return Result{Action: it.Label}, true
			case ItemFile:
				return Result{File: it.File}, true
			}
		}
	}
	// Click outside menu → dismiss.
	m.open = false
	return Result{}, true
}

// Draw renders the menu onto the SDL renderer.
// Call this after all other pane drawing so it appears on top.
func (m *Menu) Draw(ren *sdl.Renderer, face render.Face, pal render.Palette) {
	if !m.open {
		return
	}
	w := m.width(face)
	h := m.height()

	// Menu background.
	ren.SetDrawColor(pal.MenuBG.R, pal.MenuBG.G, pal.MenuBG.B, 255)
	ren.FillRect(&sdl.Rect{X: m.x, Y: m.y, W: w, H: h})

	// Menu border.
	ren.SetDrawColor(pal.Border.R, pal.Border.G, pal.Border.B, 255)
	ren.DrawRect(&sdl.Rect{X: m.x, Y: m.y, W: w, H: h})

	y := m.y
	for i, it := range m.items {
		if it.Kind == ItemSeparator {
			mid := y + sepH/2
			ren.SetDrawColor(pal.Border.R, pal.Border.G, pal.Border.B, 255)
			ren.DrawLine(m.x+2, mid, m.x+w-2, mid)
			y += sepH
			continue
		}

		// Highlight hovered row.
		if i == m.hovered {
			ren.SetDrawColor(pal.MenuHilight.R, pal.MenuHilight.G, pal.MenuHilight.B, 255)
			ren.FillRect(&sdl.Rect{X: m.x + 1, Y: y + 1, W: w - 2, H: itemH - 2})
		}

		face.DrawText(it.Label, m.x+padX, y+(itemH-int32(face.Height()))/2, pal.MenuFG)
		y += itemH
	}
}
