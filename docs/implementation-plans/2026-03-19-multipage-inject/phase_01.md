# Multi-Page InjectRecognText — Phase 1: Offset Collection and Shift Computation

**Goal:** Build the infrastructure in `note/relocate.go` that identifies which offsets need relocation and computes the final byte shift.

**Architecture:** Pure functional-core functions operating on the already-loaded `n.raw` bytes of a `*Note`. No I/O — all inputs are data already in memory. Output is offset lists, maps, and scalars consumed by Phase 2's emit logic.

**Tech Stack:** Go standard library (`strconv`, `regexp`, `encoding/binary`). No new dependencies.

**Scope:** Phase 1 of 2 from the original design.

**Codebase verified:** 2026-03-19

---

## Acceptance Criteria Coverage

This phase implements and tests:

### multipage-inject.AC3: Variable-width offset values handled via convergence
- **multipage-inject.AC3.1 Success:** computeShift returns a stable result for normal inputs (no digit-width crossings)
- **multipage-inject.AC3.2 Edge:** An offset crossing a decimal digit boundary (e.g., 99990 + shift produces a 6-digit value where 99990 was 5 digits) causes computeShift to include the extra byte in the final shift
- **multipage-inject.AC3.3 Edge:** computeShift always terminates (convergence guaranteed)

---

## Implementation Context

The existing codebase (all in `package note`):

- `note/parse.go`: `Note{Header Tags, Pages []*Page, raw []byte}`, `Page{Index int, Meta Tags, raw []byte}`, `Tags = map[string]string`, `BlockAt(off int) ([]byte, error)`, `FooterTags() (Tags, error)`, `parseTags(b []byte) Tags`
- `note/write.go`: `footerPageOffset(pageIdx int) (int, error)`, `replaceTagValue(meta []byte, key, newVal string) []byte`, `appendBlock(dst, data []byte) []byte`
- Test helpers in `note/write_test.go`: `loadNote(t, name)`, `roundTripNote(t, out)` — use `../../testdata/` relative path
- Multi-page corpus files in `testdata/`:
  - `"gosnstd.note"` — 3-page standard note
  - `"gosnrtr.note"` — 3-page RTR note (out-of-order page layout)
- Single-page corpus: `"20260318_154108 std one line.note"`, `"20260318_154754 rtr one line.note"`

Footer offset in raw bytes: `int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))` (last 4 bytes, since tail = "tail" + uint32, 8 bytes total, footer uint32 at `len-4`).

**FCIS:** `note/relocate.go` is **Functional Core** — pure functions, no I/O, no side effects.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->

<!-- START_TASK_1 -->
## Task 1: Create `note/relocate.go` with `collectOffsets`, `computeShift`, `buildOffsetMap`, `buildUpdateSet`

**Verifies:** multipage-inject.AC3.1, multipage-inject.AC3.2, multipage-inject.AC3.3 (via task 2 tests)

**Files:**
- Create: `note/relocate.go`

**Implementation:**

