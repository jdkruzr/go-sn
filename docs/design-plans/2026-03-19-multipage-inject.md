# Multi-Page InjectRecognText Design

## Summary

`InjectRecognText` currently works only when the target page's metadata block sits immediately before the footer — a condition satisfied by single-page notes and the final page of any multi-page note. For any earlier page in a multi-page note, the function returns an error rather than corrupt output. This design removes that restriction.

The core challenge is that inserting a RECOGNTEXT block mid-file shifts every byte offset stored after the insertion point. The `.note` format stores dozens of such offsets as decimal integers inside `<KEY:VALUE>` tag strings, spread across the footer, all subsequent page metadata blocks, layer header blocks, and nested keyword structures. A complication specific to decimal encoding is that adding bytes can cause an offset value itself to grow in digit count (e.g. 99997 → 100003), which would itself add a byte and potentially cascade. The fix handles this with a fixed-point iteration that converges on the exact final shift before any bytes are written. Once the shift is known, a block-level walk from the insertion point to the footer rebuilds only the blocks that contain affected offsets, emitting everything else verbatim.

## Definition of Done

1. `InjectRecognText` works correctly for any page index in a multi-page note, not just the last page.
2. All file offsets past the insertion point are correctly relocated — including footer `PAGE{N}` tags, `TITLE_*`/`KEYWORD_*` tags, subsequent page metadata tags (`MAINLAYER`, `BGLAYER`, `TOTALPATH`, `RECOGNTEXT`, `RECOGNFILE`), layer-level `LAYERBITMAP` offsets, and nested `KEYWORDSITE` offsets inside KEYWORD blocks.
3. Variable-width offset values (e.g., `99999` → `100000`) are handled via iterative convergence — re-running the relocation pass until no offset value changes digit width.
4. Existing single-page behavior is unchanged.
5. The fix is verified against a provided test corpus of real multi-page `.note` files.

## Acceptance Criteria

### multipage-inject.AC1: InjectRecognText works for any page position
- **multipage-inject.AC1.1 Success:** Inject into display page 0 (first page) of a 3-page note → output is a valid, parseable Note
- **multipage-inject.AC1.2 Success:** Inject into display page 1 (middle page) → output is valid
- **multipage-inject.AC1.3 Success:** Inject into the last page of a multi-page note → output matches pre-fix behavior (regression)
- **multipage-inject.AC1.4 Success:** Inject into a note where pages appear in non-sequential file order (RTR note: display page 2 metadata appears before display page 0 metadata in the file) → output is valid

### multipage-inject.AC2: All offsets past the insertion point are correctly relocated
- **multipage-inject.AC2.1 Success:** Footer PAGE{N} tags for all pages after the insertion point resolve to readable metadata blocks in the output
- **multipage-inject.AC2.2 Success:** MAINLAYER, BGLAYER, TOTALPATH values in subsequent page metas resolve to readable blocks (BlockAt succeeds)
- **multipage-inject.AC2.3 Success:** LAYERBITMAP inside a MAINLAYER block at offset > insertionPoint is correctly incremented in the rebuilt block
- **multipage-inject.AC2.4 Success:** RECOGNTEXT in subsequent page metas (RTR note) points to the correct location in the output after replacement
- **multipage-inject.AC2.5 Edge:** BGLAYER blocks whose LAYERBITMAP points before the insertion point (shared template bitmap) are not modified — value unchanged in output

### multipage-inject.AC3: Variable-width offset values handled via convergence
- **multipage-inject.AC3.1 Success:** computeShift returns a stable result for normal inputs (no digit-width crossings)
- **multipage-inject.AC3.2 Edge:** An offset crossing a decimal digit boundary (e.g., 99990 + shift produces a 6-digit value where 99990 was 5 digits) causes computeShift to include the extra byte in the final shift
- **multipage-inject.AC3.3 Edge:** computeShift always terminates (convergence guaranteed)

### multipage-inject.AC4: Existing single-page behavior unchanged
- **multipage-inject.AC4.1 Success:** InjectRecognText on a single-page note produces byte-identical output before and after this change
- **multipage-inject.AC4.2 Success:** InjectRecognText on the last page of a multi-page note (previously the only supported case) produces correct output

