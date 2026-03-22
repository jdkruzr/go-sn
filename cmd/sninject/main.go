// pattern: Imperative Shell

// sninject processes a Supernote .note file through a vision-API OCR pipeline
// and injects searchable RECOGNTEXT into each page.
//
// Standard notes (FILE_RECOGN_TYPE=0) are the primary use case. The device's
// handwriting search reads RECOGNTEXT regardless of note type, so injecting
// recognition data into Standard notes makes them searchable — something the
// device cannot do natively without Real-Time Recognition mode.
//
// RTR notes (FILE_RECOGN_TYPE=1) are refused by default because the device's
// AUTO_CONVERT process fires ~40 seconds after opening an RTR note and
// overwrites RECOGNTEXT from its own recognition engine, destroying any
// injected text. Use --force-rtr to override this, but understand that
// injected data WILL be clobbered when the note is opened on the device.
//
// This is a standalone tool for debugging and experimentation — it does not
// interact with any database, sync service, or file watcher.
//
// Usage:
//
//	sninject -in note.note -out modified.note [options]
//
// Options:
//
//	-api-url    OCR API base URL (default: http://localhost:8000)
//	-api-key    OCR API key (optional for unauthenticated local endpoints)
//	-model      OCR model name (default: Qwen/Qwen3-VL-8B-Instruct)
//	-format     API format: "openai" or "anthropic" (default: openai)
//	-force-rtr  Allow processing RTR notes (injected text WILL be clobbered by device)
//	-dry-run    OCR and print results without modifying the file
//
// Examples:
//
//	# Inject OCR into a Standard note using a local vLLM endpoint
//	sninject -in my.note -out my_ocr.note -api-url http://192.168.1.5:8000
//
//	# OpenRouter with Claude
//	sninject -in my.note -out my_ocr.note \
//	  -api-url https://openrouter.ai/api \
//	  -api-key sk-or-... \
//	  -model anthropic/claude-sonnet-4-20250514 \
//	  -format anthropic
//
//	# Dry run — just see what OCR produces
//	sninject -in my.note -out /dev/null -dry-run
//
//	# Force processing an RTR note (injected text will be clobbered on device)
//	sninject -in rtr.note -out rtr_ocr.note --force-rtr
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"

	gosnote "github.com/jdkruzr/go-sn/note"
)

