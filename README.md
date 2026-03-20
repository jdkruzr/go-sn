# go-sn

Go library for parsing, rendering, and modifying Supernote `.note` files. No external dependencies beyond the Go standard library.

## What it does

- **Parse** `.note` files into structured data: pages, metadata tags, layer info, footer annotations
- **Decode strokes** from TOTALPATH blocks into pixel-space coordinates with pressure data
- **Render pages** to images with pressure-sensitive anti-aliased strokes
- **Read and write recognition text** (RECOGNTEXT blocks) — extract existing OCR results or inject new ones
- **Handle orientation** — per-page landscape/portrait detection with correct coordinate mapping

Supports A5X, N6, and Manta devices.

## Install

```
go get github.com/jdkruzr/go-sn
```

## Usage

### Parse a note file

```go
f, _ := os.Open("example.note")
defer f.Close()

n, _ := note.Load(f)
fmt.Printf("%d pages, device: %s\n", len(n.Pages), n.Header["APPLY_EQUIPMENT"])

for _, p := range n.Pages {
    w, h := n.PageDimensions(p)
    fmt.Printf("  page %d: %dx%d, orientation=%s\n", p.Index, w, h, p.Meta["ORIENTATION"])
}
```

### Render a page to an image

```go
p := n.Pages[0]
pageW, pageH := n.PageDimensions(p)

tp, _ := n.TotalPathData(p)
objs, _ := note.DecodeObjects(tp, pageW, pageH)
img := note.RenderObjects(objs, pageW, pageH, nil)

out, _ := os.Create("page1.jpg")
jpeg.Encode(out, img, &jpeg.Options{Quality: 90})
out.Close()
```

### Read recognition text

```go
raw, err := n.ReadRecognText(p)
if err == nil && raw != nil {
    var rc note.RecognContent
    json.Unmarshal(raw, &rc)
    for _, el := range rc.Elements {
        fmt.Println(el.Label)
    }
}
```

### Inject recognition text

```go
content := note.RecognContent{
    Type: "Text",
    Elements: []note.RecognElement{
        {Type: "Text", Label: "Hello from OCR"},
    },
}

// Inject into each page. Reload after each write because file offsets shift.
current := n
for pageIdx := range n.Pages {
    out, err := current.InjectRecognText(pageIdx, content)
    if err != nil {
        log.Fatalf("page %d: %v", pageIdx, err)
    }
    if err := os.WriteFile("modified.note", out, 0644); err != nil {
        log.Fatal(err)
    }
    // Reload the modified file — block offsets changed.
    f, _ := os.Open("modified.note")
    current, err = note.Load(f)
    f.Close()
    if err != nil {
        log.Fatal(err)
    }
}
```

## Command-line tools

### snrender

Renders `.note` pages to JPEG images.

```
go install github.com/jdkruzr/go-sn/cmd/snrender@latest

snrender example.note                    # all pages
snrender -page 2 example.note            # page 2 only
snrender -bbox example.note              # draw text box / digest bounding boxes
snrender -o /tmp -quality 95 example.note
```

### sndump

Dumps TOTALPATH objects, titles, and keyword annotations for debugging.

```
go install github.com/jdkruzr/go-sn/cmd/sndump@latest

sndump example.note
```

## .note file format

A `.note` file is a sequence of length-prefixed blocks with `<KEY:VALUE>` metadata tags:

```
[magic: "noteSN_FILE_VER_20230015"]
[header block]           — FILE_TYPE, APPLY_EQUIPMENT, FILE_RECOGN_TYPE, ...
[data blocks]            — layer bitmaps, TOTALPATH strokes, RECOGNTEXT, ...
[footer block]           — PAGE1, PAGE2, ..., TITLE_*, KEYWORD_*, STYLE_*
["tail" + footer offset]
```

Each block is `[4-byte LE length][body]`. The footer's `PAGE{N}` tags point to per-page metadata blocks, which in turn reference layer, stroke, and recognition data via file offsets.

### Page metadata tags

| Tag | Description |
|-----|-------------|
| `MAINLAYER` | File offset of main layer block |
| `BGLAYER` | File offset of background layer block |
| `LAYER1`–`LAYER3` | Extra layer offsets (0 = absent) |
| `TOTALPATH` | File offset of stroke vector data |
| `RECOGNTEXT` | File offset of base64-encoded recognition JSON |
| `RECOGNSTATUS` | 0 = none, 1 = recognized, 2 = modified since recognition |
| `ORIENTATION` | 1000 = portrait, 1090 = landscape |
| `PAGESTYLE` | Template name (e.g., "style_8mm_ruled_line") |

### Device dimensions

| Device | Portrait (px) | EMR max |
|--------|--------------|---------|
| A5X | 1404 x 1872 | 15819 x 11864 |
| Manta | 1920 x 2560 | 21632 x 16224 |
| N6 | 1404 x 1872 | 15819 x 11864 |

Landscape pages swap both pixel and EMR dimensions. EMR values are embedded per-object in the TOTALPATH data, so coordinate transforms are self-describing.

### TOTALPATH stroke format

Each stroke object contains a 212-byte header followed by coordinate arrays:

```
[0:212]           fixed header (includes tpPageH/tpPageW at +128/+132)
[212:216]         point_count (uint32 LE)
[216:216+N*8]     N coordinate pairs (rawX, rawY as uint32 LE)
[216+N*8:+4]      pressure_count (uint32 LE)
[+N*2]            N pressure values (uint16 LE, range ~200–3000)
```

Coordinate transform to portrait pixel space:
```
pixel_Y = rawX * pageH / tpPageH
pixel_X = (tpPageW - rawY) * pageW / tpPageW
```

## License

See [LICENSE](LICENSE).
