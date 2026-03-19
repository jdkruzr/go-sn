# note package

Last verified: 2026-03-19

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