func main() {
	inPath := flag.String("in", "", "input .note file (required)")
	outPath := flag.String("out", "", "output .note file (required)")
	apiURL := flag.String("api-url", "http://localhost:8000", "OCR API base URL")
	apiKey := flag.String("api-key", "", "OCR API key")
	model := flag.String("model", "Qwen/Qwen3-VL-8B-Instruct", "OCR model name")
	format := flag.String("format", "openai", "API format: openai or anthropic")
	forceRTR := flag.Bool("force-rtr", false, "allow processing RTR notes (injected text WILL be clobbered by device)")
	dryRun := flag.Bool("dry-run", false, "OCR only, don't modify file")
	flag.Parse()

	if *inPath == "" || *outPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	f, err := os.Open(*inPath)
	if err != nil {
		fatal("open: %v", err)
	}
	n, err := gosnote.Load(f)
	f.Close()
	if err != nil {
		fatal("load: %v", err)
	}

	isRTR := n.Header["FILE_RECOGN_TYPE"] == "1"

	fmt.Printf("Loaded %s: %d pages, %s, APPLY_EQUIPMENT=%s\n",
		*inPath, len(n.Pages), noteTypeLabel(isRTR), n.Header["APPLY_EQUIPMENT"])

	if isRTR && !*forceRTR {
		fmt.Fprintf(os.Stderr, "\n"+
			"ERROR: This is an RTR (Real-Time Recognition) note.\n"+
			"\n"+
			"The device's AUTO_CONVERT process fires ~40 seconds after opening an\n"+
			"RTR note and re-derives RECOGNTEXT from its own recognition engine,\n"+
			"destroying any text injected by sninject.\n"+
			"\n"+
			"To inject into RTR notes anyway, use --force-rtr. The injected text\n"+
			"will be searchable until the note is next opened on the device.\n"+
			"\n"+
			"Consider converting the note to Standard mode on the device first,\n"+
			"or creating a new Standard note with the same content.\n")
		os.Exit(1)
	}

	if isRTR && *forceRTR {
		fmt.Fprintf(os.Stderr, "\n"+
			"WARNING: Processing RTR note with --force-rtr.\n"+
			"Injected RECOGNTEXT WILL be overwritten when this note is opened on the device.\n"+
			"Zeroing RECOGNFILE and setting FILE_RECOGN_TYPE=0 to suppress AUTO_CONVERT.\n\n")
	}

	equipment := n.Header["APPLY_EQUIPMENT"]
	raw, err := os.ReadFile(*inPath)
	if err != nil {
		fatal("read: %v", err)
	}

	currentNote := n
	for pageIdx := range n.Pages {
		p := currentNote.Pages[pageIdx]
		fmt.Printf("\nPage %d: RECOGNSTATUS=%s\n", pageIdx, p.Meta["RECOGNSTATUS"])

		tp, err := currentNote.TotalPathData(p)
		if err != nil || tp == nil {
			fmt.Printf("  No TOTALPATH data, skipping\n")
			continue
		}

		pageW, pageH := currentNote.PageDimensions(p)
		objs, err := gosnote.DecodeObjects(tp, pageW, pageH)
		if err != nil {
			fmt.Printf("  DecodeObjects failed: %v, skipping\n", err)
			continue
		}
		img := gosnote.RenderObjects(objs, pageW, pageH, nil)

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			fmt.Printf("  JPEG encode failed: %v, skipping\n", err)
			continue
		}
		fmt.Printf("  Rendered %d bytes JPEG\n", buf.Len())

		var text string
		switch *format {
		case "openai":
			text, err = ocrOpenAI(*apiURL, *apiKey, *model, buf.Bytes())
		case "anthropic":
			text, err = ocrAnthropic(*apiURL, *apiKey, *model, buf.Bytes())
		default:
			fatal("unknown format %q (use openai or anthropic)", *format)
		}
		if err != nil {
			fatal("OCR page %d: %v", pageIdx, err)
		}
		fmt.Printf("  OCR: %q\n", truncate(text, 120))

		if *dryRun {
			continue
		}

		strokes, err := gosnote.DecodeTotalPath(tp, pageW, pageH)
		if err != nil {
			fmt.Printf("  No stroke data: %v\n", err)
			strokes = nil
		}
		var strokeBounds gosnote.Rect
		if strokes != nil {
			strokeBounds = gosnote.StrokeBounds(strokes)
		}

		content := gosnote.BuildRecognText(text, strokeBounds, equipment)
		newBytes, err := currentNote.InjectRecognText(pageIdx, content)
		if err != nil {
			fatal("inject page %d: %v", pageIdx, err)
		}
		raw = newBytes

		// Reload for next page — offsets shifted after injection.
		tmpF, _ := os.CreateTemp("", "sninject-*.note")
		tmpF.Write(raw)
		tmpName := tmpF.Name()
		tmpF.Close()
		f2, _ := os.Open(tmpName)
		currentNote, err = gosnote.Load(f2)
		f2.Close()
		os.Remove(tmpName)
		if err != nil {
			fatal("reload after page %d: %v", pageIdx, err)
		}
	}

	if *dryRun {
		fmt.Println("\nDry run — no file written.")
		return
	}

	// For RTR notes processed with --force-rtr: zero RECOGNFILE and set
	// FILE_RECOGN_TYPE=0 to suppress AUTO_CONVERT. Without this, the device
	// re-derives RECOGNTEXT ~40s after opening, clobbering injected text.
	if isRTR && *forceRTR {
		fmt.Printf("\nZeroing RECOGNFILE...\n")
		raw, err = zeroRecognFile(raw)
		if err != nil {
			fatal("zero RECOGNFILE: %v", err)
		}
		fmt.Printf("Setting FILE_RECOGN_TYPE=0...\n")
		raw, err = zeroFileRecognType(raw)
		if err != nil {
			fatal("clear RTR: %v", err)
		}
	}

	if err := os.WriteFile(*outPath, raw, 0644); err != nil {
		fatal("write: %v", err)
	}
	fmt.Printf("\nWrote %s (%d bytes)\n", *outPath, len(raw))

	// Verify
	f3, _ := os.Open(*outPath)
	n3, err := gosnote.Load(f3)
	f3.Close()
	if err != nil {
		fatal("verify: %v", err)
	}
	fmt.Printf("\nVerification:\n")
	fmt.Printf("  FILE_RECOGN_TYPE=%s (%s)\n", n3.Header["FILE_RECOGN_TYPE"], noteTypeLabel(n3.Header["FILE_RECOGN_TYPE"] == "1"))
	for i, pg := range n3.Pages {
		fmt.Printf("  Page %d: RECOGNFILE=%s RECOGNFILESTATUS=%s RECOGNSTATUS=%s\n",
			i, pg.Meta["RECOGNFILE"], pg.Meta["RECOGNFILESTATUS"], pg.Meta["RECOGNSTATUS"])
		rt, _ := n3.ReadRecognText(pg)
		if rt != nil {
			var v map[string]interface{}
			json.Unmarshal(rt, &v)
			if elems, ok := v["elements"].([]interface{}); ok {
				for _, e := range elems {
					if em, ok := e.(map[string]interface{}); ok {
						if label, ok := em["label"].(string); ok {
							fmt.Printf("    %q\n", truncate(label, 80))
						}
					}
				}
			}
		}
	}
}

