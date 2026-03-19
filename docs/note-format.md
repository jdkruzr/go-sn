# .note File Format Reference

Last verified: 2026-03-18
Device: Supernote N6 (1404×1872 px, 226 DPI)

Reverse engineered by direct binary analysis of six test files spanning
standard and RTR (real-time recognition) notes, with and without digests
(screen clips), text boxes, and stickers.

---

## File Layout

```
[0:24]       Magic string: "noteSN_FILE_VER_20230015"
[24:28]      File header block length L (LE uint32)
[28:28+L]    File header metadata: <KEY:VALUE> tags

... data blocks scattered throughout the file ...
... each block: [4-byte LE length][body of that length] ...

[footer_off+0:+4]   Footer block length F (LE uint32)
[footer_off+4:+4+F] Footer metadata: <KEY:VALUE> tags
[end-8:end-4]       ASCII literal "tail"
[end-4:end]         Footer offset (LE uint32) = footer_off
```

Every block (page, layer, style, TOTALPATH, etc.) starts with a 4-byte
LE uint32 length prefix. For layer blocks, this length covers the
metadata tags; the actual bitmap data follows the tags and extends to
the next block (determined by the LAYERBITMAP offset pointer).

---

## Metadata Tag Format

Tags use the angle-bracket syntax: `<KEY:VALUE>` with no escaping.
Multiple tags are concatenated without delimiters.

Parse with: `<([^:<>]+):(.*?)>`

---

## File Header Tags

| Tag | Example | Notes |
|-----|---------|-------|
| `MODULE_LABEL` | `none` | Present only when full app feature set ran |
| `FILE_TYPE` | `NOTE` | Always NOTE for .note files |
| `APPLY_EQUIPMENT` | `N6` | Device model |
| `FINALOPERATION_PAGE` | `1` | Last active page number |
| `FINALOPERATION_LAYER` | `1` | Last active layer |
| `DEVICE_DPI` | `0` | 0 means use device default |
| `SOFT_DPI` | `0` | |
| `FILE_PARSE_TYPE` | `0` | |
| `RATTA_ETMD` | `0` | |
| `APP_VERSION` | `0` | Present when MODULE_LABEL present |
| `FILE_ID` | `F20260318...` | Unique file identifier |
| `FILE_RECOGN_TYPE` | `0` or `1` | **0 = standard, 1 = RTR (MyScript iink active)** |
| `FILE_RECOGN_LANGUAGE` | `en_US` or `none` | |
| `PDFSTYLE` | `none` | |
| `PDFSTYLEMD5` | `0` | |
| `STYLEUSAGETYPE` | `0` | |
| `HIGHLIGHTINFO` | `0` | |
| `HORIZONTAL_CHECK` | `0` | |
| `IS_OLD_APPLY_EQUIPMENT` | `1` | |
| `ANTIALIASING_CONVERT` | `2` | |

---

## Footer Tags

| Tag | Example | Notes |
|-----|---------|-------|
| `PAGE1` | `43467` | File offset of Page 1 metadata block |
| `PAGE2` | `...` | File offset of Page 2 metadata block (if present) |
| `DIRTY` | `1` | Unsaved-changes flag |
| `FILE_FEATURE` | `24` or `3194` | Feature bitmask (24=standard, 3194=RTR) |
| `STYLE_style_8mm_ruled_line` | `330` | File offset of background style bitmap |

---

## Page Metadata Block

Located at the offset given by `PAGE1` etc. in the footer.
The 4-byte length prefix covers the metadata; all the offset values
within it point to OTHER blocks elsewhere in the file.

| Tag | Example | Notes |
|-----|---------|-------|
| `PAGESTYLE` | `style_8mm_ruled_line` | Template name |
| `PAGESTYLEMD5` | `0` | |
| `LAYERINFO` | `[{...}]` | JSON (uses `#` instead of `:`) with layer visibility/ordering |
| `LAYERSEQ` | `MAINLAYER,BGLAYER` | Active layer names |
| `MAINLAYER` | `16054` | File offset of main layer block |
| `BGLAYER` | `1255` | File offset of background layer block |
| `LAYER1`, `LAYER2`, `LAYER3` | `0` | Extra layer offsets (0 = absent) |
| `TOTALPATH` | `17539` | File offset of TOTALPATH (stroke vector) block |
| `THUMBNAILTYPE` | `0` | |
| `RECOGNSTATUS` | `0`,`1`,`2` | 0=no recognition, 1=recognized, 2=modified |
| `RECOGNTEXT` | `59720` or `0` | File offset of recognition text block (base64 JSON) |
| `RECOGNFILE` | `48289` or `0` | File offset of MyScript iink ZIP archive |
| `PAGEID` | `P20260318...` | Unique page identifier |
| `RECOGNTYPE` | `0` | |
| `RECOGNFILESTATUS` | `1` or `0` | |
| `RECOGNLANGUAGE` | `none` | |
| `EXTERNALLINKINFO` | `0` | |
| `IDTABLE` | `61171` or `0` | File offset of ID table block |
| `ORIENTATION` | `1000` or `1090` | **1000 = portrait, 1090 = landscape** |
| `PAGETEXTBOX` | `0` or `1` | 1 = page has text box overlay |
| `DISABLE` | `122,447,193,88\|` | Pixel rect of disabled/masked region (digest box) |

