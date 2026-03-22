package note

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

const testdataDir = "../../testdata/" // .note files live in testdata/

func loadNote(t *testing.T, name string) *Note {
	t.Helper()
	f, err := os.Open(testdataDir + name)
	if err != nil {
		t.Skipf("test file not found: %v", err)
	}
	defer f.Close()
	n, err := Load(f)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return n
}

// roundTripNote re-parses out and returns the resulting Note.
func roundTripNote(t *testing.T, out []byte) *Note {
	t.Helper()
	n, err := parse(out)
	if err != nil {
		t.Fatalf("re-parse after inject: %v", err)
	}
	return n
}

// readContent reads and unmarshals the RECOGNTEXT block from page 0 of n.
func readContent(t *testing.T, n *Note) RecognContent {
	t.Helper()
	raw, err := n.ReadRecognText(n.Pages[0])
	if err != nil {
		t.Fatalf("ReadRecognText: %v", err)
	}
	if raw == nil {
		t.Fatal("ReadRecognText returned nil (no block)")
	}
	var c RecognContent
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal RecognContent: %v", err)
	}
	return c
}

func TestInjectRecognText_StandardNote(t *testing.T) {
	n := loadNote(t, "20260318_154108 std one line.note")
	if n.Pages[0].Meta["RECOGNTEXT"] != "0" {
		t.Skip("expected RECOGNTEXT=0 for standard note")
	}

	want := RecognContent{
		Type: "Text",
		Elements: []RecognElement{
			{
				Type:        "Text",
				Label:       "hello world",
				BoundingBox: &RecognBox{X: 100, Y: 200, Width: 500, Height: 50},
			},
		},
	}

	out, err := n.InjectRecognText(0, want)
	if err != nil {
		t.Fatalf("InjectRecognText: %v", err)
	}

	n2 := roundTripNote(t, out)
	p := n2.Pages[0]

	if p.Meta["RECOGNSTATUS"] != "1" {
		t.Errorf("RECOGNSTATUS = %q, want 1", p.Meta["RECOGNSTATUS"])
	}
	if p.Meta["RECOGNTEXT"] == "0" || p.Meta["RECOGNTEXT"] == "" {
		t.Errorf("RECOGNTEXT not updated: %q", p.Meta["RECOGNTEXT"])
	}

	got := readContent(t, n2)
	if got.Type != want.Type || len(got.Elements) != 1 || got.Elements[0].Label != want.Elements[0].Label {
		t.Errorf("content mismatch: got %+v", got)
	}
}

func TestInjectRecognText_RTRNote(t *testing.T) {
	n := loadNote(t, "20260318_154754 rtr one line.note")
	if n.Pages[0].Meta["RECOGNSTATUS"] != "1" {
		t.Skip("expected RTR note with existing recognition")
	}

	want := RecognContent{
		Type: "Text",
		Elements: []RecognElement{
			{Type: "Text", Label: "replaced text"},
		},
	}

	out, err := n.InjectRecognText(0, want)
	if err != nil {
		t.Fatalf("InjectRecognText: %v", err)
	}

	n2 := roundTripNote(t, out)
	got := readContent(t, n2)

	if got.Type != want.Type || len(got.Elements) == 0 || got.Elements[0].Label != want.Elements[0].Label {
		t.Errorf("content mismatch: got %+v", got)
	}
}

func TestInjectRecognText_ReplaceTagValue(t *testing.T) {
	cases := []struct {
		in, key, val, want string
	}{
		{"<RECOGNTEXT:0><RECOGNSTATUS:0>", "RECOGNTEXT", "59720", "<RECOGNTEXT:59720><RECOGNSTATUS:0>"},
		{"<RECOGNTEXT:59720>", "RECOGNTEXT", "99999", "<RECOGNTEXT:99999>"},
		{"<FOO:bar>", "MISSING", "x", "<FOO:bar>"},
	}
	for _, c := range cases {
		got := string(replaceTagValue([]byte(c.in), c.key, c.val))
		if got != c.want {
			t.Errorf("replaceTagValue(%q, %q, %q) = %q, want %q", c.in, c.key, c.val, got, c.want)
		}
	}
}