### multipage-inject.AC5: Test corpus validation
- **multipage-inject.AC5.1 Success:** Inject into each of pages 0, 1, 2 of gosnstd.note → all 3 pages remain readable, all MAINLAYER/BGLAYER/TOTALPATH blocks reachable
- **multipage-inject.AC5.2 Success:** Inject into each of pages 0, 1, 2 of gosnrtr.note → same, covering both replacement and the out-of-order file layout
- **multipage-inject.AC5.3 Success:** Inject twice into any page (idempotency) → second injection produces stable, valid output
- **multipage-inject.AC5.4 Failure:** Note with unexpected layout (data block at offset > insertionPoint not reachable by block-boundary walking) returns a descriptive error rather than emitting a corrupt file

## Glossary

- **`.note` file**: Proprietary binary container format used by Supernote e-ink devices to store handwritten notes. Consists of a magic header, a sequence of length-prefixed data blocks, a footer metadata block, and an 8-byte tail that records the footer's offset.
- **RECOGNTEXT**: A page metadata tag whose value is the file offset of a data block containing base64-encoded handwriting recognition output (MyScript iink JSON). Setting this tag is how OCR results are written into a note.
- **RECOGNSTATUS**: A page metadata tag set to `"1"` to indicate that recognition text is present and valid.
- **RECOGNFILE**: A page metadata tag referencing an auxiliary recognition data block; included in offset relocation alongside RECOGNTEXT.
- **page metadata block**: A length-prefixed block of `<KEY:VALUE>` tags describing one page — including pointers (as byte offsets) to its layer data, recognition text, and stroke path data. Located via the footer's `PAGE{N}` tag.
- **footer**: The last metadata block in the file, containing `PAGE{N}` tags that point to each page's metadata block, plus `TITLE_*` and `KEYWORD_*` tags for annotations. Its own offset is stored in the final 4 bytes of the file (the tail).
- **tail**: The last 8 bytes of a `.note` file: the literal string `"tail"` followed by a little-endian uint32 giving the footer's byte offset. The tail is the entry point for parsing the file in reverse.
- **MAINLAYER / BGLAYER**: Page metadata tags whose values are file offsets pointing to layer header blocks. MAINLAYER holds the user's ink strokes; BGLAYER holds the page background (often a shared template bitmap stored near the start of the file).
- **LAYERBITMAP**: A tag inside a layer header block whose value is the file offset of the actual compressed bitmap data for that layer.
- **TOTALPATH**: A page metadata tag whose value is the file offset of the stroke-path data block for that page.
- **KEYWORDSITE**: A tag nested inside a KEYWORD annotation block in the footer, storing a file offset; must be relocated along with all other offset-valued tags.
- **insertionPoint**: The file byte offset at which the new RECOGNTEXT block is inserted — equal to the target page's metadata offset. Everything from this point onward shifts in the output file.
- **update set**: The collection of block file offsets that must be rebuilt (not emitted verbatim) because they contain offset-valued tags that need incrementing.
- **fixed-point / convergence (shift computation)**: An iterative algorithm that re-runs a calculation until the output stops changing. Here it is used because incrementing a decimal integer offset can increase its string length (digit width), which itself increases the total shift, which could in turn affect other offsets. The loop terminates when one full pass produces no additional digit-width crossings.
- **display order vs. file order**: Pages in a `.note` file are not necessarily stored in the order they appear to the user. The RTR test note (`gosnrtr.note`) demonstrates that display page 3's metadata can appear at a lower file offset than display pages 1 and 2. The implementation must treat "subsequent pages" as file-offset-subsequent, not display-index-subsequent.
- **RTR note (`gosnrtr.note`)**: A test corpus file generated by a Supernote device in real-time recognition mode, notable for its out-of-order page layout. Used to verify the implementation handles non-sequential file layouts correctly.
- **`gosnstd.note`**: A standard multi-page test corpus file with pages in sequential file order. Used alongside `gosnrtr.note` to cover both layout variants.
- **`replaceTagValue`**: An existing helper in `note/write.go` that performs a regex substitution to update a single `<KEY:VALUE>` tag in a raw byte slice.
- **`appendBlock`**: An existing helper that encodes a byte slice as a length-prefixed block (`[4-byte LE uint32][data]`), the universal framing used by all blocks in the `.note` format.
- **`BlockAt`**: An existing method on `Note` that reads the body of a length-prefixed block at a given file offset, used to verify that an offset resolves to a reachable, in-bounds block.
- **`sndump`**: A command-line debugging tool in `cmd/sndump/` that prints the decoded structure of a `.note` file, including page layout, stroke data, and annotation offsets.
- **`note/relocate.go`**: New file introduced by this design to house `collectOffsets`, `computeShift`, `buildOffsetMap`, `buildUpdateSet`, and `rebuildBlock`, keeping injection logic in `write.go` separate from relocation mechanics.
- **dead space**: Bytes in the output file that are no longer referenced by any metadata pointer. Existing RECOGNTEXT blocks become dead space when replaced, because the design updates only the pointer rather than removing the old block. Compaction is out of scope.
- **MyScript iink**: The handwriting recognition engine used by Supernote devices. Its JSON output format is what gets base64-encoded and stored in RECOGNTEXT blocks.
- **little-endian uint32 (LE uint32)**: A 4-byte integer stored least-significant byte first. All block length prefixes and the tail footer offset in the `.note` format use this encoding.