---

## Layer Block Structure

```
[0:4]          Metadata length M (LE uint32)
[4:4+M]        <KEY:VALUE> tags:
                 LAYERTYPE: NOTE
                 LAYERPROTOCOL: RATTA_RLE
                 LAYERNAME: MAINLAYER or BGLAYER etc.
                 LAYERPATH: 0
                 LAYERBITMAP: <file offset of bitmap block>
                 LAYERVECTORGRAPH: 0
                 LAYERRECOGN: 0
[4+M : next]   (not used directly — bitmap is at LAYERBITMAP offset)
```

The `LAYERBITMAP` pointer points to a separate block. For BGLAYER, this
is often the same as the template STYLE block (the background layer
literally IS the page template). For MAINLAYER it points to the actual
drawing bitmap block.

### RATTA_RLE Bitmap Block

```
[0:4]   Data length D (LE uint32)
[4:4+D] RATTA_RLE compressed bitmap
```

**RATTA_RLE format:**
- Pairs of bytes: (color_code, length)
- Color codes: `0x61`=black, `0x62`=background/white, `0x63`=dark-gray,
  `0x64`=gray, `0x65`=white; +`0x05` variants are "marker" versions
- Length: if high bit set (`& 0x80`), multi-byte (combine with next);
  `0xFF` = special sentinel = 0x4000 pixel run
- Output: grayscale or RGB based on bit depth in LAYERPROTOCOL metadata

Reference implementation: see `supernotelib` Python package (jya-dev).

---

## TOTALPATH Block

Contains all vector objects for the page: pen strokes, digest overlays,
text boxes, and stickers. Pen strokes include raw (x,y) coordinates,
pressure, and timing data.

```
[0:4]   outer_count (LE uint32)  — total number of objects
[4:8]   first_obj_size (LE uint32) — byte length of object 0's data

[8 : 8+first_obj_size]      Object 0 data (NO size prefix)
[8+first_obj_size : +4]     Object 1 size (LE uint32)
[... : ...+obj1_size]       Object 1 data
[... : +4]                  Object 2 size
...
```

### Pen Stroke Object

Identified by the byte sequence `others\x00\x00` at offset 48 within
the object data. All offsets below are relative to the object data start.

```
[0:48]    Fixed header part 1 (mostly zeros and constants)
[8]       Always 1
[16]      Always 400 (0x190)
[20]      Always 10
[24]      Always 0
[28]      Always 32 (0x20)
[32]      Always 0xFFFFFFFF (sentinel)
[36]      Always 1
[40]      Always 0
[44]      Always 0
[48:56]   "others\x00\x00" — stroke type marker
[56:104]  Zeros
[104]     Always 0

[108:112] Bounding box pixel_x_min (portrait coords)
[112:116] Bounding box pixel_y_min (portrait coords)
[116:120] Bounding box pixel_x_center
[120:124] Bounding box pixel_y_max - pixel_y_min (height)
[124:128] Bounding box pixel_x_max

[128:132] tpPageH — page height in TOTALPATH coordinate units (15819 for N6)
[132:136] tpPageW — page width in TOTALPATH coordinate units (11864 for N6)
[136:144] "others\x00\x00" again? (overlaps with next section)  ← TODO: clarify
[144:160] "superNoteNote\x00\x00\x00" — app identifier

[160:212] Zeros / reserved
[212:216] point_count (LE uint32)

[216 : 216+N*8]       N coordinate pairs: (rawX, rawY) each LE uint32
[216+N*8 : +4]        pressure_count (must equal N)
[220+N*8 : +N*2]      N pressure values (LE uint16, range ~200–3000)
[220+N*8+N*2 : +4]    third_array_count (= N)
[... : +N*4]          third array (uint32 per point — likely timestamps)
[... : +4]            fourth_array_count (= N)
[... : +N*4]          fourth array (uint32 per point — meaning TBD)
[... : ...]           additional trailing data (meaning TBD)
```

### Coordinate Transform (Portrait Pixel Space)

The TOTALPATH coordinate system has its origin at the **top-right** of the
portrait page (or equivalently at the top-left when the page is viewed
in landscape orientation with the long edge horizontal). X increases
down the portrait page; Y increases from right to left.

```
pixel_Y = rawX × pageHeightPx / tpPageH
pixel_X = (tpPageW − rawY) × pageWidthPx / tpPageW
```

