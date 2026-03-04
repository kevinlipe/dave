package input

import (
	"dave/pane"

	"github.com/veandco/go-sdl2/sdl"
)

// BorderDragger manages hit-testing, SDL cursor changes, and dragging
// for a set of horizontal Border dividers.
type BorderDragger struct {
	borders      []*pane.Border
	hovered      *pane.Border
	dragging     *pane.Border
	cursorSizeNS *sdl.Cursor
	cursorArrow  *sdl.Cursor
}

// NewBorderDragger creates a dragger for the given borders.
func NewBorderDragger(borders ...*pane.Border) *BorderDragger {
	return &BorderDragger{
		borders:      borders,
		cursorSizeNS: sdl.CreateSystemCursor(sdl.SYSTEM_CURSOR_SIZENS),
		cursorArrow:  sdl.CreateSystemCursor(sdl.SYSTEM_CURSOR_ARROW),
	}
}

// Close frees SDL cursor resources.
func (d *BorderDragger) Close() {
	if d.cursorSizeNS != nil {
		sdl.FreeCursor(d.cursorSizeNS)
	}
	if d.cursorArrow != nil {
		sdl.FreeCursor(d.cursorArrow)
	}
}

// MouseMotion handles mouse movement. Returns true if a drag consumed the event.
// totalHeight is needed for constraint clamping during drag.
func (d *BorderDragger) MouseMotion(y, totalHeight int32) bool {
	if d.dragging != nil {
		d.dragging.Move(y, totalHeight)
		return true
	}
	// Update hover state and cursor.
	var hot *pane.Border
	for _, b := range d.borders {
		if b.Hot(y) {
			hot = b
			break
		}
	}
	if hot != d.hovered {
		d.hovered = hot
		if hot != nil {
			sdl.SetCursor(d.cursorSizeNS)
		} else {
			sdl.SetCursor(d.cursorArrow)
		}
	}
	return false
}

// MouseDown starts a drag if the mouse is over a border (left button only).
// Returns true if a drag began (event consumed).
func (d *BorderDragger) MouseDown(button uint8, y int32) bool {
	if button != sdl.BUTTON_LEFT || d.hovered == nil {
		return false
	}
	d.dragging = d.hovered
	return true
}

// MouseUp ends a drag. Returns true if a drag was in progress.
func (d *BorderDragger) MouseUp() bool {
	if d.dragging == nil {
		return false
	}
	d.dragging = nil
	if d.hovered == nil {
		sdl.SetCursor(d.cursorArrow)
	}
	return true
}

// IsDragging returns true while a border drag is in progress.
func (d *BorderDragger) IsDragging() bool { return d.dragging != nil }
