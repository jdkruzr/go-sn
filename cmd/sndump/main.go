// sndump dumps TOTALPATH non-stroke objects, titles, and keywords for analysis.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jdkruzr/go-sn/note"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sndump file.note")
		os.Exit(1)
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	n, err := note.Load(f)
	if err != nil {
		log.Fatalf("parse: %v", err)
	}

	fmt.Printf("device: %s (%dx%d portrait)\n", n.Header["APPLY_EQUIPMENT"], n.PageWidth(), n.PageHeight())

	for _, p := range n.Pages {
		pageW, pageH := n.PageDimensions(p)

		tp, err := n.TotalPathData(p)
		if err != nil {
			log.Printf("page %d TotalPathData: %v", p.Index+1, err)
			continue
		}
		if tp == nil {
			fmt.Printf("page %d: no TOTALPATH\n", p.Index+1)
			continue
		}

		orient := "portrait"
		if p.Meta["ORIENTATION"] == "1090" {
			orient = "landscape"
		}
		fmt.Printf("\n=== page %d (%s %dx%d) ===\n", p.Index+1, orient, pageW, pageH)
		fmt.Printf("TOTALPATH length: %d bytes\n", len(tp))
		fmt.Printf("outer_count: %d\n", binary.LittleEndian.Uint32(tp[0:4]))
		fmt.Printf("first_obj_size: %d\n", binary.LittleEndian.Uint32(tp[4:8]))

		dumpObjects(tp, pageW, pageH)
	}

	dumpFooterAnnotations(n)
}

func dumpObjects(tp []byte, pageW, pageH int) {
	firstObjSize := int(binary.LittleEndian.Uint32(tp[4:8]))
	objOff := 8
	objSize := firstObjSize
	first := true
	objIdx := 0

	for objOff < len(tp) {
		if !first {
			if objOff+4 > len(tp) {
				break
			}
			objSize = int(binary.LittleEndian.Uint32(tp[objOff:]))
			objOff += 4
		}
		first = false

		dataStart := objOff
		if dataStart+objSize > len(tp) {
			objSize = len(tp) - dataStart
		}

		isStroke := objSize >= 56 && string(tp[dataStart+48:dataStart+56]) == "others\x00\x00"

		if !isStroke && objSize >= 216 {
			fmt.Printf("\n--- obj %d (NON-STROKE) ds=%d size=%d ---\n", objIdx, dataStart, objSize)
			dumpNonStroke(tp, dataStart, objSize, pageW, pageH)
		} else {
			fmt.Printf("obj %d: stroke ds=%d size=%d\n", objIdx, dataStart, objSize)
		}

		objOff = dataStart + objSize
		objIdx++
	}
}

func dumpNonStroke(tp []byte, ds, size, pageW, pageH int) {
	if ds+size > len(tp) {
		return
	}
	obj := tp[ds : ds+size]

	// Print header bytes
	fmt.Printf("  bytes[0:16]:  %s\n", hex.EncodeToString(obj[0:16]))
	fmt.Printf("  bytes[16:32]: %s\n", hex.EncodeToString(obj[16:32]))
	fmt.Printf("  bytes[32:48]: %s\n", hex.EncodeToString(obj[32:48]))
	fmt.Printf("  bytes[48:64]: %s\n", hex.EncodeToString(obj[48:64]))
	fmt.Printf("  bytes[64:80]: %s\n", hex.EncodeToString(obj[64:80]))
	fmt.Printf("  bytes[80:96]: %s\n", hex.EncodeToString(obj[80:96]))
	fmt.Printf("  bytes[96:128]:\n")
	for off := 96; off < 128 && off+4 <= size; off += 4 {
		v := binary.LittleEndian.Uint32(obj[off:])
		fmt.Printf("    [%d]=%d (0x%08X)\n", off, v, v)
	}
	fmt.Printf("  bytes[128:144]:\n")
	for off := 128; off < 144 && off+4 <= size; off += 4 {
		v := binary.LittleEndian.Uint32(obj[off:])
		fmt.Printf("    [%d]=%d (0x%08X)\n", off, v, v)
	}

	// Byte-8 discriminator
	b8 := binary.LittleEndian.Uint32(obj[8:])
	fmt.Printf("  byte8_u32=%d\n", b8)

	// tpPageH / tpPageW
	if size >= 136 {
		tpH := int(binary.LittleEndian.Uint32(obj[128:]))
		tpW := int(binary.LittleEndian.Uint32(obj[132:]))
		fmt.Printf("  tpPageH=%d tpPageW=%d\n", tpH, tpW)
	}

	// point_count at +212
	if size >= 216 {
		n := int(binary.LittleEndian.Uint32(obj[212:]))
		fmt.Printf("  point_count=%d\n", n)

		if n > 0 && n <= 1000 && size >= 216+n*8 {
			tpH := float64(binary.LittleEndian.Uint32(obj[128:]))
			tpW := float64(binary.LittleEndian.Uint32(obj[132:]))
			fmt.Printf("  points (TOTALPATH → pixel):\n")
			for i := 0; i < n; i++ {
				base := 216 + i*8
				rawX := float64(binary.LittleEndian.Uint32(obj[base:]))
				rawY := float64(binary.LittleEndian.Uint32(obj[base+4:]))
				pxY := rawX * float64(pageH) / tpH
				pxX := (tpW - rawY) * float64(pageW) / tpW
				fmt.Printf("    [%d] rawX=%6.0f rawY=%6.0f → pixel X=%.1f Y=%.1f\n", i, rawX, rawY, pxX, pxY)
			}
		}
	}
}

