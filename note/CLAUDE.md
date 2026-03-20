# note package

Last verified: 2026-03-20

## Purpose
Core library for parsing, rendering, and modifying Supernote .note binary files.
Enables OCR text injection and content extraction without the Supernote device.

## Contracts
- **Exposes**: `Load(io.Reader) (*Note, error)` -- parse .note file
- **Exposes**: `InjectRecognText(pageIdx, content) ([]byte, error)` -- inject OCR text into any page
- **Exposes**: `ReadRecognText(page) ([]byte, error)` -- read existing OCR text
- **Exposes**: `DecodeObjects(tp, w, h) (*PageObjects, error)` -- decode strokes and bounding boxes
- **Exposes**: `Render / RenderObjects` -- render strokes to RGBA image
- **Guarantees**: InjectRecognText works for ANY page in single or multi-page notes
- **Guarantees**: Original .note data before insertion point is preserved byte-for-byte
- **Guarantees**: Old RECOGNTEXT blocks are left as dead space; only the pointer changes
- **Expects**: Valid .note file (correct magic bytes, well-formed footer)

## Dependencies
- **Uses**: stdlib only (encoding/binary, encoding/base64, encoding/json, image, regexp)
- **Used by**: ultrabridge processor (OCR pipeline), cmd/snrender, cmd/sndump
- **Boundary**: No database, no network -- pure file format library

## Key Decisions
- Dead-space strategy for replaced blocks: simpler than compaction, device tolerates it
- Segment-based emit for multi-page: walk known tagged block offsets, copy raw gaps (LAYERBITMAP pixel data) verbatim to avoid corrupting binary data
- Offset relocation with digit-width convergence: inserting a block shifts all subsequent offsets; decimal string growth of offset values causes cascading shifts handled by fixpoint loop

## Invariants
- Footer is always last structured block before "tail" marker + 4-byte offset
- Block format: [4-byte LE length][body] -- body contains `<TAG:VALUE>` pairs
- RECOGNSTATUS=1 means RECOGNTEXT block contains valid OCR data
- Page metadata tags: MAINLAYER, BGLAYER, TOTALPATH, RECOGNTEXT, RECOGNFILE (all offset-valued)
- LAYERBITMAP is raw pixel data (NOT a tagged block) -- must be copied verbatim, never parsed as tags

## Key Files
- `parse.go` -- Load, Note/Page types, BlockAt, FooterTags, parseTags
- `write.go` -- InjectRecognText, ReadRecognText, RecognContent types, collectTaggedBlockOffsets
- `relocate.go` -- Multi-page offset relocation: collectOffsets, computeShift, buildOffsetMap, buildUpdateSet, rebuildBlock
- `totalpath.go` -- Stroke/Point/Rect types, DecodeObjects, DecodeTotalPath
- `render.go` -- Render, RenderObjects, RenderOpts

## Gotchas
- Supernote quirk: page numbering in footer is 1-based (PAGE1, PAGE2) but API is 0-based
- LAYERBITMAP offsets live inside MAINLAYER/BGLAYER blocks but the bitmap data itself is raw bytes in the gap between tagged blocks
- `replaceTagValue` uses regex per call -- acceptable for small tag blocks, not for bulk operations
- Supernote firmware bug: some strokes have inflated `point_count` in the TOTALPATH header -- the decoder reads pressure/timing data as coordinates, producing values in the millions. Three defense layers handle this (see below)

## Firmware Bug: Inflated Stroke point_count

Observed on Nomad N6, discovered 2026-03-20 in file `20260319_202438 Cocktails.note`.

**Bug:** A stroke object's header declares `point_count=118` at offset +212, but only 107 coordinate pairs contain valid data. The remaining 11 are uint16 pressure values misinterpreted as uint32 coordinate pairs, producing coordinates like rawY=95,094,187 (vs expected range 0-15,819). The `pressure_count` at the expected position (after 118 coordinate pairs) is 103,089,641 — confirming the header is corrupt.

**Impact without fix:** `drawThickLine` iterates a bounding box spanning millions of pixels. A single stroke hangs the renderer for 5+ minutes, causing HTTP timeouts and blank page renders in the web UI.

**Defense in depth (3 layers):**
1. `decodeStroke` cross-validates `pressure_count == point_count`; rejects entire stroke if mismatched (catches corrupt header)
2. `decodeStroke` truncates at first coordinate where rawX > 2*tpPageH or rawY > 2*tpPageW (catches individual garbage coords)
3. `drawThickLine` clamps bounding box to image bounds (prevents hang regardless of input)
