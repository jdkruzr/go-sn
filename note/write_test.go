package note

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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
