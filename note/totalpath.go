package note

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Stroke is a single pen-down → pen-up sequence of points.
type Stroke struct {
	Points    []Point  // portrait pixel coordinates
	Pressures []uint16 // raw pressure values (0–4096 scale), same length as Points
}

// Point is a position in portrait pixel space.
type Point struct {
	X, Y float64
}

// ObjectType classifies a TOTALPATH object.
type ObjectType int

const (
	ObjectTypeStroke  ObjectType = 0
	ObjectTypeTextBox ObjectType = 1 // byte8=200; PAGETEXTBOX overlay
	ObjectTypeDigest  ObjectType = 2 // byte8=100; screen-clip / digest region
)

// Rect is an axis-aligned bounding box in portrait pixel space.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

func (r Rect) Width() float64  { return r.MaxX - r.MinX }
func (r Rect) Height() float64 { return r.MaxY - r.MinY }

// NonStrokeObject is a decoded text-box or digest bounding box.
type NonStrokeObject struct {
	Type   ObjectType
	Bounds Rect
}

// PageObjects holds all decoded objects from a TOTALPATH block.
type PageObjects struct {
	Strokes    []Stroke
	NonStrokes []NonStrokeObject
}

// objectTypeFromByte8 maps the discriminator uint32 at offset +8 to an ObjectType.
// Returns 0 (ObjectTypeStroke) if the code is not a known non-stroke type.
func objectTypeFromByte8(v uint32) ObjectType {
	switch v {
	case 200:
		return ObjectTypeTextBox
	case 100:
		return ObjectTypeDigest
	}
	return ObjectTypeStroke
}

// isStrokeObject returns true if the 216-byte object header beginning at off
// looks like a pen stroke (has "others\x00\x00" at off+48).
func isStrokeObject(data []byte, off int) bool {
	if off+56 > len(data) {
		return false
	}
	return string(data[off+48:off+56]) == "others\x00\x00"
}

// walkObjects iterates over all objects in a TOTALPATH block, calling fn for each.
// fn receives (dataStart, objSize int).
func walkObjects(tp []byte, fn func(dataStart, objSize int)) {
	if len(tp) < 8 {
		return
	}
	firstObjSize := int(binary.LittleEndian.Uint32(tp[4:8]))
	objOff := 8
	objSize := firstObjSize
	first := true

	for objOff < len(tp) {
		if !first {
			if objOff+4 > len(tp) {
				break
			}
			objSize = int(binary.LittleEndian.Uint32(tp[objOff:]))
			objOff += 4
		}
		first = false

		dataStart := objOff
		if dataStart+objSize > len(tp) {
			objSize = len(tp) - dataStart
		}
		fn(dataStart, objSize)
		objOff = dataStart + objSize
	}
}

// DecodeTotalPath parses the TOTALPATH block returned by Note.TotalPathData.
// pageW and pageH are the device pixel dimensions (e.g. 1404×1872 for N6).
// Returns all pen strokes found in the block.
func DecodeTotalPath(tp []byte, pageW, pageH int) ([]Stroke, error) {
	if len(tp) < 8 {
		return nil, fmt.Errorf("TOTALPATH block too short (%d bytes)", len(tp))
	}

	var strokes []Stroke
	walkObjects(tp, func(dataStart, objSize int) {
		if objSize < 216 {
			return
		}
		if isStrokeObject(tp, dataStart) {
			s, err := decodeStroke(tp, dataStart, objSize, pageW, pageH)
			if err == nil {
				strokes = append(strokes, s)
			}
		}
	})
	return strokes, nil
}

// DecodeObjects parses the TOTALPATH block and returns all objects:
// pen strokes and non-stroke bounding boxes (text boxes and digests).
func DecodeObjects(tp []byte, pageW, pageH int) (*PageObjects, error) {
	if len(tp) < 8 {
		return nil, fmt.Errorf("TOTALPATH block too short (%d bytes)", len(tp))
	}

	out := &PageObjects{}
	walkObjects(tp, func(dataStart, objSize int) {
		if objSize < 216 {
			return
		}
		if isStrokeObject(tp, dataStart) {
			s, err := decodeStroke(tp, dataStart, objSize, pageW, pageH)
			if err == nil {
				out.Strokes = append(out.Strokes, s)
			}
			return
		}
		// Check for known non-stroke discriminator at +8
		b8 := binary.LittleEndian.Uint32(tp[dataStart+8:])
		ot := objectTypeFromByte8(b8)
		if ot != ObjectTypeStroke {
			bounds, ok := decodeBoundingBox(tp, dataStart, objSize, pageW, pageH)
			if ok {
				out.NonStrokes = append(out.NonStrokes, NonStrokeObject{Type: ot, Bounds: bounds})
			}
		}
	})
	return out, nil
}