```go
// pattern: Functional Core
package note

import (
	"encoding/binary"
	"strconv"
)

// collectOffsets returns all offset values stored as tag values in the note
// that are greater than insertionPoint. These are the offsets that must be
// incremented when a new block is inserted at insertionPoint.
//
// Collects from:
//   - Footer PAGE{N}, TITLE_*, KEYWORD_* tag values
//   - KEYWORDSITE tags inside each KEYWORD block whose offset > insertionPoint
//   - Each page-meta block at offset > insertionPoint: MAINLAYER, BGLAYER,
//     TOTALPATH, RECOGNTEXT, RECOGNFILE tag values
//   - LAYERBITMAP inside MAINLAYER/BGLAYER blocks at offset > insertionPoint
func collectOffsets(n *Note, insertionPoint int) []int {
	seen := make(map[int]bool)
	add := func(v int) {
		if v > insertionPoint {
			seen[v] = true
		}
	}
	parseInt := func(s string) (int, bool) {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 {
			return 0, false
		}
		return v, true
	}

	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	footerLen := int(binary.LittleEndian.Uint32(n.raw[footerOff:]))
	footer := parseTags(n.raw[footerOff+4 : footerOff+4+footerLen])

	for key, val := range footer {
		isPage := len(key) > 4 && key[:4] == "PAGE"
		isTitle := len(key) > 6 && key[:6] == "TITLE_"
		isKeyword := len(key) > 8 && key[:8] == "KEYWORD_"
		if !isPage && !isTitle && !isKeyword {
			continue
		}
		off, ok := parseInt(val)
		if !ok {
			continue
		}
		add(off)
		// Collect KEYWORDSITE offsets inside each KEYWORD block.
		if isKeyword && off > insertionPoint {
			if block, err := n.BlockAt(off); err == nil {
				tags := parseTags(block)
				for kk, kv := range tags {
					if len(kk) >= 11 && kk[:11] == "KEYWORDSITE" {
						if kOff, ok2 := parseInt(kv); ok2 {
							add(kOff)
						}
					}
				}
			}
		}
	}

	// Collect offsets from each page-meta block at offset > insertionPoint.
	for i := range n.Pages {
		pageOff, err := n.footerPageOffset(i)
		if err != nil || pageOff <= insertionPoint {
			continue
		}
		pageMetaLen := int(binary.LittleEndian.Uint32(n.raw[pageOff:]))
		pageMeta := parseTags(n.raw[pageOff+4 : pageOff+4+pageMetaLen])
		for _, tag := range []string{"MAINLAYER", "BGLAYER", "TOTALPATH", "RECOGNTEXT", "RECOGNFILE"} {
			off, ok := parseInt(pageMeta[tag])
			if !ok {
				continue
			}
			add(off)
			// Collect LAYERBITMAP inside layer blocks that are themselves past insertionPoint.
			if (tag == "MAINLAYER" || tag == "BGLAYER") && off > insertionPoint {
				if block, err := n.BlockAt(off); err == nil {
					tags := parseTags(block)
					if bOff, ok2 := parseInt(tags["LAYERBITMAP"]); ok2 {
						add(bOff)
					}
				}
			}
		}
	}

	result := make([]int, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	return result
}

// computeShift computes the final total byte shift caused by inserting a block
// of baseShift bytes at insertionPoint. affectedOffsets is the list returned by
// collectOffsets. The extra bytes arise from decimal digit-width growth: when
// shifting offset O by the current shift causes strconv.Itoa(O+shift) to be
// longer than strconv.Itoa(O), the tag value itself grows by one byte, which
// must be included in the total shift. The loop accumulates the total extra
// bytes across all offsets at the current shift, then updates shift to
// baseShift + extra. It repeats until shift stabilizes.
//
// Convergence is guaranteed because shift is non-decreasing and bounded above
// by baseShift + len(affectedOffsets) (each offset can cross at most one
// decimal digit boundary).
func computeShift(baseShift int, affectedOffsets []int) int {
	shift := baseShift
	for {
		extra := 0
		for _, o := range affectedOffsets {
			extra += len(strconv.Itoa(o+shift)) - len(strconv.Itoa(o))
		}
		newShift := baseShift + extra
		if newShift == shift {
			return shift
		}
		shift = newShift
	}
}

// buildOffsetMap returns a map from each offset in affectedOffsets to its
// relocated value (offset + finalShift).
func buildOffsetMap(affectedOffsets []int, finalShift int) map[int]int {
	m := make(map[int]int, len(affectedOffsets))
	for _, o := range affectedOffsets {
		m[o] = o + finalShift
	}
	return m
}

// buildUpdateSet returns the set of block file offsets that must be rebuilt
// (their tag values rewritten) rather than emitted verbatim. These are:
//   - The footer block (always rebuilt: PAGE{N} tags updated)
//   - All page-meta blocks at offset > insertionPoint
//   - All MAINLAYER and BGLAYER blocks at offset > insertionPoint
//   - All KEYWORD annotation blocks at offset > insertionPoint (contain KEYWORDSITE tags)
func buildUpdateSet(n *Note, insertionPoint int) map[int]bool {
	s := make(map[int]bool)

	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	s[footerOff] = true

	parseInt := func(v string) (int, bool) {
		off, err := strconv.Atoi(v)
		if err != nil || off <= 0 {
			return 0, false
		}
		return off, true
	}

	footerLen := int(binary.LittleEndian.Uint32(n.raw[footerOff:]))
	footer := parseTags(n.raw[footerOff+4 : footerOff+4+footerLen])

	// Include KEYWORD annotation blocks past insertionPoint (they contain KEYWORDSITE offsets).
	for key, val := range footer {
		if len(key) >= 8 && key[:8] == "KEYWORD_" {
			off, ok := parseInt(val)
			if ok && off > insertionPoint {
				s[off] = true
			}
		}
	}

	for i := range n.Pages {
		pageOff, err := n.footerPageOffset(i)
		if err != nil || pageOff <= insertionPoint {
			continue
		}
		s[pageOff] = true
		pageMetaLen := int(binary.LittleEndian.Uint32(n.raw[pageOff:]))
		pageMeta := parseTags(n.raw[pageOff+4 : pageOff+4+pageMetaLen])
		for _, tag := range []string{"MAINLAYER", "BGLAYER"} {
			off, ok := parseInt(pageMeta[tag])
			if !ok {
				continue
			}
			if off > insertionPoint {
				s[off] = true
			}
		}
	}
	return s
}
```

**Verification:**
```
go build -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./...
```
Expected: Builds without errors.

**Commit:** `feat: add note/relocate.go with collectOffsets, computeShift, buildOffsetMap, buildUpdateSet`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
## Task 2: Write tests in `note/relocate_test.go`

**Verifies:** multipage-inject.AC3.1, multipage-inject.AC3.2, multipage-inject.AC3.3

**Files:**
- Create: `note/relocate_test.go`

**Implementation:**

```go
package note

import (
	"encoding/binary"
	"slices"
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
```

**Note on imports:** The test uses `slices.Contains` (Go 1.21+) and `strconv.Atoi`. Add `"strconv"` to the import block.

**Verification:**
```
go test -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./note/ -run TestComputeShift -v
go test -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./note/ -run TestBuildOffsetMap -v
go test -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./note/ -run TestCollectOffsets -v
go test -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./note/ -run TestBuildUpdateSet -v
```
Expected: All tests pass.

```
go test -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./...
```
Expected: All tests pass (including existing write_test.go tests).

**Commit:** `test: add relocate_test.go for collectOffsets, computeShift, buildOffsetMap, buildUpdateSet`
<!-- END_TASK_2 -->

<!-- END_SUBCOMPONENT_A -->
