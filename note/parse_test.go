package note

import "testing"

func TestPageDimensions_Portrait(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "A5X"}}
	p := &Page{Meta: Tags{"ORIENTATION": "1000"}}
	w, h := n.PageDimensions(p)
	if w != 1404 || h != 1872 {
		t.Errorf("A5X portrait: got %dx%d, want 1404x1872", w, h)
	}
}

func TestPageDimensions_Landscape(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "A5X"}}
	p := &Page{Meta: Tags{"ORIENTATION": "1090"}}
	w, h := n.PageDimensions(p)
	if w != 1872 || h != 1404 {
		t.Errorf("A5X landscape: got %dx%d, want 1872x1404", w, h)
	}
}

func TestPageDimensions_MantaPortrait(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "Manta"}}
	p := &Page{Meta: Tags{"ORIENTATION": "1000"}}
	w, h := n.PageDimensions(p)
	if w != 1920 || h != 2560 {
		t.Errorf("Manta portrait: got %dx%d, want 1920x2560", w, h)
	}
}

func TestPageDimensions_MantaLandscape(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "Manta"}}
	p := &Page{Meta: Tags{"ORIENTATION": "1090"}}
	w, h := n.PageDimensions(p)
	if w != 2560 || h != 1920 {
		t.Errorf("Manta landscape: got %dx%d, want 2560x1920", w, h)
	}
}

func TestPageDimensions_NoOrientation(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "N6"}}
	p := &Page{Meta: Tags{}}
	w, h := n.PageDimensions(p)
	if w != 1404 || h != 1872 {
		t.Errorf("N6 no orientation tag: got %dx%d, want 1404x1872 (default portrait)", w, h)
	}
}

func TestPageDimensions_MixedOrientations(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "A5X"}}
	portrait := &Page{Meta: Tags{"ORIENTATION": "1000"}}
	landscape := &Page{Meta: Tags{"ORIENTATION": "1090"}}

	pw, ph := n.PageDimensions(portrait)
	lw, lh := n.PageDimensions(landscape)

	if pw != 1404 || ph != 1872 {
		t.Errorf("portrait page: got %dx%d, want 1404x1872", pw, ph)
	}
	if lw != 1872 || lh != 1404 {
		t.Errorf("landscape page: got %dx%d, want 1872x1404", lw, lh)
	}
	// Width and height should be swapped between orientations
	if pw != lh || ph != lw {
		t.Error("landscape dimensions should be portrait dimensions swapped")
	}
}

func TestPageWidth_BackwardsCompatible(t *testing.T) {
	n := &Note{Header: Tags{"APPLY_EQUIPMENT": "A5X"}}
	if n.PageWidth() != 1404 {
		t.Errorf("PageWidth() = %d, want 1404", n.PageWidth())
	}
	if n.PageHeight() != 1872 {
		t.Errorf("PageHeight() = %d, want 1872", n.PageHeight())
	}
}

// TestDevicePhysicalMM verifies AC2.2 and AC2.4: devicePhysicalMM returns
// correct physical dimensions for known devices and falls back to N6 for unknowns.
func TestDevicePhysicalMM(t *testing.T) {
	tests := []struct {
		name      string
		equipment string
		wantWMM   float64 // within ±2mm tolerance
		wantHMM   float64
	}{
		{
			name:      "AC2.2: N6 dimensions",
			equipment: "N6",
			wantWMM:   119, // 7.8" diagonal, 1404×1872 pixels → ~119mm × ~158mm
			wantHMM:   158,
		},
		{
			name:      "AC2.2: Manta dimensions",
			equipment: "Manta",
			wantWMM:   163, // 10.67" diagonal, 1920×2560 pixels → ~163mm × ~217mm
			wantHMM:   217,
		},
		{
			name:      "AC2.2: A5X dimensions",
			equipment: "A5X",
			wantWMM:   156, // 10.3" diagonal, 1404×1872 pixels → ~156mm × ~208mm
			wantHMM:   208,
		},
		{
			name:      "AC2.2: A5_X variant",
			equipment: "A5_X",
			wantWMM:   156,
			wantHMM:   208,
		},
		{
			name:      "AC2.4: Unknown device falls back to N6",
			equipment: "UNKNOWN_DEVICE",
			wantWMM:   119,
			wantHMM:   158,
		},
		{
			name:      "AC2.4: Empty equipment falls back to N6",
			equipment: "",
			wantWMM:   119,
			wantHMM:   158,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wMM, hMM := devicePhysicalMM(tt.equipment)
			// Allow ±2mm tolerance due to floating-point arithmetic
			if wMM < tt.wantWMM-2 || wMM > tt.wantWMM+2 {
				t.Errorf("width = %.1f mm, want %.1f ± 2", wMM, tt.wantWMM)
			}
			if hMM < tt.wantHMM-2 || hMM > tt.wantHMM+2 {
				t.Errorf("height = %.1f mm, want %.1f ± 2", hMM, tt.wantHMM)
			}
		})
	}
}
