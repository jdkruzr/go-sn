package note

import (
	"encoding/binary"
	"testing"
)

// buildStrokeObject constructs a minimal valid TOTALPATH stroke object with the
// given coordinates and tpPage dimensions. The object has "others\x00\x00" at
// offset +48, tpPageH at +128, tpPageW at +132, and point_count at +212.
func buildStrokeObject(tpPageW, tpPageH int, coords [][2]uint32) []byte {
	nPts := len(coords)
	objSize := 216 + nPts*8 + 4 + nPts*2 // header + coords + pressure_count + pressures
	buf := make([]byte, objSize)

	// "others\x00\x00" marker at +48
	copy(buf[48:56], "others\x00\x00")
	// tpPageH at +128, tpPageW at +132
	binary.LittleEndian.PutUint32(buf[128:], uint32(tpPageH))
	binary.LittleEndian.PutUint32(buf[132:], uint32(tpPageW))
	// point_count at +212
	binary.LittleEndian.PutUint32(buf[212:], uint32(nPts))

	// Coordinate pairs at +216
	for i, c := range coords {
		binary.LittleEndian.PutUint32(buf[216+i*8:], c[0])   // rawX
		binary.LittleEndian.PutUint32(buf[216+i*8+4:], c[1]) // rawY
	}

	// Pressure count and values
	pressOff := 216 + nPts*8
	binary.LittleEndian.PutUint32(buf[pressOff:], uint32(nPts))
	for i := 0; i < nPts; i++ {
		binary.LittleEndian.PutUint16(buf[pressOff+4+i*2:], 1500)
	}

	return buf
}

// buildTotalPathBlock wraps one or more raw object buffers into a TOTALPATH block.
// The block has a 4-byte object count header, then 4-byte size + object for each.
func buildTotalPathBlock(objects ...[]byte) []byte {
	// First 4 bytes: object count (not used by walker, but present in real data)
	// Next 4 bytes: first object size
	// Then first object data
	// For subsequent objects: 4-byte size prefix + data
	var buf []byte
	buf = append(buf, 0, 0, 0, 0) // object count placeholder

	for i, obj := range objects {
		size := make([]byte, 4)
		binary.LittleEndian.PutUint32(size, uint32(len(obj)))
		if i == 0 {
			buf = append(buf, size...) // first object size at offset 4
			buf = append(buf, obj...)
		} else {
			buf = append(buf, size...)
			buf = append(buf, obj...)
		}
	}
	return buf
}

// TestDecodeStroke_InflatedPointCount verifies that decodeStroke rejects a
// stroke whose point_count doesn't match the pressure_count at the expected
// offset. This reproduces the Supernote firmware bug where stroke 209 claims
// 118 points but the pressure_count at that position is garbage (103M).
func TestDecodeStroke_InflatedPointCount(t *testing.T) {
	tpPageW := 11864
	tpPageH := 15819

	// Build an object that claims 8 points but has garbage at the expected
	// pressure_count position (simulating an inflated point_count).
	inflatedCount := 8
	coords := [][2]uint32{
		{1000, 2000},
		{3000, 4000},
		{5000, 6000},
		{7000, 8000},
		{9000, 10000},
		{95_000_000, 110_000_000},
		{119_000_000, 117_000_000},
		{112_000_000, 113_000_000},
	}

	objSize := 216 + inflatedCount*8 + 4 + inflatedCount*2
	buf := make([]byte, objSize)
	copy(buf[48:56], "others\x00\x00")
	binary.LittleEndian.PutUint32(buf[128:], uint32(tpPageH))
	binary.LittleEndian.PutUint32(buf[132:], uint32(tpPageW))
	binary.LittleEndian.PutUint32(buf[212:], uint32(inflatedCount))

	for i, c := range coords {
		binary.LittleEndian.PutUint32(buf[216+i*8:], c[0])
		binary.LittleEndian.PutUint32(buf[216+i*8+4:], c[1])
	}
	// Write a NON-matching pressure count (simulates corruption)
	pressOff := 216 + inflatedCount*8
	binary.LittleEndian.PutUint32(buf[pressOff:], 103_089_641) // garbage
	for i := 0; i < inflatedCount; i++ {
		binary.LittleEndian.PutUint16(buf[pressOff+4+i*2:], 1500)
	}

	_, err := decodeStroke(buf, 0, len(buf), 1404, 1872)
	if err == nil {
		t.Fatal("expected error for stroke with mismatched pressure_count, got nil")
	}
}

// TestDecodeStroke_AllValidPoints verifies normal strokes are not truncated.
func TestDecodeStroke_AllValidPoints(t *testing.T) {
	tpPageW := 11864
	tpPageH := 15819
	coords := [][2]uint32{
		{1000, 2000},
		{3000, 4000},
		{5000, 6000},
	}

	obj := buildStrokeObject(tpPageW, tpPageH, coords)
	s, err := decodeStroke(obj, 0, len(obj), 1404, 1872)
	if err != nil {
		t.Fatalf("decodeStroke: %v", err)
	}
	if len(s.Points) != 3 {
		t.Errorf("got %d points, want 3", len(s.Points))
	}
	if len(s.Pressures) != 3 {
		t.Errorf("got %d pressures, want 3", len(s.Pressures))
	}
}

// TestDecodeTotalPath_DropsCorruptStrokes verifies that DecodeTotalPath
// drops strokes with mismatched pressure_count (corrupt header).
func TestDecodeTotalPath_DropsCorruptStrokes(t *testing.T) {
	tpPageW := 11864
	tpPageH := 15819

	good := buildStrokeObject(tpPageW, tpPageH, [][2]uint32{
		{1000, 2000},
		{3000, 4000},
	})

	// Build a corrupt stroke: valid coords but garbage pressure_count
	badObj := buildStrokeObject(tpPageW, tpPageH, [][2]uint32{
		{1000, 2000},
		{3000, 4000},
		{5000, 6000},
	})
	// Overwrite the pressure_count with garbage to simulate corruption
	pressOff := 216 + 3*8
	binary.LittleEndian.PutUint32(badObj[pressOff:], 103_089_641)

	tp := buildTotalPathBlock(good, badObj)
	strokes, err := DecodeTotalPath(tp, 1404, 1872)
	if err != nil {
		t.Fatalf("DecodeTotalPath: %v", err)
	}

	// Only the good stroke should survive; the corrupt one is dropped
	if len(strokes) != 1 {
		t.Errorf("got %d strokes, want 1 (corrupt stroke should be dropped)", len(strokes))
	}
	if len(strokes) > 0 && len(strokes[0].Points) != 2 {
		t.Errorf("stroke 0: %d points, want 2", len(strokes[0].Points))
	}
}
