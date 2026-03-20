// snrender renders .note files to JPEG images using raw pen stroke data.
//
// Usage:
//
//	snrender [flags] file.note [file2.note ...]
//
// Flags:
//
//	-o dir     output directory (default: same as input file)
//	-quality   JPEG quality 1-100 (default: 90)
//	-page n    render only page n (1-based); default: all pages
//	-bbox      draw bounding boxes for text boxes (blue) and digests (red)
package main

import (
	"flag"
	"fmt"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jdkruzr/go-sn/note"
)

func main() {
	outDir := flag.String("o", "", "output directory (default: same as input)")
	quality := flag.Int("quality", 90, "JPEG quality 1-100")
	pageNum := flag.Int("page", 0, "render only this page (1-based); 0 = all")
	bbox := flag.Bool("bbox", false, "draw bounding boxes for text boxes and digests")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: snrender [flags] file.note ...")
		os.Exit(1)
	}

	for _, path := range flag.Args() {
		if err := renderFile(path, *outDir, *quality, *pageNum, *bbox); err != nil {
			log.Printf("error rendering %s: %v", path, err)
		}
	}
}

func renderFile(path, outDir string, quality, pageNum int, bbox bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	n, err := note.Load(f)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	if outDir == "" {
		outDir = filepath.Dir(path)
	}
	base := strings.TrimSuffix(filepath.Base(path), ".note")

	pages := n.Pages
	if pageNum > 0 {
		if pageNum > len(pages) {
			return fmt.Errorf("page %d out of range (have %d)", pageNum, len(pages))
		}
		pages = pages[pageNum-1 : pageNum]
	}

	for _, p := range pages {
		pageW, pageH := n.PageDimensions(p)

		tp, err := n.TotalPathData(p)
		if err != nil {
			return fmt.Errorf("page %d TotalPathData: %w", p.Index+1, err)
		}
		if tp == nil {
			log.Printf("page %d: no TOTALPATH data, skipping", p.Index+1)
			continue
		}

		objs, err := note.DecodeObjects(tp, pageW, pageH)
		if err != nil {
			return fmt.Errorf("page %d DecodeObjects: %w", p.Index+1, err)
		}
		log.Printf("page %d: %d strokes, %d non-stroke objects", p.Index+1, len(objs.Strokes), len(objs.NonStrokes))

		var opts *note.RenderOpts
		if !bbox {
			// suppress bounding-box outlines; Background/Ink use defaults
			opts = &note.RenderOpts{TextBoxColor: nil, DigestColor: nil}
		}

		img := note.RenderObjects(objs, pageW, pageH, opts)

		var outPath string
		if len(n.Pages) == 1 {
			outPath = filepath.Join(outDir, base+".jpg")
		} else {
			outPath = filepath.Join(outDir, fmt.Sprintf("%s_page%d.jpg", base, p.Index+1))
		}

		out, err := os.Create(outPath)
		if err != nil {
			return err
		}
		if err := jpeg.Encode(out, img, &jpeg.Options{Quality: quality}); err != nil {
			out.Close()
			return err
		}
		out.Close()
		log.Printf("wrote %s", outPath)
	}

	return nil
}
