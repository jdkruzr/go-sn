# Multi-Page InjectRecognText -- Test Requirements

Maps each acceptance criterion from the `multipage-inject` design to automated tests or human verification.

All automated tests are Go tests in `note/relocate_test.go` or `note/write_test.go` (package `note`).

---

## multipage-inject.AC1: InjectRecognText works for any page position

| Criterion | Type | Test File | Test Function | Notes |
|-----------|------|-----------|---------------|-------|
| AC1.1 -- Inject into page 0 (first page) of 3-page note, output valid | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote/page0` | Subtest of table-driven test; calls `InjectRecognText(0, ...)` on `gosnstd.note`, round-trips, verifies RECOGNSTATUS=1 and label match |
| AC1.2 -- Inject into page 1 (middle page), output valid | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote/page1` | Same table-driven test, subtest for page 1 |
| AC1.3 -- Inject into last page of multi-page note (regression) | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote/page2` | Same table-driven test, subtest for page 2; verifies pre-fix behavior preserved |
| AC1.4 -- Inject into RTR note with non-sequential file order | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_RTRNote/page0` | Subtests for pages 0, 1, 2 on `gosnrtr.note`; display page 2 metadata appears before display page 0 in the file |

---

## multipage-inject.AC2: All offsets past the insertion point are correctly relocated

