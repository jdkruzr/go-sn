package note

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

// ReadRecognText reads and base64-decodes the RECOGNTEXT block for the given page.
// Returns nil, nil if the page has no recognition text (offset == 0).
func (n *Note) ReadRecognText(p *Page) ([]byte, error) {
	val, ok := p.Meta["RECOGNTEXT"]
	if !ok || val == "0" {
		return nil, nil
	}
	off, err := strconv.Atoi(val)
	if err != nil {
		return nil, fmt.Errorf("invalid RECOGNTEXT offset %q: %w", val, err)
	}
	block, err := n.BlockAt(off)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(string(block))
	if err != nil {
		return nil, fmt.Errorf("base64 decode RECOGNTEXT block: %w", err)
	}
	return decoded, nil
}

// RecognContent is the top-level RECOGNTEXT JSON structure (MyScript iink format).
type RecognContent struct {
	Type     string          `json:"type"`
	Elements []RecognElement `json:"elements"`
}

// RecognElement is one recognition element (e.g. a word or line).
type RecognElement struct {
	Type        string          `json:"type"`
	Label       string          `json:"label,omitempty"`
	BoundingBox *RecognBox      `json:"bounding-box,omitempty"`
	Items       []RecognElement `json:"items,omitempty"`
}

// RecognBox is a bounding box in RECOGNTEXT coordinates (device pixels).
type RecognBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// InjectRecognText replaces or inserts the RECOGNTEXT block for the given page.
// Sets RECOGNSTATUS=1 and updates the RECOGNTEXT offset in page metadata.
// Returns new file bytes suitable for writing to disk.
//
// The page's metadata block must be the last block before the footer (which is
// always true for single-page notes and the final page of multi-page notes).
// Any previous RECOGNTEXT block is left as dead space; only the pointer changes.
func (n *Note) InjectRecognText(pageIdx int, content RecognContent) ([]byte, error) {
	if pageIdx < 0 || pageIdx >= len(n.Pages) {
		return nil, fmt.Errorf("page index %d out of range [0,%d)", pageIdx, len(n.Pages))
	}
	p := n.Pages[pageIdx]

	// Locate page meta block in the raw file.
	pageMetaOff, err := n.footerPageOffset(pageIdx)
	if err != nil {
		return nil, err
	}
	if pageMetaOff+4 > len(n.raw) {
		return nil, fmt.Errorf("page %d meta offset %d out of bounds", pageIdx, pageMetaOff)
	}
	pageMetaLen := int(binary.LittleEndian.Uint32(n.raw[pageMetaOff:]))
	if pageMetaOff+4+pageMetaLen > len(n.raw) {
		return nil, fmt.Errorf("page %d meta block exceeds file size", pageIdx)
	}

	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	if footerOff+4 > len(n.raw) {
		return nil, fmt.Errorf("footer offset out of bounds")
	}
	footerLen := int(binary.LittleEndian.Uint32(n.raw[footerOff:]))
	if footerOff+4+footerLen > len(n.raw) {
		return nil, fmt.Errorf("footer block exceeds file size")
	}

	// Build new RECOGNTEXT block: [4-byte LE length][base64(json)].
	jsonBytes, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("marshal RecognContent: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(jsonBytes)
	recognBlock := appendBlock(nil, []byte(b64))

	// insertionPoint is where the new block is inserted (immediately before the
	// target page's metadata block). Everything from this offset onward shifts.
	insertionPoint := pageMetaOff

	// Determine if any page metadata blocks exist past insertionPoint (file-offset order).
	hasSubsequent := false
	for i := range n.Pages {
		if i == pageIdx {
			continue
		}
		off, err := n.footerPageOffset(i)
		if err == nil && off > insertionPoint {
			hasSubsequent = true
			break
		}
	}

	if !hasSubsequent {
		// Fast path: single-page note or injecting into the last page (by file offset).
		// No subsequent page metadata blocks need relocation.
		newRecognOff := pageMetaOff
		newPageMetaOff := newRecognOff + len(recognBlock)

		oldMeta := n.raw[pageMetaOff+4 : pageMetaOff+4+pageMetaLen]
		newMeta := replaceTagValue(oldMeta, "RECOGNTEXT", strconv.Itoa(newRecognOff))
		newMeta = replaceTagValue(newMeta, "RECOGNSTATUS", "1")
		_ = p

		oldFooter := n.raw[footerOff+4 : footerOff+4+footerLen]
		newFooter := replaceTagValue(oldFooter, fmt.Sprintf("PAGE%d", pageIdx+1), strconv.Itoa(newPageMetaOff))

		newFooterOff := newPageMetaOff + 4 + len(newMeta)

		var out []byte
		out = append(out, n.raw[:pageMetaOff]...)
		out = append(out, recognBlock...)
		out = append(out, appendBlock(nil, newMeta)...)
		out = append(out, appendBlock(nil, newFooter)...)
		out = append(out, 't', 'a', 'i', 'l')
		out = binary.LittleEndian.AppendUint32(out, uint32(newFooterOff))
		return out, nil
	}

	// Multi-page path: relocate all offsets past insertionPoint.
	affectedOffsets := collectOffsets(n, insertionPoint)
	finalShift := computeShift(len(recognBlock), affectedOffsets)
	offsetMap := buildOffsetMap(affectedOffsets, finalShift)
	updateSet := buildUpdateSet(n, insertionPoint)

	// Layout invariant guard: every data-block offset referenced by a subsequent
	// page's metadata (MAINLAYER, BGLAYER, TOTALPATH) that is > insertionPoint
	// must be reachable by walking block boundaries forward from insertionPoint.
	reachable := make(map[int]bool)
	for off := insertionPoint; off < footerOff; {
		reachable[off] = true
		if off+4 > len(n.raw) {
			break
		}
		blen := int(binary.LittleEndian.Uint32(n.raw[off:]))
		off += 4 + blen
	}
	for i := range n.Pages {
		pageOff, err := n.footerPageOffset(i)
		if err != nil || pageOff <= insertionPoint {
			continue
		}
		pmLen := int(binary.LittleEndian.Uint32(n.raw[pageOff:]))
		pm := parseTags(n.raw[pageOff+4 : pageOff+4+pmLen])
		for _, tag := range []string{"MAINLAYER", "BGLAYER", "TOTALPATH"} {
			val := pm[tag]
			if val == "" || val == "0" {
				continue
			}
			dataOff, err := strconv.Atoi(val)
			if err != nil || dataOff <= insertionPoint {
				continue
			}
			if !reachable[dataOff] {
				return nil, fmt.Errorf(
					"page %d %s block at offset %d is not reachable by block-boundary walk from insertionPoint %d; file layout unexpected",
					i, tag, dataOff, insertionPoint,
				)
			}
		}
	}

	// Patch the target page's metadata: RECOGNTEXT → insertionPoint, RECOGNSTATUS → "1".
	// (insertionPoint is where the new recognBlock lands in the output.)
	oldMeta := n.raw[pageMetaOff+4 : pageMetaOff+4+pageMetaLen]
	newMeta := replaceTagValue(oldMeta, "RECOGNTEXT", strconv.Itoa(insertionPoint))
	newMeta = replaceTagValue(newMeta, "RECOGNSTATUS", "1")
	_ = p

	// Walk blocks from pageMetaOff to footerOff, rebuilding those in updateSet.
	var midSection []byte
	for off := pageMetaOff; off < footerOff; {
		if off+4 > len(n.raw) {
			return nil, fmt.Errorf("block walk ran out of bounds at offset %d", off)
		}
		blen := int(binary.LittleEndian.Uint32(n.raw[off:]))
		if off+4+blen > len(n.raw) {
			return nil, fmt.Errorf("block at %d length %d exceeds file", off, blen)
		}
		var body []byte
		if off == pageMetaOff {
			// Target page meta: already patched above (RECOGNTEXT + RECOGNSTATUS).
			body = rebuildBlock(newMeta, offsetMap)
		} else if updateSet[off] {
			body = rebuildBlock(n.raw[off+4:off+4+blen], offsetMap)
		} else {
			body = n.raw[off+4 : off+4+blen]
		}
		midSection = appendBlock(midSection, body)
		off += 4 + blen
	}

	// Rebuild the footer.
	oldFooter := n.raw[footerOff+4 : footerOff+4+footerLen]
	newFooter := rebuildBlock(oldFooter, offsetMap)

	newFooterOff := insertionPoint + len(recognBlock) + len(midSection)

	var out []byte
	out = append(out, n.raw[:insertionPoint]...)
	out = append(out, recognBlock...)
	out = append(out, midSection...)
	out = append(out, appendBlock(nil, newFooter)...)
	out = append(out, 't', 'a', 'i', 'l')
	out = binary.LittleEndian.AppendUint32(out, uint32(newFooterOff))
	return out, nil
}

// footerPageOffset returns the file offset of the metadata block for
// page pageIdx, as stored in the footer PAGE{pageIdx+1} tag.
func (n *Note) footerPageOffset(pageIdx int) (int, error) {
	footerOff := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	if footerOff+4 > len(n.raw) {
		return 0, fmt.Errorf("footer offset out of bounds")
	}
	footerLen := int(binary.LittleEndian.Uint32(n.raw[footerOff:]))
	if footerOff+4+footerLen > len(n.raw) {
		return 0, fmt.Errorf("footer block exceeds file size")
	}
	footer := parseTags(n.raw[footerOff+4 : footerOff+4+footerLen])
	key := fmt.Sprintf("PAGE%d", pageIdx+1)
	val, ok := footer[key]
	if !ok {
		return 0, fmt.Errorf("%s not found in footer", key)
	}
	off, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid %s offset %q: %w", key, val, err)
	}
	return off, nil
}

// replaceTagValue replaces the value of the named tag in a raw tag byte slice.
// E.g. replaceTagValue(b, "RECOGNTEXT", "59720") changes <RECOGNTEXT:0> to <RECOGNTEXT:59720>.
// If the tag is not found, the original slice is returned unchanged.
func replaceTagValue(meta []byte, key, newVal string) []byte {
	re := regexp.MustCompile(`<` + regexp.QuoteMeta(key) + `:[^>]*>`)
	replacement := []byte("<" + key + ":" + newVal + ">")
	return re.ReplaceAll(meta, replacement)
}

// appendBlock encodes data as a [4-byte LE length][data] block and appends it to dst.
func appendBlock(dst, data []byte) []byte {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(data)))
	dst = append(dst, lenBuf[:]...)
	return append(dst, data...)
}