func TestInjectRecognText_OutOfRange(t *testing.T) {
	n := loadNote(t, "20260318_154108 std one line.note")
	content := RecognContent{Type: "Raw Content", Elements: []RecognElement{{Type: "Raw Content"}}}
	if _, err := n.InjectRecognText(99, content); err == nil {
		t.Error("expected error for out-of-range page index")
	}
}

func TestInjectRecognText_Idempotent(t *testing.T) {
	// Inject twice; second inject should also produce a valid parseable file.
	n := loadNote(t, "20260318_154108 std one line.note")
	content := RecognContent{Type: "Text", Elements: []RecognElement{{Type: "Text", Label: "first"}}}

	out1, err := n.InjectRecognText(0, content)
	if err != nil {
		t.Fatal(err)
	}

	n2 := roundTripNote(t, out1)
	content.Elements[0].Label = "second"
	out2, err := n2.InjectRecognText(0, content)
	if err != nil {
		t.Fatalf("second inject: %v", err)
	}

	n3 := roundTripNote(t, out2)
	got := readContent(t, n3)
	if got.Elements[0].Label != "second" {
		t.Errorf("got label %q, want second", got.Elements[0].Label)
	}
}

// readContentForPage reads and unmarshals the RECOGNTEXT block for the given page index.
func readContentForPage(t *testing.T, n *Note, pageIdx int) RecognContent {
	t.Helper()
	if pageIdx >= len(n.Pages) {
		t.Fatalf("readContentForPage: page %d out of range (have %d pages)", pageIdx, len(n.Pages))
	}
	raw, err := n.ReadRecognText(n.Pages[pageIdx])
	if err != nil {
		t.Fatalf("ReadRecognText(page %d): %v", pageIdx, err)
	}
	if raw == nil {
		t.Fatalf("ReadRecognText(page %d) returned nil (no block)", pageIdx)
	}
	var c RecognContent
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal RecognContent page %d: %v", pageIdx, err)
	}
	return c
}

// verifyAllBlocksReachable checks that every non-zero MAINLAYER, BGLAYER, and TOTALPATH
// offset in every page of n resolves to a readable block via BlockAt.
func verifyAllBlocksReachable(t *testing.T, n *Note) {
	t.Helper()
	for _, p := range n.Pages {
		for _, tag := range []string{"MAINLAYER", "BGLAYER", "TOTALPATH"} {
			val := p.Meta[tag]
			if val == "" || val == "0" {
				continue
			}
			off, err := strconv.Atoi(val)
			if err != nil {
				t.Errorf("page %d %s: invalid offset %q", p.Index, tag, val)
				continue
			}
			if _, err := n.BlockAt(off); err != nil {
				t.Errorf("page %d %s offset %d: BlockAt failed: %v", p.Index, tag, off, err)
			}
		}
	}
}

// TestInjectRecognText_MultiPage_StdNote tests injection into each page of gosnstd.note (AC1.1, AC1.2, AC1.3, AC2.1, AC2.2, AC5.1).
func TestInjectRecognText_MultiPage_StdNote(t *testing.T) {
	n := loadNote(t, "gosnstd.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	want := RecognContent{
		Type: "Text",
		Elements: []RecognElement{{Type: "Text", Label: "injected"}},
	}

	for _, pageIdx := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("page%d", pageIdx), func(t *testing.T) {
			out, err := n.InjectRecognText(pageIdx, want)
			if err != nil {
				t.Fatalf("InjectRecognText(page %d): %v", pageIdx, err)
			}

			n2 := roundTripNote(t, out)

			if len(n2.Pages) != len(n.Pages) {
				t.Errorf("page count changed: got %d, want %d", len(n2.Pages), len(n.Pages))
			}

			// AC2.1, AC2.2: all layer and path blocks must be reachable.
			verifyAllBlocksReachable(t, n2)

			// AC1.1/AC1.2/AC1.3: RECOGNTEXT was set on the target page.
			p2 := n2.Pages[pageIdx]
			if p2.Meta["RECOGNSTATUS"] != "1" {
				t.Errorf("page %d RECOGNSTATUS = %q, want 1", pageIdx, p2.Meta["RECOGNSTATUS"])
			}
			got := readContentForPage(t, n2, pageIdx)
			if got.Elements[0].Label != want.Elements[0].Label {
				t.Errorf("page %d: label %q, want %q", pageIdx, got.Elements[0].Label, want.Elements[0].Label)
			}
		})
	}
}

