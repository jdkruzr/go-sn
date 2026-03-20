// Package note parses the .note file format used by Supernote devices.
//
// File layout:
//
//	[0:24]   magic: "noteSN_FILE_VER_20230015"
//	[24:28]  header block length (LE uint32)
//	[28:28+L] header metadata: <KEY:VALUE> tags
//	...      data blocks (each: 4-byte LE length + data)
//	[footer_start:] footer metadata: <KEY:VALUE> tags
//	[end-8:] "tail" + LE uint32 footer_start
package note

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
)

const magic = "noteSN_FILE_VER_20230015"

var tagRE = regexp.MustCompile(`<([^:<>]+):(.*?)>`)

// Note represents a parsed .note file.
type Note struct {
	Header Tags
	Pages  []*Page
	raw    []byte
}

// Page represents one page of a note.
type Page struct {
	Index  int
	Meta   Tags
	raw    []byte
}

// Tags is a map of key→value from <KEY:VALUE> metadata blocks.
type Tags map[string]string

// Load reads and parses a .note file from r.
func Load(r io.Reader) (*Note, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return parse(raw)
}

func parse(raw []byte) (*Note, error) {
	if len(raw) < 32 {
		return nil, fmt.Errorf("file too short")
	}
	if string(raw[:24]) != magic {
		return nil, fmt.Errorf("bad magic: %q", string(raw[:24]))
	}

	// Header block immediately after magic
	hdrLen := int(binary.LittleEndian.Uint32(raw[24:28]))
	if 28+hdrLen > len(raw) {
		return nil, fmt.Errorf("header block exceeds file size")
	}
	hdr := parseTags(raw[28 : 28+hdrLen])

	// Footer: last 8 bytes are "tail" + LE uint32 footer offset
	if len(raw) < 8 || string(raw[len(raw)-8:len(raw)-4]) != "tail" {
		return nil, fmt.Errorf("missing tail marker")
	}
	footerOff := int(binary.LittleEndian.Uint32(raw[len(raw)-4:]))
	if footerOff+4 > len(raw) {
		return nil, fmt.Errorf("footer offset out of bounds")
	}
	footerLen := int(binary.LittleEndian.Uint32(raw[footerOff:]))
	if footerOff+4+footerLen > len(raw) {
		return nil, fmt.Errorf("footer block exceeds file size")
	}
	footer := parseTags(raw[footerOff+4 : footerOff+4+footerLen])

	n := &Note{Header: hdr, raw: raw}

	// Collect pages: PAGE1, PAGE2, ... from footer
	for i := 1; ; i++ {
		key := fmt.Sprintf("PAGE%d", i)
		val, ok := footer[key]
		if !ok {
			break
		}
		off, err := strconv.Atoi(val)
		if err != nil || off <= 0 || off+4 > len(raw) {
			break
		}
		p, err := parsePage(raw, off, i-1)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i, err)
		}
		n.Pages = append(n.Pages, p)
	}

	return n, nil
}

func parsePage(raw []byte, off, idx int) (*Page, error) {
	if off+4 > len(raw) {
		return nil, fmt.Errorf("page offset out of bounds")
	}
	metaLen := int(binary.LittleEndian.Uint32(raw[off:]))
	if off+4+metaLen > len(raw) {
		return nil, fmt.Errorf("page metadata exceeds file size")
	}
	meta := parseTags(raw[off+4 : off+4+metaLen])
	return &Page{Index: idx, Meta: meta, raw: raw}, nil
}

// BlockAt reads the data block at the given file offset.
// Returns the raw bytes of the block body (after the 4-byte length prefix).
func (n *Note) BlockAt(off int) ([]byte, error) {
	if off == 0 {
		return nil, nil
	}
	if off+4 > len(n.raw) {
		return nil, fmt.Errorf("block offset %d out of bounds", off)
	}
	blen := int(binary.LittleEndian.Uint32(n.raw[off:]))
	if off+4+blen > len(n.raw) {
		return nil, fmt.Errorf("block at %d length %d exceeds file", off, blen)
	}
	return n.raw[off+4 : off+4+blen], nil
}

// FooterTags returns the key→value tags from the file footer.
func (n *Note) FooterTags() (Tags, error) {
	if len(n.raw) < 8 {
		return nil, fmt.Errorf("file too short")
	}
	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	if footerOff+4 > len(n.raw) {
		return nil, fmt.Errorf("footer offset out of bounds")
	}
	footerLen := int(binary.LittleEndian.Uint32(n.raw[footerOff:]))
	if footerOff+4+footerLen > len(n.raw) {
		return nil, fmt.Errorf("footer block exceeds file size")
	}
	return parseTags(n.raw[footerOff+4 : footerOff+4+footerLen]), nil
}

// TotalPathData returns the raw TOTALPATH block bytes for the given page.
func (n *Note) TotalPathData(p *Page) ([]byte, error) {
	val, ok := p.Meta["TOTALPATH"]
	if !ok || val == "0" {
		return nil, nil
	}
	off, err := strconv.Atoi(val)
	if err != nil {
		return nil, fmt.Errorf("invalid TOTALPATH offset: %w", err)
	}
	return n.BlockAt(off)
}

