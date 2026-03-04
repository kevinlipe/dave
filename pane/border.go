package pane

// Border is a draggable horizontal dividing line between two adjacent regions.
// Y is the pixel y-coordinate of the top of the lower region.
type Border struct {
	Y            int32
	MinAbove     int32   // min px height of region above
	MinBelow     int32   // min px height of region below
	MaxAboveFrac float64 // if > 0: Y <= MaxAboveFrac * totalHeight
}

// BorderGrabHalf is the half-height of the grab zone (±4px).
const BorderGrabHalf = int32(4)

// Hot returns true if pixel y falls within the grab zone.
func (b *Border) Hot(y int32) bool {
	return y >= b.Y-BorderGrabHalf && y <= b.Y+BorderGrabHalf
}

// Move repositions b.Y to newY, clamped by constraints and totalHeight.
func (b *Border) Move(newY, totalHeight int32) {
	lo := b.MinAbove
	hi := totalHeight - b.MinBelow
	if b.MaxAboveFrac > 0 {
		maxFrac := int32(float64(totalHeight) * b.MaxAboveFrac)
		if maxFrac < hi {
			hi = maxFrac
		}
	}
	if newY < lo {
		newY = lo
	}
	if newY > hi {
		newY = hi
	}
	b.Y = newY
}