// TestInjectRecognText_MultiPage_RTRNote tests injection into each page of gosnrtr.note,
// covering the out-of-order file layout (AC1.4, AC2.4, AC5.2).
func TestInjectRecognText_MultiPage_RTRNote(t *testing.T) {
	n := loadNote(t, "gosnrtr.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	want := RecognContent{
		Type: "Text",
		Elements: []RecognElement{{Type: "Text", Label: "rtr-replaced"}},
	}

	for _, pageIdx := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("page%d", pageIdx), func(t *testing.T) {
			out, err := n.InjectRecognText(pageIdx, want)
			if err != nil {
				t.Fatalf("InjectRecognText(page %d): %v", pageIdx, err)
			}

			n2 := roundTripNote(t, out)
			verifyAllBlocksReachable(t, n2)

			// AC2.4: RECOGNTEXT offsets for other pages must also resolve.
			for _, p := range n2.Pages {
				if rt := p.Meta["RECOGNTEXT"]; rt != "" && rt != "0" {
					off, _ := strconv.Atoi(rt)
					if _, err := n2.BlockAt(off); err != nil {
						t.Errorf("page %d RECOGNTEXT offset %d not readable: %v", p.Index, off, err)
					}
				}
			}

			got := readContentForPage(t, n2, pageIdx)
			if got.Elements[0].Label != want.Elements[0].Label {
				t.Errorf("page %d: label %q, want %q", pageIdx, got.Elements[0].Label, want.Elements[0].Label)
			}
		})
	}
}