## Architecture

`InjectRecognText` is extended using a targeted metadata rebuild with a fixed-point shift calculation. The key insight from binary analysis of real `.note` files is that the format is interleaved: `[pageK-data][pageK-meta][pageK+1-data][pageK+1-meta]...[footer][tail]`. Pages are not stored in display order in the file — the RTR test note has page 3's metadata appearing at a lower offset than pages 1 and 2.

### Offset relocation

When a new RECOGNTEXT block of length `S` is inserted at `insertionPoint` (= target page's metadata offset), every byte from `insertionPoint` onwards shifts by `S` plus any bytes added by digit-width changes in updated tag values. The final shift is computed by fixed-point iteration:

1. Seed `shift = len(recognBlock)`
2. Collect all offset values `O > insertionPoint` from: footer `PAGE{N}`/`TITLE_*`/`KEYWORD_*` tags; each subsequent page's metadata tags (`MAINLAYER`, `BGLAYER`, `TOTALPATH`, `RECOGNTEXT`, `RECOGNFILE`); `LAYERBITMAP` inside any MAINLAYER/BGLAYER block whose file offset is also `> insertionPoint`; `KEYWORDSITE` inside KEYWORD blocks whose offsets are `> insertionPoint`
3. For each such `O`: if `len(strconv.Itoa(O + shift)) > len(strconv.Itoa(O))`, increment `shift` by 1 and restart
4. Convergence when no digit-width change occurs (typically 0–1 extra iterations in practice)

"Subsequent pages" means pages whose metadata file offset is greater than `insertionPoint`, sorted by file offset — not by display index.

### Update set

The **update set** is the collection of block offsets that need rebuilding:
- All page metadata blocks whose file offset is `> insertionPoint`
- All MAINLAYER and BGLAYER blocks at file offsets `> insertionPoint` (these contain `LAYERBITMAP` tags that may also shift)
- The footer block

BGLAYER blocks commonly share a template bitmap stored near the start of the file (offset 330 in the test corpus), so their `LAYERBITMAP` value is below `insertionPoint` and needs no update — but this is verified per-block, not assumed.

### Emit

1. `raw[:insertionPoint]` — verbatim (all data before target page's metadata)
2. `recognBlock` — the new RECOGNTEXT block
3. Walk original file from `pageMetaOff` to `footerOff` block-by-block using 4-byte length prefixes:
   - If the block's original offset is in the update set: emit `rebuildBlock(block, offsetMap)` where `offsetMap = {O → O + finalShift}` for all collected offsets
   - Otherwise: emit the block verbatim
4. `rebuildBlock(footer, offsetMap)` — updates `PAGE{N}`, `TITLE_*`, `KEYWORD_*` tags
5. `"tail"` + uint32(`footerOff + finalShift`)

`rebuildBlock` applies `replaceTagValue` for each key in the `offsetMap` whose value appears as a tag value in the block. Since tag keys are explicit, there is no ambiguity with raw binary data.

The target page's metadata is part of step 3: its `RECOGNTEXT` tag is set to `pageMetaOff` (the literal insertion offset, which is less than `insertionPoint + finalShift`) and `RECOGNSTATUS` set to `"1"`. This is handled as a special case within `rebuildBlock` for that block.

### Layout invariant guard

If any MAINLAYER/BGLAYER/TOTALPATH data-block offset found in a subsequent page's metadata is `> insertionPoint` but is not reachable by walking block boundaries forward from `pageMetaOff`, the function returns an error rather than emitting a potentially corrupt file.

## Existing Patterns

`note/write.go` already establishes the core patterns this design extends:

- **Append-based byte slice construction**: output is built by appending segments of `n.raw` and rebuilt blocks into a new `[]byte`. The multi-page emit follows the same pattern.
- **`replaceTagValue`**: regex-based tag value replacement in raw metadata bytes. Used as-is; `rebuildBlock` wraps it in a loop over the offset map.
- **`appendBlock`**: 4-byte length-prefix encoding. Used as-is.
- **`footerPageOffset`**: footer tag lookup. Reused to collect page metadata offsets.

The new `collectOffsets`, `computeShift`, and `rebuildBlock` functions are placed in a new file `note/relocate.go` to keep `write.go` focused on the injection logic.

No new dependencies. No changes to the public API surface — `InjectRecognText(pageIdx int, content RecognContent) ([]byte, error)` is unchanged.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: Offset collection and shift computation

**Goal:** Build the infrastructure that identifies which offsets need relocation and computes the final byte shift.

**Components:**
- `note/relocate.go` — new file containing:
  - `collectOffsets(n *Note, insertionPoint int) []int` — returns all offset values `> insertionPoint` from footer PAGE/TITLE/KEYWORD tags, all page metadata tags (MAINLAYER, BGLAYER, TOTALPATH, RECOGNTEXT, RECOGNFILE), and LAYERBITMAP values inside layer blocks whose own offset is `> insertionPoint`
  - `computeShift(baseShift int, affectedOffsets []int) int` — fixed-point loop: increments shift when any `O + shift` crosses a decimal digit boundary versus `O`; returns when stable
  - `buildOffsetMap(affectedOffsets []int, finalShift int) map[int]int` — returns `{O → O + finalShift}` for each collected offset
  - `buildUpdateSet(n *Note, insertionPoint int) map[int]bool` — returns file offsets of all blocks that need rebuilding (page metas after insertionPoint, layer blocks after insertionPoint, footer)
- `note/relocate_test.go` — unit tests verifying correct offset collection and shift calculation against both `gosnstd.note` and `gosnrtr.note`

**Dependencies:** None (extends existing parsed `Note` struct).

**Done when:** `collectOffsets` returns the correct set for both test files (verified against the empirical offset values from binary analysis); `computeShift` converges correctly including digit-width crossing cases; tests pass.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Multi-page emit and integration

**Goal:** Replace the single-page layout guard with the full multi-page emit and integrate into `InjectRecognText`.

**Components:**
- `note/relocate.go` (extended) — add `rebuildBlock(block []byte, offsetMap map[int]int) []byte` that applies `replaceTagValue` for each offset map entry matching a tag value in the block
- `note/write.go` (modified):
  - Remove the `pageMetaOff+4+pageMetaLen != footerOff` layout guard
  - For single-page notes or the last page of a multi-page note: existing fast path (no subsequent pages, only footer `PAGE{N}` and file-end offset need updating) — keeps current behavior
  - For any other page: full block-level walk from `pageMetaOff` to `footerOff`, applying update set and offset map
  - Add layout invariant guard: if any subsequent page's data-block offset is unreachable by block-boundary walking, return a descriptive error
- `note/write_test.go` (extended) — integration tests:
  - Inject into each of the 3 pages of `gosnstd.note` (first injection): verify round-trip parse succeeds, all MAINLAYER/BGLAYER/TOTALPATH/RECOGNTEXT offsets in all pages resolve to readable blocks
  - Inject into each of the 3 pages of `gosnrtr.note` (replacement): verify same properties, including the out-of-order page case
  - Idempotency: inject twice, verify output is stable
  - Single-page regression: single-page note injection produces identical output to pre-fix behavior

**Dependencies:** Phase 1 (offset collection and shift computation infrastructure).

**Done when:** All tests pass; `go vet ./...` clean; single-page and last-page behavior unchanged; multi-page injection succeeds for all tested page positions.
<!-- END_PHASE_2 -->

## Additional Considerations

**Dead space:** Existing RECOGNTEXT blocks are left as unreferenced bytes when replaced (same as current single-page behavior). Files grow slightly on repeated injection. Compaction is out of scope for this design.

**TITLE and KEYWORD footer tags:** The test corpus contains no TITLE or KEYWORD annotations. `collectOffsets` handles them defensively — their offset values are included if present and `> insertionPoint`. No separate test coverage is required for this design.

**KEYWORDSITE:** Nested offset inside KEYWORD blocks in the footer. Handled generically by `collectOffsets` reading KEYWORD block contents. Same defensive inclusion applies.

**Page file-order vs display-order:** The implementation must sort page metadata offsets by file position, not by display index (`pageIdx`). The RTR test note (`gosnrtr.note`) demonstrates that display page 3 can appear at a lower file offset than display pages 1 and 2. "Subsequent pages" in the update set means file-offset-subsequent, not display-index-subsequent.

**sndump enhancement:** A `-offsets` flag to `cmd/sndump/main.go` showing page metadata offsets, data block offsets, and LAYERBITMAP values would aid future debugging. Out of scope here but noted.
