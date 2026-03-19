package note

import (
	"encoding/binary"
	"slices"
	"strconv"
	"testing"
)

// TestComputeShift_Stable verifies AC3.1: no digit-width crossings → returns baseShift unchanged.
func TestComputeShift_Stable(t *testing.T) {
	offsets := []int{1000, 2000, 3000}
	got := computeShift(100, offsets)
	if got != 100 {
		t.Errorf("computeShift(100, %v) = %d, want 100", offsets, got)
	}
}

// TestComputeShift_DigitCrossing verifies AC3.2: an offset crossing a digit boundary
// causes the extra byte to be included in the final shift.
func TestComputeShift_DigitCrossing(t *testing.T) {
	// 99990 is 5 digits; 99990+10 = 100000 is 6 digits → shift must grow by 1
	offsets := []int{99990}
	got := computeShift(10, offsets)
	if got != 11 {
		t.Errorf("computeShift(10, [99990]) = %d, want 11 (digit crossing adds 1)", got)
	}
}

// TestComputeShift_Terminates verifies AC3.3: the loop always converges.
// A large list with many near-crossing offsets must still terminate.
func TestComputeShift_Terminates(t *testing.T) {
	// Multiple offsets each near a digit boundary — must converge, not loop forever.
	offsets := []int{9, 99, 999, 9999, 99999, 999999}
	// baseShift=1: each of these will cross (9→10, 99→100, etc.), adding 1 byte each.
	// extra = 6, so finalShift = 1 + 6 = 7.
	// Verify by tracing: shift=1 → extra=6 → newShift=7 → shift=7 → extra=6 → newShift=7 → stable.
	got := computeShift(1, offsets)
	if got != 7 {
		t.Errorf("computeShift(1, allNines) = %d, want 7 (1 base + 6 digit crossings)", got)
	}
}

// TestBuildOffsetMap verifies that buildOffsetMap produces {O → O+shift} for all inputs.
func TestBuildOffsetMap(t *testing.T) {
	offsets := []int{100, 200, 300}
	m := buildOffsetMap(offsets, 50)
	for _, o := range offsets {
		if m[o] != o+50 {
			t.Errorf("buildOffsetMap: m[%d] = %d, want %d", o, m[o], o+50)
		}
	}
	if len(m) != 3 {
		t.Errorf("buildOffsetMap len = %d, want 3", len(m))
	}
}

// TestCollectOffsets_StandardNote verifies collectOffsets against the 3-page standard corpus.
// insertionPoint = page 0's metadata offset (injecting into the first page).
func TestCollectOffsets_StandardNote(t *testing.T) {
	n := loadNote(t, "gosnstd.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	// insertionPoint is page 0's metadata offset (the target page meta block start).
	insertionPoint, err := n.footerPageOffset(0)
	if err != nil {
		t.Fatalf("footerPageOffset(0): %v", err)
	}

	offsets := collectOffsets(n, insertionPoint)

	// All returned offsets must be > insertionPoint.
	for _, o := range offsets {
		if o <= insertionPoint {
			t.Errorf("collectOffsets returned offset %d <= insertionPoint %d", o, insertionPoint)
		}
	}

	// Page 1 and page 2 metadata offsets (from footer) must be in the result.
	for _, pageIdx := range []int{1, 2} {
		pageOff, err := n.footerPageOffset(pageIdx)
		if err != nil {
			t.Fatalf("footerPageOffset(%d): %v", pageIdx, err)
		}
		if !slices.Contains(offsets, pageOff) {
			t.Errorf("collectOffsets missing page %d offset %d", pageIdx, pageOff)
		}
	}

	// Footer offset itself is NOT in collectOffsets (footer offset is in buildUpdateSet, not here).
	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	if slices.Contains(offsets, footerOff) {
		t.Errorf("collectOffsets should not include the footer block offset %d (it's a block to rebuild, not an offset value to relocate)", footerOff)
	}
}

// TestCollectOffsets_RTRNote verifies collectOffsets against the 3-page RTR corpus,
// which has an out-of-order page layout (display page indices don't match file order).
func TestCollectOffsets_RTRNote(t *testing.T) {
	n := loadNote(t, "gosnrtr.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	insertionPoint, err := n.footerPageOffset(0)
	if err != nil {
		t.Fatalf("footerPageOffset(0): %v", err)
	}

	offsets := collectOffsets(n, insertionPoint)

	// All offsets must be > insertionPoint.
	for _, o := range offsets {
		if o <= insertionPoint {
			t.Errorf("collectOffsets returned offset %d <= insertionPoint %d", o, insertionPoint)
		}
	}

	// At least the footer-referenced page offsets for subsequent pages must appear.
	for _, pageIdx := range []int{1, 2} {
		pageOff, err := n.footerPageOffset(pageIdx)
		if err != nil {
			t.Fatalf("footerPageOffset(%d): %v", pageIdx, err)
		}
		if pageOff > insertionPoint && !slices.Contains(offsets, pageOff) {
			t.Errorf("collectOffsets missing page %d offset %d for RTR note", pageIdx, pageOff)
		}
	}

	// RTR note has RECOGNTEXT on each page: those offsets must appear if > insertionPoint.
	for _, p := range n.Pages {
		pageOff, err := n.footerPageOffset(p.Index)
		if err != nil || pageOff <= insertionPoint {
			continue
		}
		if rt := p.Meta["RECOGNTEXT"]; rt != "" && rt != "0" {
			rtOff, _ := strconv.Atoi(rt)
			if rtOff > insertionPoint && !slices.Contains(offsets, rtOff) {
				t.Errorf("collectOffsets missing RECOGNTEXT offset %d for page %d", rtOff, p.Index)
			}
		}
	}
}

// TestBuildUpdateSet_StandardNote verifies that buildUpdateSet includes the footer
// and all page-meta blocks past insertionPoint.
func TestBuildUpdateSet_StandardNote(t *testing.T) {
	n := loadNote(t, "gosnstd.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	insertionPoint, err := n.footerPageOffset(0)
	if err != nil {
		t.Fatalf("footerPageOffset(0): %v", err)
	}

	updateSet := buildUpdateSet(n, insertionPoint)

	// Footer must always be in the update set.
	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	if !updateSet[footerOff] {
		t.Errorf("buildUpdateSet missing footer offset %d", footerOff)
	}

	// Pages 1 and 2 meta offsets (both > insertionPoint) must be in the update set.
	for _, pageIdx := range []int{1, 2} {
		pageOff, err := n.footerPageOffset(pageIdx)
		if err != nil {
			t.Fatalf("footerPageOffset(%d): %v", pageIdx, err)
		}
		if !updateSet[pageOff] {
			t.Errorf("buildUpdateSet missing page %d meta offset %d", pageIdx, pageOff)
		}
	}
}