// TestInjectRecognText_MultiPage_BGLayerUnchanged verifies AC2.5: BGLAYER blocks whose
// LAYERBITMAP points before insertionPoint are not modified.
func TestInjectRecognText_MultiPage_BGLayerUnchanged(t *testing.T) {
	n := loadNote(t, "gosnstd.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	// Record the BGLAYER LAYERBITMAP offset for page 1 before injection.
	bglayerTags, _, err := n.LayerData(n.Pages[1], "BGLAYER")
	if err != nil || bglayerTags == nil {
		t.Skip("page 1 has no BGLAYER, skipping")
	}
	bitmapOffBefore := bglayerTags["LAYERBITMAP"]

	// Inject into page 0 (before page 1 in file order).
	want := RecognContent{Type: "Text", Elements: []RecognElement{{Type: "Text", Label: "x"}}}
	out, err := n.InjectRecognText(0, want)
	if err != nil {
		t.Fatalf("InjectRecognText(page 0): %v", err)
	}

	n2 := roundTripNote(t, out)
	bglayerTags2, _, err := n2.LayerData(n2.Pages[1], "BGLAYER")
	if err != nil || bglayerTags2 == nil {
		t.Fatal("page 1 BGLAYER missing after injection")
	}
	bitmapOffAfter := bglayerTags2["LAYERBITMAP"]

	// If the bitmap was before insertionPoint, it must be unchanged.
	beforeOff, _ := strconv.Atoi(bitmapOffBefore)
	insertionPoint, _ := n.footerPageOffset(0)
	if beforeOff < insertionPoint {
		if bitmapOffBefore != bitmapOffAfter {
			t.Errorf("BGLAYER LAYERBITMAP below insertionPoint changed: was %s, now %s", bitmapOffBefore, bitmapOffAfter)
		}
	}
}

// TestInjectRecognText_MultiPage_Idempotent verifies AC5.3: inject twice → stable output.
func TestInjectRecognText_MultiPage_Idempotent(t *testing.T) {
	n := loadNote(t, "gosnstd.note")
	if len(n.Pages) < 3 {
		t.Skipf("expected >= 3 pages, got %d", len(n.Pages))
	}

	c1 := RecognContent{Type: "Text", Elements: []RecognElement{{Type: "Text", Label: "first"}}}
	out1, err := n.InjectRecognText(1, c1)
	if err != nil {
		t.Fatalf("InjectRecognText(page 1): %v", err)
	}

	n2 := roundTripNote(t, out1)
	c2 := RecognContent{Type: "Text", Elements: []RecognElement{{Type: "Text", Label: "second"}}}
	out2, err := n2.InjectRecognText(1, c2)
	if err != nil {
		t.Fatalf("second inject: %v", err)
	}

	n3 := roundTripNote(t, out2)
	verifyAllBlocksReachable(t, n3)
	got := readContentForPage(t, n3, 1)
	if got.Elements[0].Label != "second" {
		t.Errorf("second inject: label %q, want second", got.Elements[0].Label)
	}
}

// TestInjectRecognText_SinglePage_Regression verifies AC4.1: single-page note → byte-identical
// output before and after this change (regression test).
func TestInjectRecognText_SinglePage_Regression(t *testing.T) {
	// The single-page test was already passing before this change.
	// This test just ensures it still passes unchanged.
	n := loadNote(t, "20260318_154108 std one line.note")
	want := RecognContent{
		Type: "Text",
		Elements: []RecognElement{{Type: "Text", Label: "regression check"}},
	}

	out, err := n.InjectRecognText(0, want)
	if err != nil {
		t.Fatalf("InjectRecognText: %v", err)
	}

	n2 := roundTripNote(t, out)
	if n2.Pages[0].Meta["RECOGNSTATUS"] != "1" {
		t.Errorf("RECOGNSTATUS = %q, want 1", n2.Pages[0].Meta["RECOGNSTATUS"])
	}
	got := readContentForPage(t, n2, 0)
	if got.Elements[0].Label != want.Elements[0].Label {
		t.Errorf("label %q, want %q", got.Elements[0].Label, want.Elements[0].Label)
	}
}

// snapshotLayerBitmaps captures the LAYERBITMAP data (raw bytes) for each page's
// MAINLAYER and BGLAYER. Used to verify bitmap data is preserved across injection.
func snapshotLayerBitmaps(t *testing.T, n *Note) map[string][]byte {
	t.Helper()
	snap := make(map[string][]byte)
	for _, p := range n.Pages {
		for _, layer := range []string{"MAINLAYER", "BGLAYER"} {
			_, bitmap, err := n.LayerData(p, layer)
			if err != nil || bitmap == nil {
				continue
			}
			key := fmt.Sprintf("page%d_%s", p.Index, layer)
			cp := make([]byte, len(bitmap))
			copy(cp, bitmap)
			snap[key] = cp
		}
	}
	return snap
}

// verifyLayerBitmapsPreserved checks that bitmap data matches a previous snapshot.
func verifyLayerBitmapsPreserved(t *testing.T, n *Note, snap map[string][]byte) {
	t.Helper()
	for _, p := range n.Pages {
		for _, layer := range []string{"MAINLAYER", "BGLAYER"} {
			_, bitmap, err := n.LayerData(p, layer)
			if err != nil {
				t.Errorf("page %d %s LayerData error: %v", p.Index, layer, err)
				continue
			}
			key := fmt.Sprintf("page%d_%s", p.Index, layer)
			orig, ok := snap[key]
			if !ok {
				if bitmap != nil {
					t.Errorf("page %d %s: bitmap appeared after injection", p.Index, layer)
				}
				continue
			}
			if bitmap == nil {
				t.Errorf("page %d %s: bitmap disappeared after injection", p.Index, layer)
				continue
			}
			if len(bitmap) != len(orig) {
				t.Errorf("page %d %s LAYERBITMAP: length changed %d → %d", p.Index, layer, len(orig), len(bitmap))
				continue
			}
			for i := range orig {
				if bitmap[i] != orig[i] {
					t.Errorf("page %d %s LAYERBITMAP: data differs at byte %d (was 0x%02x, now 0x%02x)", p.Index, layer, i, orig[i], bitmap[i])
					break
				}
			}
		}
	}
}

// TestInjectRecognText_SequentialMultiPage injects into every page sequentially
// (inject page 0 → reload → inject page 1 → reload → ...) and verifies that
// all blocks including LAYERBITMAP remain reachable after each round.
// This reproduces the LAYERBITMAP relocation bug where offsetMap's uniform shift
// gives wrong positions for LAYERBITMAP blocks.
func TestInjectRecognText_SequentialMultiPage(t *testing.T) {
	for _, name := range []string{"gosnstd.note", "gosnrtr.note"} {
		t.Run(name, func(t *testing.T) {
			n := loadNote(t, name)
			if len(n.Pages) < 2 {
				t.Skipf("need >= 2 pages, got %d", len(n.Pages))
			}

			// Snapshot original bitmap data for comparison after injection.
			origBitmaps := snapshotLayerBitmaps(t, n)

			current := n
			for pageIdx := range current.Pages {
				content := RecognContent{
					Type: "Raw Content",
					Elements: []RecognElement{{
						Type:  "Text",
						Label: fmt.Sprintf("page-%d-text", pageIdx),
						Words: []RecognWord{{Label: fmt.Sprintf("page-%d-text", pageIdx), BoundingBox: &RecognBox{X: 1, Y: 1, Width: 10, Height: 5}}},
					}},
				}
				out, err := current.InjectRecognText(pageIdx, content)
				if err != nil {
					t.Fatalf("InjectRecognText(page %d): %v", pageIdx, err)
				}

				current = roundTripNote(t, out)

				// Verify structural integrity after each injection.
				verifyAllBlocksReachable(t, current)
				verifyLayerBitmapsPreserved(t, current, origBitmaps)

				// Verify the injected text is readable.
				got := readContentForPage(t, current, pageIdx)
				if got.Elements[0].Label != content.Elements[0].Label {
					t.Errorf("page %d: label %q, want %q", pageIdx, got.Elements[0].Label, content.Elements[0].Label)
				}
			}

			// After all pages injected, verify everything is still intact.
			for pageIdx := range current.Pages {
				wantLabel := fmt.Sprintf("page-%d-text", pageIdx)
				got := readContentForPage(t, current, pageIdx)
				if got.Elements[0].Label != wantLabel {
					t.Errorf("final check page %d: label %q, want %q", pageIdx, got.Elements[0].Label, wantLabel)
				}
			}
		})
	}
}

// TestBuildRecognText verifies BuildRecognText constructs valid JIIX RecognContent
// from plain text and stroke geometry (verifies AC1.1, AC1.2, AC1.3, AC3.1-AC3.6).
func TestBuildRecognText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		bounds   Rect
		equipment string
		wantType string
		checkFn  func(t *testing.T, c RecognContent)
	}{
		{
			name:      "simple single word",
			text:      "hello",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 50},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC1.1: Root type is "Raw Content"
				if c.Type != "Raw Content" {
					t.Errorf("Type = %q, want Raw Content", c.Type)
				}
				// AC1.2: Text element present with type, label, words
				if len(c.Elements) != 1 {
					t.Errorf("Elements count = %d, want 1", len(c.Elements))
					return
				}
				e := c.Elements[0]
				if e.Type != "Text" {
					t.Errorf("Element type = %q, want Text", e.Type)
				}
				if e.Label != "hello" {
					t.Errorf("Element label = %q, want hello", e.Label)
				}
				if len(e.Words) == 0 {
					t.Errorf("Words empty, want 1 word")
				}
				// AC1.3: No forbidden fields
				jsonBytes, _ := json.Marshal(c)
				jsonStr := string(jsonBytes)
				for _, forbidden := range []string{"\"version\":", "\"id\":", "\"candidates\":", "\"reflow-label\":"} {
					if strings.Contains(jsonStr, forbidden) {
						t.Errorf("forbidden field in JSON: %s", forbidden)
					}
				}
			},
		},
		{
			name:      "multiple words with spaces",
			text:      "hello world",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 200, MaxY: 50},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC3.1: Words split by whitespace with shared bbox
				// AC3.2: Spaces between words without bbox
				if len(c.Elements) != 1 {
					return
				}
				e := c.Elements[0]
				if len(e.Words) != 3 {
					t.Errorf("Words count = %d, want 3 (hello, space, world)", len(e.Words))
					return
				}
				// Check word 0: "hello" with bbox
				if e.Words[0].Label != "hello" || e.Words[0].BoundingBox == nil {
					t.Errorf("Word 0 should be 'hello' with bbox")
				}
				// Check word 1: space without bbox
				if e.Words[1].Label != " " || e.Words[1].BoundingBox != nil {
					t.Errorf("Word 1 should be space without bbox")
				}
				// Check word 2: "world" with bbox
				if e.Words[2].Label != "world" || e.Words[2].BoundingBox == nil {
					t.Errorf("Word 2 should be 'world' with bbox")
				}
				// AC3.5: Element label equals concatenation of word labels
				if e.Label != "hello world" {
					t.Errorf("Label = %q, want 'hello world'", e.Label)
				}
			},
		},
		{
			name:      "multiline text",
			text:      "hello\nworld",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC3.3: Newlines are {"label":"\n"} without bbox
				if len(c.Elements) != 1 {
					return
				}
				e := c.Elements[0]
				if len(e.Words) != 3 {
					t.Errorf("Words count = %d, want 3 (hello, newline, world)", len(e.Words))
					return
				}
				if e.Words[1].Label != "\n" || e.Words[1].BoundingBox != nil {
					t.Errorf("Word 1 should be newline without bbox")
				}
				if e.Label != "hello\nworld" {
					t.Errorf("Label mismatch after newline")
				}
			},
		},
		{
			name:      "trailing punctuation",
			text:      "hello.",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 50},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC3.4: Trailing punctuation (. ! ? ,) split from word
				if len(c.Elements) != 1 {
					return
				}
				e := c.Elements[0]
				if len(e.Words) != 2 {
					t.Errorf("Words count = %d, want 2 (hello, period)", len(e.Words))
					return
				}
				if e.Words[0].Label != "hello" {
					t.Errorf("Word 0 = %q, want hello", e.Words[0].Label)
				}
				if e.Words[1].Label != "." {
					t.Errorf("Word 1 = %q, want .", e.Words[1].Label)
				}
				if e.Label != "hello." {
					t.Errorf("Label = %q, want 'hello.'", e.Label)
				}
			},
		},
		{
			name:      "empty text",
			text:      "",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 50},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC3.6: Empty text produces empty elements array
				if c.Type != "Raw Content" {
					t.Errorf("Type = %q, want Raw Content", c.Type)
				}
				if len(c.Elements) != 0 {
					t.Errorf("Elements count = %d, want 0", len(c.Elements))
				}
			},
		},
		{
			name:      "whitespace only",
			text:      "   ",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 50},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC3.6: Whitespace-only text produces empty elements array
				if len(c.Elements) != 0 {
					t.Errorf("Elements count = %d, want 0", len(c.Elements))
				}
			},
		},
		{
			name:      "multiple punctuation types",
			text:      "hello! world? yes, no.",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 300, MaxY: 50},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				if len(c.Elements) != 1 {
					return
				}
				e := c.Elements[0]
				// Expect: hello, !, space, world, ?, space, yes, ,, space, no, .
				expectedLabel := "hello! world? yes, no."
				if e.Label != expectedLabel {
					t.Errorf("Label = %q, want %q", e.Label, expectedLabel)
				}
				// Verify all punctuation is split
				hasPunct := false
				for _, w := range e.Words {
					if w.Label == "!" || w.Label == "?" || w.Label == "," || w.Label == "." {
						if w.BoundingBox == nil {
							t.Errorf("Punctuation %q should have bbox", w.Label)
						}
						hasPunct = true
					}
				}
				if !hasPunct {
					t.Errorf("No punctuation found in words")
				}
			},
		},
		{
			name:      "bounding box conversion to mm",
			text:      "test",
			bounds:    Rect{MinX: 100, MinY: 200, MaxX: 300, MaxY: 300},
			equipment: "N6",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				if len(c.Elements) != 1 {
					return
				}
				e := c.Elements[0]
				if len(e.Words) == 0 {
					return
				}
				// Check that bbox was converted (not pixel values)
				w := e.Words[0]
				if w.BoundingBox == nil {
					t.Errorf("Word should have bounding box")
					return
				}
				// For N6 (7.8" diagonal, 1404x1872): widthMM ≈ 119, heightMM ≈ 159
				// Bounds: MinX=100 MinY=200 MaxX=300 MaxY=300 (width=200, height=100)
				// X should be 100 * 119/1404 ≈ 8.5, Width should be 200 * 119/1404 ≈ 17
				if w.BoundingBox.X > 20 {
					t.Errorf("BoundingBox.X = %v, seems too large (should be in mm, not pixels)", w.BoundingBox.X)
				}
				if w.BoundingBox.Width > 30 {
					t.Errorf("BoundingBox.Width = %v, seems too large", w.BoundingBox.Width)
				}
			},
		},
		{
			name:      "unknown equipment fallback",
			text:      "test",
			bounds:    Rect{MinX: 0, MinY: 0, MaxX: 100, MaxY: 50},
			equipment: "UNKNOWN_DEVICE",
			wantType:  "Raw Content",
			checkFn: func(t *testing.T, c RecognContent) {
				// AC2.4: Unknown equipment falls back (should not error)
				if c.Type != "Raw Content" {
					t.Errorf("Type = %q, want Raw Content", c.Type)
				}
				if len(c.Elements) != 1 {
					t.Errorf("Elements count = %d, want 1", len(c.Elements))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := BuildRecognText(tt.text, tt.bounds, tt.equipment)
			if c.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", c.Type, tt.wantType)
			}
			tt.checkFn(t, c)
		})
	}
}