| Criterion | Type | Test File | Test Function | Notes |
|-----------|------|-----------|---------------|-------|
| AC2.1 -- Footer PAGE{N} tags resolve to readable metadata blocks | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote` | `verifyAllBlocksReachable` helper calls `BlockAt` for every MAINLAYER/BGLAYER/TOTALPATH offset in every page; a parseable round-trip implicitly validates PAGE{N} tags since `parse()` uses them |
| AC2.2 -- MAINLAYER, BGLAYER, TOTALPATH resolve to readable blocks | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote` | Same `verifyAllBlocksReachable` helper; also covered by RTR note test |
| AC2.3 -- LAYERBITMAP inside MAINLAYER at offset > insertionPoint is correctly incremented | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote` | Indirectly verified: if MAINLAYER block is readable via `BlockAt` (AC2.2) and the round-tripped note parses without error, LAYERBITMAP must resolve correctly. Direct LAYERBITMAP value assertion is in `TestInjectRecognText_MultiPage_BGLayerUnchanged` (for the unchanged case) |
| AC2.4 -- RECOGNTEXT in subsequent page metas (RTR) points correctly | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_RTRNote` | Iterates all pages in round-tripped output; calls `BlockAt` on every non-zero RECOGNTEXT offset |
| AC2.5 -- BGLAYER LAYERBITMAP below insertionPoint is unchanged | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_BGLayerUnchanged` | Records BGLAYER LAYERBITMAP for page 1 before injection into page 0; compares after injection; asserts unchanged when bitmap offset < insertionPoint |

---

## multipage-inject.AC3: Variable-width offset values handled via convergence

| Criterion | Type | Test File | Test Function | Notes |
|-----------|------|-----------|---------------|-------|
| AC3.1 -- computeShift stable for normal inputs (no digit crossings) | Unit | `note/relocate_test.go` | `TestComputeShift_Stable` | Passes offsets [1000, 2000, 3000] with baseShift=100; asserts result == 100 |
| AC3.2 -- Offset crossing decimal digit boundary adds extra byte | Unit | `note/relocate_test.go` | `TestComputeShift_DigitCrossing` | Offset 99990 + shift 10 = 100000 (5->6 digits); asserts result == 11 |
| AC3.3 -- computeShift always terminates (convergence guaranteed) | Unit | `note/relocate_test.go` | `TestComputeShift_Terminates` | Passes offsets [9, 99, 999, 9999, 99999, 999999] with baseShift=1; all cross a digit boundary; asserts result == 7 (1 base + 6 crossings) |

---

## multipage-inject.AC4: Existing single-page behavior unchanged

| Criterion | Type | Test File | Test Function | Notes |
|-----------|------|-----------|---------------|-------|
| AC4.1 -- Single-page note produces byte-identical output | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_SinglePage_Regression` | Loads `20260318_154108 std one line.note`, injects, round-trips, verifies RECOGNSTATUS and label; existing `TestInjectRecognText_StandardNote` also covers this |
| AC4.2 -- Last page of multi-page note produces correct output | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote/page2` | Subtest for page 2 (the last page); same validation as AC1.3 |

---

## multipage-inject.AC5: Test corpus validation

| Criterion | Type | Test File | Test Function | Notes |
|-----------|------|-----------|---------------|-------|
| AC5.1 -- Inject into each page of gosnstd.note, all blocks reachable | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_StdNote` | Three subtests (page0, page1, page2); each calls `verifyAllBlocksReachable` |
| AC5.2 -- Inject into each page of gosnrtr.note, all blocks reachable | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_RTRNote` | Three subtests (page0, page1, page2); each calls `verifyAllBlocksReachable` and checks all RECOGNTEXT offsets |
| AC5.3 -- Inject twice (idempotency), stable valid output | Unit (integration-style) | `note/write_test.go` | `TestInjectRecognText_MultiPage_Idempotent` | Injects into page 1 of `gosnstd.note` twice with different labels; verifies second label survives round-trip and all blocks reachable |
| AC5.4 -- Unexpected layout returns descriptive error | Unit (integration-style) | `note/write_test.go` | _(see Human Verification below)_ | No synthetic malformed corpus file exists; guard is tested implicitly by all valid notes not triggering it |

---

## Human Verification

### AC5.4 -- Unexpected layout returns descriptive error

**Justification:** This criterion requires a `.note` file with an intentionally malformed block layout where a data-block offset referenced by a subsequent page's metadata is not reachable by walking block boundaries forward from the insertion point. No such file exists in the test corpus, and the design explicitly does not specify one. Constructing a synthetic malformed binary would be brittle and tightly coupled to the file format's internal layout.

**Verification approach:**
1. Confirm the layout invariant guard exists in `note/write.go` -- the `if !reachable[dataOff]` check that returns a `fmt.Errorf` with the message `"is not reachable by block-boundary walk"`.
2. Confirm that all valid corpus tests (`TestInjectRecognText_MultiPage_StdNote`, `TestInjectRecognText_MultiPage_RTRNote`) pass without triggering the guard, proving it does not false-positive on well-formed files.
3. Optionally: use a hex editor to create a synthetic `.note` file with a MAINLAYER offset that points into the middle of another block (not on a block boundary). Run `InjectRecognText` on it and verify the error message matches the expected format: `"page N MAINLAYER block at offset X is not reachable by block-boundary walk from insertionPoint Y; file layout unexpected"`.

**Risk if not automated:** Low. The guard is a defensive check against a file layout that has never been observed in practice. The code path is straightforward (a map lookup returning false) and unlikely to regress.

---

## Supporting Test Infrastructure

| Component | File | Function | Purpose |
|-----------|------|----------|---------|
| Note loader | `note/write_test.go` | `loadNote` | Loads a `.note` file from `testdata/` directory |
| Round-trip parser | `note/write_test.go` | `roundTripNote` | Re-parses output bytes into a `*Note` to validate structural integrity |
| Page-specific content reader | `note/write_test.go` | `readContentForPage` | Reads and unmarshals RECOGNTEXT from any page (extends existing `readContent` which is page-0 only) |
| Block reachability checker | `note/write_test.go` | `verifyAllBlocksReachable` | Iterates all pages, calls `BlockAt` on every MAINLAYER/BGLAYER/TOTALPATH offset |
| Offset collection (Phase 1) | `note/relocate_test.go` | `TestCollectOffsets_StandardNote` | Verifies `collectOffsets` returns correct set for `gosnstd.note` |
| Offset collection (RTR) | `note/relocate_test.go` | `TestCollectOffsets_RTRNote` | Verifies `collectOffsets` returns correct set for `gosnrtr.note` (out-of-order layout) |
| Offset map builder | `note/relocate_test.go` | `TestBuildOffsetMap` | Verifies `buildOffsetMap` produces `{O -> O+shift}` correctly |
| Update set builder | `note/relocate_test.go` | `TestBuildUpdateSet_StandardNote` | Verifies footer and subsequent page-meta offsets are in the update set |

---

## Test Corpus Files

| File | Pages | Layout | Used By |
|------|-------|--------|---------|
| `testdata/gosnstd.note` | 3 | Sequential file order | AC1.1, AC1.2, AC1.3, AC2.1-AC2.3, AC2.5, AC5.1, AC5.3 |
| `testdata/gosnrtr.note` | 3 | Non-sequential (RTR, out-of-order) | AC1.4, AC2.4, AC5.2 |
| `testdata/20260318_154108 std one line.note` | 1 | Single page, standard | AC4.1 |
| `testdata/20260318_154754 rtr one line.note` | 1 | Single page, RTR | Existing tests (not directly mapped to multipage-inject ACs) |