func noteTypeLabel(isRTR bool) string {
	if isRTR {
		return "RTR (Real-Time Recognition)"
	}
	return "Standard"
}

func ocrOpenAI(baseURL, apiKey, model string, jpegData []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(jpegData)
	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "Transcribe all handwritten text in this image exactly as written. Output only the transcribed text, nothing else."},
					{"type": "image_url", "image_url": map[string]string{
						"url": "data:image/jpeg;base64," + b64,
					}},
				},
			},
		},
		"max_tokens": 2000,
	}
	return doOpenAIRequest(baseURL+"/v1/chat/completions", apiKey, reqBody)
}

func ocrAnthropic(baseURL, apiKey, model string, jpegData []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(jpegData)
	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 2000,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "image",
						"source": map[string]string{
							"type":       "base64",
							"media_type": "image/jpeg",
							"data":       b64,
						},
					},
					map[string]interface{}{
						"type": "text",
						"text": "Transcribe all handwritten text in this image exactly as written. Output only the transcribed text, nothing else.",
					},
				},
			},
		},
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", baseURL+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, respBody)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no content in response")
	}
	return result.Content[0].Text, nil
}

func doOpenAIRequest(url, apiKey string, reqBody interface{}) (string, error) {
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, respBody)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

// zeroFileRecognType sets FILE_RECOGN_TYPE in the file header to "0",
// making the device treat it as a non-RTR note (suppresses AUTO_CONVERT).
func zeroFileRecognType(raw []byte) ([]byte, error) {
	old := []byte("<FILE_RECOGN_TYPE:1>")
	new := []byte("<FILE_RECOGN_TYPE:0>")
	if !bytes.Contains(raw, old) {
		return raw, nil
	}
	return bytes.Replace(raw, old, new, 1), nil
}

func zeroRecognFile(raw []byte) ([]byte, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("file too short")
	}
	footerOff := int(binary.LittleEndian.Uint32(raw[len(raw)-4:]))
	if footerOff+4 > len(raw) {
		return nil, fmt.Errorf("footer offset out of bounds")
	}
	footerLen := int(binary.LittleEndian.Uint32(raw[footerOff:]))
	footerBody := raw[footerOff+4 : footerOff+4+footerLen]

	pageRe := regexp.MustCompile(`<PAGE(\d+):(\d+)>`)
	matches := pageRe.FindAllSubmatch(footerBody, -1)

	for _, m := range matches {
		pageNum, _ := strconv.Atoi(string(m[1]))
		pageOff, _ := strconv.Atoi(string(m[2]))

		if pageOff+4 > len(raw) {
			continue
		}
		metaLen := int(binary.LittleEndian.Uint32(raw[pageOff:]))
		if pageOff+4+metaLen > len(raw) {
			continue
		}
		metaBody := raw[pageOff+4 : pageOff+4+metaLen]

		// Zero RECOGNFILE using leading-zero padding to maintain exact tag length.
		// E.g. <RECOGNFILE:606006> becomes <RECOGNFILE:000000> — same byte count,
		// Atoi("000000") == 0, and no trailing garbage in the metadata block.
		newMeta := replaceTagPreserveLen(metaBody, "RECOGNFILE", "0")
		newMeta = replaceTagPreserveLen(newMeta, "RECOGNFILESTATUS", "0")

		if len(newMeta) != len(metaBody) {
			return nil, fmt.Errorf("page %d: meta length changed (%d -> %d)", pageNum, len(metaBody), len(newMeta))
		}
		copy(raw[pageOff+4:], newMeta)
	}
	return raw, nil
}

// replaceTagPreserveLen replaces a tag value while keeping the exact same byte length.
// The new value is left-padded with '0' to match the original value's length.
// E.g. <RECOGNFILE:606006> with newVal="0" becomes <RECOGNFILE:000000>.
func replaceTagPreserveLen(meta []byte, key, newVal string) []byte {
	re := regexp.MustCompile(`<` + regexp.QuoteMeta(key) + `:([^>]*)>`)
	return re.ReplaceAllFunc(meta, func(match []byte) []byte {
		inner := re.FindSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		origValLen := len(inner[1])
		padded := newVal
		for len(padded) < origValLen {
			padded = "0" + padded
		}
		return []byte("<" + key + ":" + padded + ">")
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