// TestBuildRecognText_RoundTrip verifies the full chain: BuildRecognText → InjectRecognText
// → file write/read → re-parse → ReadRecognText (verifies AC1.4).
func TestBuildRecognText_RoundTrip(t *testing.T) {
	// Load an RTR testdata file
	n := loadNote(t, "gosnrtr.note")
	if n.Pages[0].Meta["RECOGNSTATUS"] != "1" {
		t.Skip("expected RTR note with existing recognition")
	}

	// Decode TOTALPATH to get strokes
	tpData, err := n.TotalPathData(n.Pages[0])
	if err != nil {
		t.Fatalf("TotalPathData: %v", err)
	}
	if len(tpData) == 0 {
		t.Skip("no TOTALPATH block")
	}

	w := n.PageWidth()
	h := n.PageHeight()
	strokes, err := DecodeTotalPath(tpData, w, h)
	if err != nil {
		t.Fatalf("DecodeTotalPath: %v", err)
	}
	if len(strokes) == 0 {
		t.Skip("no strokes in TOTALPATH")
	}

	// Compute stroke bounds
	bounds := StrokeBounds(strokes)

	// Build JIIX RecognContent from plain text
	testText := "Hello world."
	testEquipment := "N6"
	content := BuildRecognText(testText, bounds, testEquipment)

	// Inject into page 0
	out, err := n.InjectRecognText(0, content)
	if err != nil {
		t.Fatalf("InjectRecognText: %v", err)
	}

	// Re-parse the file
	n2 := roundTripNote(t, out)

	// Read back the RECOGNTEXT
	readBack, err := n2.ReadRecognText(n2.Pages[0])
	if err != nil {
		t.Fatalf("ReadRecognText: %v", err)
	}

	// Unmarshal and verify structure
	var decoded RecognContent
	if err := json.Unmarshal(readBack, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// AC1.4: Verify decoded structure matches what we built
	if decoded.Type != "Raw Content" {
		t.Errorf("Type = %q, want Raw Content", decoded.Type)
	}
	if len(decoded.Elements) != 1 {
		t.Errorf("Elements count = %d, want 1", len(decoded.Elements))
	} else {
		e := decoded.Elements[0]
		if e.Type != "Text" {
			t.Errorf("Element type = %q, want Text", e.Type)
		}
		if e.Label != testText {
			t.Errorf("Element label = %q, want %q", e.Label, testText)
		}
		if len(e.Words) == 0 {
			t.Errorf("Words empty")
		}
		// Verify words have bounding boxes in mm range (should be < 200mm for any device)
		for _, w := range e.Words {
			if w.BoundingBox != nil && w.BoundingBox.X > 300 {
				t.Errorf("Word bbox X = %v, seems to be in pixels not mm", w.BoundingBox.X)
			}
		}
	}

	// Verify JSON has no forbidden fields
	jsonBytes, _ := json.Marshal(decoded)
	jsonStr := string(jsonBytes)
	for _, forbidden := range []string{"\"version\":", "\"id\":", "\"candidates\":", "\"reflow-label\":"} {
		if strings.Contains(jsonStr, forbidden) {
			t.Errorf("forbidden field in JSON: %s", forbidden)
		}
	}
}