// LayerData returns the raw layer bitmap bytes for the named layer.
// The layer header (metadata tags) is returned separately; the bitmap
// follows the header within the same block, and its start offset is
// given by the LAYERBITMAP tag within the header (which points to
// a separate block in the file for shared bitmaps like BGLAYER).
func (n *Note) LayerData(p *Page, layerName string) (Tags, []byte, error) {
	val, ok := p.Meta[layerName]
	if !ok || val == "0" {
		return nil, nil, nil
	}
	off, err := strconv.Atoi(val)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid %s offset: %w", layerName, err)
	}
	if off+4 > len(n.raw) {
		return nil, nil, fmt.Errorf("%s block out of bounds", layerName)
	}
	metaLen := int(binary.LittleEndian.Uint32(n.raw[off:]))
	if off+4+metaLen > len(n.raw) {
		return nil, nil, fmt.Errorf("%s metadata exceeds file", layerName)
	}
	meta := parseTags(n.raw[off+4 : off+4+metaLen])

	// LAYERBITMAP points to the actual bitmap block
	bitmapVal, ok := meta["LAYERBITMAP"]
	if !ok || bitmapVal == "0" {
		return meta, nil, nil
	}
	bitmapOff, err := strconv.Atoi(bitmapVal)
	if err != nil {
		return meta, nil, fmt.Errorf("invalid LAYERBITMAP offset: %w", err)
	}
	bitmap, err := n.BlockAt(bitmapOff)
	if err != nil {
		return meta, nil, err
	}
	return meta, bitmap, nil
}

// RecognText returns the base64-encoded recognition text for the page, or "" if absent.
func (p *Page) RecognText() string {
	val, ok := p.Meta["RECOGNTEXT"]
	if !ok || val == "0" {
		return ""
	}
	return val
}

// PageWidth returns the pixel width of this note's device in portrait orientation.
// For orientation-aware dimensions, use PageDimensions.
func (n *Note) PageWidth() int {
	return devicePortraitWidth(n.Header["APPLY_EQUIPMENT"])
}

// PageHeight returns the pixel height of this note's device in portrait orientation.
// For orientation-aware dimensions, use PageDimensions.
func (n *Note) PageHeight() int {
	return devicePortraitHeight(n.Header["APPLY_EQUIPMENT"])
}

// PageDimensions returns the pixel width and height for a specific page,
// accounting for landscape orientation (ORIENTATION=1090 in page metadata).
func (n *Note) PageDimensions(p *Page) (w, h int) {
	eq := n.Header["APPLY_EQUIPMENT"]
	return p.Width(eq), p.Height(eq)
}

// Width returns the pixel width of this page, accounting for landscape orientation.
// ORIENTATION meta tag: 1000=portrait (default), 1090=landscape.
func (p *Page) Width(equipment string) int {
	if p.Meta["ORIENTATION"] == "1090" {
		return devicePortraitHeight(equipment)
	}
	return devicePortraitWidth(equipment)
}

// Height returns the pixel height of this page, accounting for landscape orientation.
// ORIENTATION meta tag: 1000=portrait (default), 1090=landscape.
func (p *Page) Height(equipment string) int {
	if p.Meta["ORIENTATION"] == "1090" {
		return devicePortraitWidth(equipment)
	}
	return devicePortraitHeight(equipment)
}

func devicePortraitWidth(equipment string) int {
	switch equipment {
	case "Manta":
		return 1920
	default: // N6, A5X, etc.
		return 1404
	}
}

func devicePortraitHeight(equipment string) int {
	switch equipment {
	case "Manta":
		return 2560
	default:
		return 1872
	}
}

func parseTags(b []byte) Tags {
	t := make(Tags)
	for _, m := range tagRE.FindAllSubmatch(b, -1) {
		t[string(m[1])] = string(m[2])
	}
	return t
}

// devicePhysicalMM returns the physical display dimensions in millimeters
// for the given equipment string. Used for pixel-to-mm coordinate conversion
// in JIIX RECOGNTEXT bounding boxes.
//
// Dimensions are derived from display diagonal and pixel aspect ratio.
// Unknown equipment strings fall back to Nomad (N6) dimensions.
func devicePhysicalMM(equipment string) (widthMM, heightMM float64) {
	switch equipment {
	case "Manta":
		// 10.67" diagonal, 1920×2560 pixels
		return physicalFromDiag(10.67, 1920, 2560)
	case "A5X", "A5_X":
		// 10.3" diagonal, 1404×1872 pixels
		return physicalFromDiag(10.3, 1404, 1872)
	default:
		// N6 (Nomad): 7.8" diagonal, 1404×1872 pixels
		// Unknown devices also fall back to N6.
		return physicalFromDiag(7.8, 1404, 1872)
	}
}

func physicalFromDiag(diagInches float64, pxW, pxH int) (float64, float64) {
	diagPx := math.Sqrt(float64(pxW*pxW + pxH*pxH))
	mmPerPx := diagInches * 25.4 / diagPx
	return float64(pxW) * mmPerPx, float64(pxH) * mmPerPx
}