// dumpFooterAnnotations prints TITLE_* and KEYWORD_* entries from the footer.
func dumpFooterAnnotations(n *note.Note) {
	footer, err := n.FooterTags()
	if err != nil {
		log.Printf("FooterTags: %v", err)
		return
	}

	// Collect and sort keys so output is deterministic.
	var titleKeys, kwKeys []string
	for k := range footer {
		switch {
		case strings.HasPrefix(k, "TITLE_"):
			titleKeys = append(titleKeys, k)
		case strings.HasPrefix(k, "KEYWORD_"):
			kwKeys = append(kwKeys, k)
		}
	}
	sort.Strings(titleKeys)
	sort.Strings(kwKeys)

	if len(titleKeys) == 0 && len(kwKeys) == 0 {
		return
	}

	fmt.Printf("\n=== titles & keywords ===\n")

	for _, k := range titleKeys {
		off, _ := strconv.Atoi(footer[k])
		level, y, x, h, w := decodeTitleKey(k[len("TITLE_"):])
		fmt.Printf("\nTITLE  level=%d  y=%d x=%d h=%d w=%d  (block offset %d)\n", level, y, x, h, w, off)
		block, err := n.BlockAt(off)
		if err != nil {
			fmt.Printf("  error reading block: %v\n", err)
			continue
		}
		printTagBlock(block)
	}

	for _, k := range kwKeys {
		off, _ := strconv.Atoi(footer[k])
		page, y := decodeKeywordKey(k[len("KEYWORD_"):])
		fmt.Printf("\nKEYWORD  page=%d  y=%d  (block offset %d)\n", page, y, off)
		block, err := n.BlockAt(off)
		if err != nil {
			fmt.Printf("  error reading block: %v\n", err)
			continue
		}
		printTagBlock(block)

		// Follow KEYWORDSITE to print the text.
		tags := parseMiniTags(block)
		if siteStr, ok := tags["KEYWORDSITE"]; ok {
			siteOff, _ := strconv.Atoi(siteStr)
			text, err := n.BlockAt(siteOff)
			if err != nil {
				fmt.Printf("  KEYWORDSITE error: %v\n", err)
			} else {
				fmt.Printf("  text: %q\n", text)
			}
		}
	}
}

// decodeTitleKey parses the suffix of a TITLE_ footer key.
// Format: [level:4][y:4][x:4][h:4][w:4] (zero-padded decimal groups).
func decodeTitleKey(s string) (level, y, x, h, w int) {
	if len(s) < 20 {
		return
	}
	level, _ = strconv.Atoi(s[0:4])
	y, _ = strconv.Atoi(s[4:8])
	x, _ = strconv.Atoi(s[8:12])
	h, _ = strconv.Atoi(s[12:16])
	w, _ = strconv.Atoi(s[16:20])
	return
}

// decodeKeywordKey parses the suffix of a KEYWORD_ footer key.
// Format: [page:4][y:4] (1-based page number, zero-padded decimal).
func decodeKeywordKey(s string) (page, y int) {
	if len(s) < 8 {
		return
	}
	page, _ = strconv.Atoi(s[0:4])
	y, _ = strconv.Atoi(s[4:8])
	return
}

// printTagBlock prints <KEY:VALUE> tags found in a raw block.
func printTagBlock(block []byte) {
	tags := parseMiniTags(block)
	// Print in a stable order.
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-20s = %s\n", k, tags[k])
	}
}

// parseMiniTags extracts <KEY:VALUE> pairs from a byte slice.
func parseMiniTags(b []byte) map[string]string {
	m := map[string]string{}
	s := string(b)
	for {
		start := strings.IndexByte(s, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start:], '>')
		if end < 0 {
			break
		}
		tag := s[start+1 : start+end]
		colon := strings.IndexByte(tag, ':')
		if colon >= 0 {
			m[tag[:colon]] = tag[colon+1:]
		}
		s = s[start+end+1:]
	}
	return m
}
