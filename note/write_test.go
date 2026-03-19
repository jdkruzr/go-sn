package note

import (
	"encoding/json"
	"os"
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