// decodeBoundingBox decodes the axis-aligned bounding box from a non-stroke object.
//
// Non-stroke objects use the same coordinate transform as strokes.
// The bounding box is encoded as 5 closed-polygon points (corners of a rectangle)
// at the same offset (+212 point_count, +216 coords) as stroke point data.
func decodeBoundingBox(tp []byte, dataStart, objSize, pageW, pageH int) (Rect, bool) {
	if dataStart+216 > len(tp) {
		return Rect{}, false
	}
	tpH := int(binary.LittleEndian.Uint32(tp[dataStart+128:]))
	tpW := int(binary.LittleEndian.Uint32(tp[dataStart+132:]))
	if tpH <= 0 || tpW <= 0 {
		return Rect{}, false
	}

	n := int(binary.LittleEndian.Uint32(tp[dataStart+212:]))
	if n <= 0 || n > 1000 || dataStart+216+n*8 > len(tp) {
		return Rect{}, false
	}

	minX := math.MaxFloat64
	minY := math.MaxFloat64
	maxX := -math.MaxFloat64
	maxY := -math.MaxFloat64

	for i := 0; i < n; i++ {
		base := dataStart + 216 + i*8
		rawX := binary.LittleEndian.Uint32(tp[base:])
		rawY := binary.LittleEndian.Uint32(tp[base+4:])
		pxY := float64(rawX) * float64(pageH) / float64(tpH)
		pxX := (float64(tpW) - float64(rawY)) * float64(pageW) / float64(tpW)
		if pxX < minX {
			minX = pxX
		}
		if pxX > maxX {
			maxX = pxX
		}
		if pxY < minY {
			minY = pxY
		}
		if pxY > maxY {
			maxY = pxY
		}
	}

	return Rect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}, true
}

// decodeStroke parses one pen-stroke object starting at dataStart.
//
// Object layout (relative to dataStart):
//
//	[0:212]         fixed header
//	[212:216]       point_count (uint32 LE)
//	[216:216+N*8]   N coordinate pairs: (rawX, rawY) uint32 LE each
//	[216+N*8]       pressure_count (uint32 LE, must equal N)
//	[220+N*8:...]   N pressure values (uint16 LE each)
//	...             additional arrays (timing etc.) — not parsed
//
// Coordinate transform (portrait pixel space):
//
//	pixel_Y = rawX * pageH / tpPageH
//	pixel_X = (tpPageW - rawY) * pageW / tpPageW
//
// where tpPageH and tpPageW are read from the header at +128 and +132.
func decodeStroke(tp []byte, dataStart, objSize, pageW, pageH int) (Stroke, error) {
	if dataStart+216 > len(tp) {
		return Stroke{}, fmt.Errorf("object too short for header")
	}

	tpPageH := int(binary.LittleEndian.Uint32(tp[dataStart+128:]))
	tpPageW := int(binary.LittleEndian.Uint32(tp[dataStart+132:]))
	if tpPageH <= 0 || tpPageW <= 0 {
		return Stroke{}, fmt.Errorf("invalid page dimensions in header: %d×%d", tpPageW, tpPageH)
	}

	n := int(binary.LittleEndian.Uint32(tp[dataStart+212:]))
	if n <= 0 || n > 100_000 {
		return Stroke{}, fmt.Errorf("implausible point count: %d", n)
	}

	coordEnd := dataStart + 216 + n*8
	if coordEnd > len(tp) {
		return Stroke{}, fmt.Errorf("coordinate data exceeds buffer: need %d, have %d", coordEnd, len(tp))
	}

	pts := make([]Point, n)
	for i := 0; i < n; i++ {
		base := dataStart + 216 + i*8
		rawX := binary.LittleEndian.Uint32(tp[base:])
		rawY := binary.LittleEndian.Uint32(tp[base+4:])

		pts[i] = Point{
			Y: float64(rawX) * float64(pageH) / float64(tpPageH),
			X: (float64(tpPageW) - float64(rawY)) * float64(pageW) / float64(tpPageW),
		}
	}

	// Pressure data (optional — may be absent in future format versions)
	var pressures []uint16
	if coordEnd+4 <= len(tp) {
		pcount := int(binary.LittleEndian.Uint32(tp[coordEnd:]))
		if pcount == n {
			pressureEnd := coordEnd + 4 + n*2
			if pressureEnd <= len(tp) {
				pressures = make([]uint16, n)
				for i := 0; i < n; i++ {
					pressures[i] = binary.LittleEndian.Uint16(tp[coordEnd+4+i*2:])
				}
			}
		}
	}

	return Stroke{Points: pts, Pressures: pressures}, nil
}
