package note

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
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

	// Multi-page path: relocate all offsets past insertionPoint using segment-based emit.
	updateSet := buildUpdateSet(n, insertionPoint)

	// Collect all known tagged block offsets in [insertionPoint, footerOff).
	// We walk only between these known points, leaving raw data (LAYERBITMAP pixels) untouched.
	knownOffsets := n.collectTaggedBlockOffsets(insertionPoint, footerOff)
	slices.Sort(knownOffsets)

	// The target page's metadata will be patched during the segment walk after blockPositions are known.
	oldMeta := n.raw[pageMetaOff+4 : pageMetaOff+4+pageMetaLen]

	// Two-pass segment-based walk:
	// Pass 1: collect blockPositions (output offsets for each block)
	// Pass 2: emit blocks with offset references updated based on blockPositions

	// PASS 1: Collect block positions
	blockPositions := make(map[int]int)  // oldOffset -> newOffset in output
	var pass1Size int
	prevEnd := insertionPoint
	for _, off := range knownOffsets {
		// Count the gap
		if off > prevEnd {
			pass1Size += off - prevEnd
		}

		// Count this block
		if off+4 <= len(n.raw) {
			blen := int(binary.LittleEndian.Uint32(n.raw[off:]))
			blockPos := insertionPoint + len(recognBlock) + pass1Size
			blockPositions[off] = blockPos
			pass1Size += 4 + blen
			prevEnd = off + 4 + blen
		}
	}
	if footerOff > prevEnd {
		pass1Size += footerOff - prevEnd
	}

	// PASS 2: Emit blocks with correct offset references
	var midSection []byte
	prevEnd = insertionPoint
	for _, off := range knownOffsets {
		// Copy raw data gap verbatim (may contain LAYERBITMAP pixels or other raw data).
		if off > prevEnd {
			midSection = append(midSection, n.raw[prevEnd:off]...)
		}

		// Read the block at off.
		if off+4 > len(n.raw) {
			return nil, fmt.Errorf("block offset %d: length prefix out of bounds", off)
		}
		blen := int(binary.LittleEndian.Uint32(n.raw[off:]))
		if off+4+blen > len(n.raw) {
			return nil, fmt.Errorf("block at %d length %d exceeds file", off, blen)
		}

		// Determine body: use patched metadata for target page, rebuild if in updateSet, else unchanged.
		var body []byte
		if off == pageMetaOff {
			// Patch the target page metadata
			body = replaceTagValue(oldMeta, "RECOGNTEXT", strconv.Itoa(insertionPoint))
			body = replaceTagValue(body, "RECOGNSTATUS", "1")
			// Update other offset references in the target page metadata
			body = rebuildBlock(body, blockPositions)
		} else if updateSet[off] {
			origBody := n.raw[off+4 : off+4+blen]
			// Rebuild offset references in other blocks
			body = rebuildBlock(origBody, blockPositions)
		} else {
			body = n.raw[off+4 : off+4+blen]
		}
		midSection = appendBlock(midSection, body)
		prevEnd = off + 4 + blen
	}

	// Copy remaining raw data from last block end to footerOff verbatim.
	if footerOff > prevEnd {
		midSection = append(midSection, n.raw[prevEnd:footerOff]...)
	}

	// Rebuild the footer, updating offset references using blockPositions.
	oldFooter := n.raw[footerOff+4 : footerOff+4+footerLen]
	newFooter := oldFooter

	// First, update PAGE tags based on blockPositions
	for i := range n.Pages {
		pageKey := fmt.Sprintf("PAGE%d", i+1)
		oldPageOff, err := n.footerPageOffset(i)
		if err == nil {
			if newPageOff, ok := blockPositions[oldPageOff]; ok {
				newFooter = replaceTagValue(newFooter, pageKey, strconv.Itoa(newPageOff))
			}
		}
	}

	// Then rebuild other offset references
	newFooter = rebuildBlock(newFooter, blockPositions)

	// newFooterOff is computed from the actual lengths of recognBlock and midSection.
	// midSection length may differ from the original span due to digit-width changes
	// in tag values (handled by computeShift and offsetMap). The offset is not
	// predicted but calculated from actual lengths rather than relying on finalShift.
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

// collectTaggedBlockOffsets returns all known tagged block start offsets in the range
// [insertionPoint, footerOff). These are the offsets we know contain structured data,
// so we walk only between these known points, leaving raw data (like LAYERBITMAP pixels)
// untouched in the gaps.
//
// Includes: insertionPoint, all page meta blocks, and all layer/path/keyword blocks
// that are referenced by tag values. EXCLUDES LAYERBITMAP offsets (raw pixel data).
func (n *Note) collectTaggedBlockOffsets(insertionPoint, footerOff int) []int {
	seen := make(map[int]bool)
	seen[insertionPoint] = true // Target page meta block is always first known block

	// Collect page meta offsets for all pages where the offset > insertionPoint
	for i := range n.Pages {
		off, err := n.footerPageOffset(i)
		if err == nil && off > insertionPoint {
			seen[off] = true
		}
	}

	parseInt := func(s string) (int, bool) {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 {
			return 0, false
		}
		return v, true
	}

	// Collect offsets from footer tags: PAGE{N}, TITLE_*, KEYWORD_*
	footerOff2 := int(binary.LittleEndian.Uint32(n.raw[len(n.raw)-4:]))
	footerLen := int(binary.LittleEndian.Uint32(n.raw[footerOff2:]))
	footer := parseTags(n.raw[footerOff2+4 : footerOff2+4+footerLen])

	for key, val := range footer {
		if len(key) < 4 {
			continue
		}
		isKeyword := len(key) >= 8 && key[:8] == "KEYWORD_"
		isTitle := len(key) >= 6 && key[:6] == "TITLE_"
		if !isKeyword && !isTitle {
			continue
		}
		off, ok := parseInt(val)
		if ok && off > insertionPoint && off < footerOff {
			seen[off] = true
		}
	}

	// Collect offsets from page-meta blocks: MAINLAYER, BGLAYER, TOTALPATH, RECOGNTEXT, RECOGNFILE
	// Also collect LAYERBITMAP from MAINLAYER/BGLAYER (but we'll NOT include them as known blocks,
	// only mark them for offset updates in the block rebuild)
	for i := range n.Pages {
		pageOff, err := n.footerPageOffset(i)
		if err != nil || pageOff <= insertionPoint {
			continue
		}
		pageMetaLen := int(binary.LittleEndian.Uint32(n.raw[pageOff:]))
		pageMeta := parseTags(n.raw[pageOff+4 : pageOff+4+pageMetaLen])
		for _, tag := range []string{"MAINLAYER", "BGLAYER", "TOTALPATH", "RECOGNTEXT", "RECOGNFILE"} {
			val := pageMeta[tag]
			off, ok := parseInt(val)
			if ok && off > insertionPoint && off < footerOff {
				seen[off] = true
			}
		}
	}

	result := make([]int, 0, len(seen))
	for off := range seen {
		result = append(result, off)
	}
	return result
}
