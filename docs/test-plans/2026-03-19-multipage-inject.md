# Human Test Plan: Multi-Page InjectRecognText

Generated from: `docs/implementation-plans/2026-03-19-multipage-inject/`

## Prerequisites

- Go toolchain installed
- Working directory: `/home/sysop/src/go-sn/.worktrees/multipage-inject/`
- All automated tests passing: `go test -C /home/sysop/src/go-sn/.worktrees/multipage-inject ./note/ -v -count=1`
- Test corpus files present in `testdata/`: `gosnstd.note` (3-page standard), `gosnrtr.note` (3-page RTR), `20260318_154108 std one line.note` (single-page)

## Phase 1: Verify AC5.4 — Unexpected Layout Guard

| Step | Action | Expected |
|------|--------|----------|
| 1 | Open `note/write.go`, navigate to lines ~170–196 | See a loop over `n.Pages` that checks `knownSet[dataOff]` for each MAINLAYER/BGLAYER/TOTALPATH offset past `insertionPoint` |
| 2 | Confirm the error message reads: `"page %d %s block at offset %d is not a recognized tagged block; file layout unexpected"` | Error message is present and includes page index, tag name, offset value, and "file layout unexpected" |
| 3 | Run `go test -C ... ./note/ -run TestInjectRecognText_MultiPage_StdNote -v` | All three subtests (page0, page1, page2) pass — guard does not trigger on valid files |
| 4 | Run `go test -C ... ./note/ -run TestInjectRecognText_MultiPage_RTRNote -v` | All three subtests pass — no false positives on well-formed RTR file |
| 5 | (Optional) Use a hex editor to open a copy of `testdata/gosnstd.note`. Locate page 1 MAINLAYER offset in the footer/metadata. Change it to an offset not on a block boundary. Write a short Go test calling `InjectRecognText(0, ...)` on the modified file | Returns error containing "file layout unexpected" |

## Phase 2: End-to-End Round-Trip Validation

Confirm a note injected on a non-last page can be reloaded and re-injected on a different page without corruption.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Load `testdata/gosnstd.note` | Load succeeds; note has 3 pages |
| 2 | Call `InjectRecognText(0, content)` with label "page zero text" | Returns output bytes, no error |
| 3 | Write output to temp file, then `Load` on that file | Load succeeds; re-parsed note has 3 pages |
| 4 | Call `InjectRecognText(2, content)` on re-parsed note with label "page two text" | Returns output bytes, no error |
| 5 | Re-parse second output. Call `ReadRecognText` for page 0 and page 2 | Page 0 → "page zero text"; page 2 → "page two text" |
| 6 | Verify page 1 has no RECOGNTEXT (`ReadRecognText` returns nil) | Page 1 has no injected text |

## Phase 3: Cross-Inject on RTR Note

Confirm the out-of-order file layout does not cause offset corruption when injecting sequentially across multiple pages.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Load `testdata/gosnrtr.note` | 3 pages loaded; file order differs from display order |
| 2 | Inject into page 1 (middle display page), save output | No error |
| 3 | Reload output, inject into page 0 | No error |
| 4 | Reload output, inject into page 2 | No error |
| 5 | On final output, iterate all pages; call `BlockAt` for every non-zero MAINLAYER, BGLAYER, TOTALPATH, and RECOGNTEXT offset | All `BlockAt` calls succeed; no offset points into the middle of another block |

## Traceability

| Acceptance Criterion | Automated Test | Manual Step |
|----------------------|----------------|-------------|
| AC1.1 Inject into page 0 of 3-page note | `TestInjectRecognText_MultiPage_StdNote/page0` | — |
| AC1.2 Inject into page 1 (middle) | `TestInjectRecognText_MultiPage_StdNote/page1` | — |
| AC1.3 Inject into last page | `TestInjectRecognText_MultiPage_StdNote/page2` | — |
| AC1.4 RTR note non-sequential layout | `TestInjectRecognText_MultiPage_RTRNote/page0,1,2` | — |
| AC2.1 Footer PAGE tags resolve | `TestInjectRecognText_MultiPage_StdNote` (verifyAllBlocksReachable) | — |
| AC2.2 MAINLAYER/BGLAYER/TOTALPATH resolve | `TestInjectRecognText_MultiPage_StdNote` + RTR | — |
| AC2.3 LAYERBITMAP correctly incremented | `TestInjectRecognText_MultiPage_StdNote` + BGLayerUnchanged | — |
| AC2.4 RECOGNTEXT in RTR subsequent pages | `TestInjectRecognText_MultiPage_RTRNote` (RECOGNTEXT check) | — |
| AC2.5 BGLAYER bitmap below insertion unchanged | `TestInjectRecognText_MultiPage_BGLayerUnchanged` | — |
| AC3.1 computeShift stable | `TestComputeShift_Stable` | — |
| AC3.2 Digit crossing adds extra byte | `TestComputeShift_DigitCrossing` | — |
| AC3.3 computeShift always terminates | `TestComputeShift_Terminates` | — |
| AC4.1 Single-page regression | `TestInjectRecognText_SinglePage_Regression` | — |
| AC4.2 Last page of multi-page | `TestInjectRecognText_MultiPage_StdNote/page2` | — |
| AC5.1 All pages of gosnstd.note | `TestInjectRecognText_MultiPage_StdNote` (3 subtests) | — |
| AC5.2 All pages of gosnrtr.note | `TestInjectRecognText_MultiPage_RTRNote` (3 subtests) | — |
| AC5.3 Idempotency | `TestInjectRecognText_MultiPage_Idempotent` | — |
| AC5.4 Unexpected layout returns error | — (designated manual) | Phase 1: steps 1–5 |
| — | — | Phase 2: End-to-end round-trip (steps 1–6) |
| — | — | Phase 3: Cross-inject on RTR (steps 1–5) |