For N6: `pageHeightPx=1872, pageWidthPx=1404, tpPageH=15819, tpPageW=11864`

Scale is isotropic: tpPageH/pageHeightPx = tpPageW/pageWidthPx ≈ 8.45 units/pixel.

The physical units are approximately 1/100 mm:
- tpPageH=15819 → 158.19 mm ≈ N6 screen height (157.8 mm) ✓
- tpPageW=11864 → 118.64 mm (content area, not full page height for landscape)

### Non-Stroke Objects (Digest, Text Box, Sticker)

Objects without `others\x00\x00` at +48 are non-stroke objects. They
share the same 216-byte fixed header structure and use the same
coordinate transform as pen strokes.

**Discriminator** — `uint32 LE` at offset **+8** within the object data:

| Value | Type       | Relation to page metadata |
|-------|------------|---------------------------|
| 100   | Digest     | One per entry in DISABLE tag (screen clip region) |
| 200   | Text Box   | Present when PAGETEXTBOX=1 |

**Bounding box** — the bounding polygon is stored at the same location
as stroke point data:

```
[212:216]       point_count (uint32 LE) — always 5 for a rectangle
[216:216+5*8]   5 closed-polygon corners (rawX, rawY uint32 LE pairs)
```

The 5 points form a closed rectangle (first == last). Apply the same
coordinate transform as strokes to get portrait pixel coordinates:

```
pixel_Y = rawX × pageH / tpPageH
pixel_X = (tpPageW − rawY) × pageW / tpPageW
```

**Additional fields in header** (partially decoded):

```
[100:104]   pixel_X_min (matches DISABLE x value for digests)
[104:108]   pixel_Y_min (matches DISABLE y value for digests)
[108:116]   unknown (zeros observed)
[116:120]   unknown non-zero value
[120:124]   unknown non-zero value
[124:128]   0xFFFFFFFF sentinel
```

Sticker objects were not observed in the test corpus. Sticker artwork
appears to be encoded as dense pen stroke sequences (large stroke objects
with thousands of closely-spaced points forming a filled shape).

---

## RECOGNTEXT Block

```
[0:4]   Data length D (LE uint32)
[4:4+D] Base64-encoded JSON string
```

Decoded JSON example:
```json
{
  "elements": [
    { "type": "Raw Content" }
  ],
  "type": "Raw Content"
}
```

For files with actual recognized text, elements have type `"Text"` with
word bounding boxes and label text. Format follows MyScript iink output.

---

## RECOGNFILE Block

```
[0:4]   Data length D (LE uint32)
[4:4+D] ZIP archive (PK\x03\x04 magic)
```

The ZIP contains MyScript iink recognition data:

```
meta.json                   — document metadata
                              Application: "iink", Version: "3.0.3"
rel.json                    — object relationship graph
index.bdom                  — binary DOM (BDOM format, starts with "BDOM")
pages/{pageId}/ink.bink     — raw ink data (BINK format, starts with "BINK")
pages/{pageId}/page.bdom    — page binary DOM
pages/{pageId}/style.css    — MyScript pen/font CSS
pages/{pageId}/meta.json    — page-level metadata
```

The BINK format is MyScript's proprietary binary ink data format.
It contains the same stroke coordinates as TOTALPATH but in iink's
coordinate system and only exists for RTR notes (FILE_RECOGN_TYPE=1).

---

## Device Pixel Dimensions

| Device | Width | Height | DPI |
|--------|-------|--------|-----|
| N6     | 1404  | 1872   | 226 |
| Manta  | 1920  | 2560   | 300 |
| A5X    | 1404  | 1872   | 226 |

---

## Key Findings / Gotchas

1. **ORIENTATION=1000 = PORTRAIT** (not landscape). 1090 = landscape.
   The supernotelib constant name `ORIENTATION_VERTICAL="1000"` is correct.

2. **Y-axis inversion in TOTALPATH**: rawY=0 is the RIGHT edge of portrait.
   You must subtract from tpPageW: `pixel_X = (tpPageW − rawY) × scale`.

3. **LAYERBITMAP sharing**: BGLAYER.LAYERBITMAP often points to the same
   block as the page template STYLE block. They are literally the same data.

4. **outer_count includes ALL objects**, not just pen strokes. Digest boxes,
   text boxes, and stickers all appear as separate objects in TOTALPATH.

5. **supernotelib does NOT decode TOTALPATH**. Their "vector" export uses
   potrace to trace the RATTA_RLE raster bitmap. Our approach is novel.

6. **Pressure range**: empirically ~200 (pen lift) to ~3000 (hard press).
   Approximately 12-bit resolution mapped over the usable range.

7. **File_ID format**: `F{yyyyMMddHHmmss}{microseconds}{random}`

8. **LAYERINFO JSON quirk**: uses `#` instead of `:` for key-value separation
   within the JSON-like structure (probably to avoid conflict with tag format).
