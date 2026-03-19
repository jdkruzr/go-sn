// pattern: Functional Core
package note

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

// rebuildBlock rewrites each offset-valued tag in block whose decimal value
// appears as a key in offsetMap, replacing it with the mapped value.
// Operates on the raw block body (without length prefix). Returns a new slice.
//
// Example: offsetMap = {59720: 59820} rewrites <RECOGNTEXT:59720> → <RECOGNTEXT:59820>
// without touching <RECOGNTEXT:0> or unrelated tags.
// Uses regex with < anchoring to prevent matching offset values that are numeric
// suffixes of others (e.g., :100> in :59100>).
func rebuildBlock(block []byte, offsetMap map[int]int) []byte {
	if len(offsetMap) == 0 {
		return block
	}
	// Build alternation of all old offset values, sorted for deterministic regex.
	offStrs := make([]string, 0, len(offsetMap))
	for oldOff := range offsetMap {
		offStrs = append(offStrs, strconv.Itoa(oldOff))
	}
	sort.Strings(offStrs)
	// Anchoring on < prevents matching offset values that are numeric suffixes of others.
	// Compile ONE regex per call (not per offset entry).
	re := regexp.MustCompile(`<([^:<>]+):(` + strings.Join(offStrs, `|`) + `)>`)
	return re.ReplaceAllFunc(block, func(match []byte) []byte {
		// Find the colon separating tag name from value (scan from the end of the tag name).
		colonIdx := bytes.Index(match[1:], []byte(":")) + 1
		oldOff, err := strconv.Atoi(string(match[colonIdx+1 : len(match)-1]))
		if err != nil {
			return match
		}
		newOff, ok := offsetMap[oldOff]
		if !ok {
			return match
		}
		result := make([]byte, 0, len(match)+4)
		result = append(result, match[:colonIdx+1]...) // "<TAGNAME:"
		result = strconv.AppendInt(result, int64(newOff), 10)
		result = append(result, '>')
		return result
	})
}
