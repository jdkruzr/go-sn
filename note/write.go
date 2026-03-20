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
	Words       []RecognWord    `json:"words,omitempty"`
	Items       []RecognElement `json:"items,omitempty"`
}

// RecognBox is a bounding box in RECOGNTEXT coordinates (device pixels).
type RecognBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// RecognWord is a word-level entry in JIIX RECOGNTEXT.
// Space separators (label=" ") and newline separators (label="\n") omit BoundingBox.
type RecognWord struct {
	Label       string     `json:"label"`
	BoundingBox *RecognBox `json:"bounding-box,omitempty"`
}

// InjectRecognText replaces or inserts the RECOGNTEXT block for the given page.
// Sets RECOGNSTATUS=1 and updates the RECOGNTEXT offset in page metadata.
// Returns new file bytes suitable for writing to disk.
//
// Works for any page in a single-page or multi-page note. For multi-page notes,
// all blocks and offsets past the insertion point are relocated using a segment-based
// emit that preserves raw LAYERBITMAP pixel data between tagged blocks.
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

	// Multi-page path: use Phase 1 helpers for offset mapping, segment walk for emission.
	affectedOffsets := collectOffsets(n, insertionPoint)
	finalShift := computeShift(len(recognBlock), affectedOffsets)
	offsetMap := buildOffsetMap(affectedOffsets, finalShift)
	updateSet := buildUpdateSet(n, insertionPoint)

	// Patch the target page meta: RECOGNTEXT → insertionPoint, RECOGNSTATUS → "1".
	// Then apply offsetMap to update other offsets in it (MAINLAYER, BGLAYER, etc.).
	// KEY INSIGHT: insertionPoint itself is NOT in offsetMap (collectOffsets only includes
	// values strictly > insertionPoint), so setting RECOGNTEXT=insertionPoint is safe —
	// rebuildBlock(newMeta, offsetMap) will NOT overwrite it.
	oldMeta := n.raw[pageMetaOff+4 : pageMetaOff+4+pageMetaLen]
	newMeta := replaceTagValue(oldMeta, "RECOGNTEXT", strconv.Itoa(insertionPoint))
	newMeta = replaceTagValue(newMeta, "RECOGNSTATUS", "1")
	newMeta = rebuildBlock(newMeta, offsetMap)

	// Collect all KNOWN TAGGED block start offsets in [insertionPoint, footerOff).
	// This excludes raw LAYERBITMAP pixel data (which is not a tagged block).
	// We walk only between these known points, preserving raw data gaps verbatim.
	knownOffsets := n.collectTaggedBlockOffsets(insertionPoint, footerOff)
	slices.Sort(knownOffsets)

	// Build lookup set for O(1) validation.
	knownSet := make(map[int]bool, len(knownOffsets))
	for _, off := range knownOffsets {
		knownSet[off] = true
	}

	// AC5.4: Validate that subsequent page data blocks are at known tagged-block positions.
	// If a MAINLAYER/BGLAYER/TOTALPATH offset is not in knownOffsets, the file layout
	// is unexpected and we return a descriptive error rather than producing corrupt output.
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
			if !knownSet[dataOff] {
				return nil, fmt.Errorf(
					"page %d %s block at offset %d is not a recognized tagged block; file layout unexpected",
					i, tag, dataOff,
				)
			}
		}
	}

	// Segment-based emit: iterate over known block positions, copying gaps verbatim.
	// Track actual output offsets for each block to update the footer PAGE tags.
	blockOutputOffsets := make(map[int]int) // oldOffset -> newOffset in output
	var midSection []byte
	prevEnd := insertionPoint
	for _, off := range knownOffsets {
		// Copy raw data gap verbatim (raw bitmap data lives here).
		if off > prevEnd {
			midSection = append(midSection, n.raw[prevEnd:off]...)
		}
		if off+4 > len(n.raw) {
			return nil, fmt.Errorf("block offset %d: length prefix out of bounds", off)
		}
		blen := int(binary.LittleEndian.Uint32(n.raw[off:]))
		if off+4+blen > len(n.raw) {
			return nil, fmt.Errorf("block at %d length %d exceeds file", off, blen)
		}

		// Compute the output offset of this block (start of block including 4-byte length prefix).
		blockOutputOff := insertionPoint + len(recognBlock) + len(midSection)
		blockOutputOffsets[off] = blockOutputOff

		var emitBody []byte
		if off == pageMetaOff {
			emitBody = newMeta // already patched and rebuilt above
		} else if updateSet[off] {
			// For other blocks, use blockOutputOffsets (actual output offsets computed during segment walk).
			// But also include offsetMap for offsets not in blockOutputOffsets (e.g., LAYERBITMAP).
			mergedOffsets := make(map[int]int)
			for k, v := range blockOutputOffsets {
				mergedOffsets[k] = v
			}
			for k, v := range offsetMap {
				if _, ok := mergedOffsets[k]; !ok {
					mergedOffsets[k] = v
				}
			}
			emitBody = rebuildBlock(n.raw[off+4:off+4+blen], mergedOffsets)
		} else {
			emitBody = n.raw[off+4 : off+4+blen]
		}
		midSection = appendBlock(midSection, emitBody)
		prevEnd = off + 4 + blen
	}
	// Copy remaining raw data from last block end to footerOff verbatim.
	if footerOff > prevEnd {
		midSection = append(midSection, n.raw[prevEnd:footerOff]...)
	}

	// Rebuild the footer with relocated offsets.
	oldFooter := n.raw[footerOff+4 : footerOff+4+footerLen]
	newFooter := oldFooter

	// Update PAGE tags using actual output offsets computed during segment walk.
	for i := range n.Pages {
		oldPageOff, err := n.footerPageOffset(i)
		if err != nil {
			continue
		}
		var newPageOff int
		if i == pageIdx {
			// Target page: metadata starts at insertionPoint + len(recognBlock)
			newPageOff = insertionPoint + len(recognBlock)
		} else if newOff, ok := blockOutputOffsets[oldPageOff]; ok {
			// Other pages: use actual output offset computed during segment walk
			newPageOff = newOff
		} else {
			// Page not in segment (offset <= insertionPoint): unchanged
			newPageOff = oldPageOff
		}
		pageKey := fmt.Sprintf("PAGE%d", i+1)
		newFooter = replaceTagValue(newFooter, pageKey, strconv.Itoa(newPageOff))
	}

	// Finally, rebuild other offset references. Use blockOutputOffsets for page/block offsets,
	// and offsetMap for other offsets (like RECOGNTEXT, LAYERBITMAP) not in blockOutputOffsets.
	mergedFooterOffsets := make(map[int]int)
	for k, v := range blockOutputOffsets {
		mergedFooterOffsets[k] = v
	}
	for k, v := range offsetMap {
		if _, ok := mergedFooterOffsets[k]; !ok {
			mergedFooterOffsets[k] = v
		}
	}
	newFooter = rebuildBlock(newFooter, mergedFooterOffsets)

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
	// LAYERBITMAP offsets are NOT collected here; they are raw pixel data (not tagged blocks)
	// and are handled by the offset relocation in rebuildBlock via offsetMap.
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
