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
