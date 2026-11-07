// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package gcf

import (
	"io"
	"strings"

	jsonv1 "encoding/json"
)

// DefaultMinRows is the smallest array CompactTabular will fold when the
// caller passes 0. It is the OmniRoute default: below it, the fence and
// profile-line overhead typically outweigh the keys saved per row.
const DefaultMinRows = 8

// fenceOpen and fenceClose delimit the block inside message text. The
// gcf-generic info string tells the decoder (and the model) which profile to
// apply, mirroring how OmniRoute marks its folded blocks.
const (
	fenceOpen  = "```gcf-generic"
	fenceClose = "```"
	jsonFence  = "```json"
)

// CompactTabular scans text for JSON arrays of objects and replaces each one
// with a folded GCF block, but only where the fold is strictly smaller.
//
// Two shapes are recognized: a whole text that is itself a JSON array, and
// ```json fenced blocks embedded in surrounding prose. Everything else —
// arrays below minRows, arrays containing non-objects, invalid JSON — passes
// through untouched. minRows <= 0 selects DefaultMinRows.
//
// The no-regression gate compares the new fenced block against the exact text
// it would replace: if the fold does not pay for its own fence, profile line
// and header, the original wins. Compaction must never make the context
// larger.
func CompactTabular(text string, minRows int) (string, bool) {
	if minRows <= 0 {
		minRows = DefaultMinRows
	}
	if replacement, ok := compactJSONArrayText(text, "", minRows); ok {
		return replacement, true
	}
	return compactFencedBlocks(text, minRows)
}

// compactJSONArrayText encodes candidate (a JSON array of objects) as a
// fenced GCF block gated on strictly-smaller size. span is the exact text the
// replacement would remove (the whole text, or one fenced block); an empty
// span means the whole-text form where the fence overhead must beat the raw
// array itself.
func compactJSONArrayText(candidate, span string, minRows int) (string, bool) {
	rows, ok := parseJSONArray(candidate)
	if !ok || len(rows) < minRows {
		return "", false
	}
	block, err := Encode(rows)
	if err != nil {
		// Unencodable rows (e.g. cyclic or unsupported types) leave the
		// original text in place rather than failing the whole message.
		return "", false
	}
	fenced := fenceOpen + "\n" + block + "\n" + fenceClose
	if span == "" {
		span = candidate
	}
	if len(fenced) >= len(span) {
		return "", false
	}
	return fenced, true
}

// parseJSONArray parses s as exactly one JSON array whose elements are all
// objects, returning the rows. It decodes with json/v1's UseNumber because
// encoding/json/v2 unmarshals every number into float64: integer literals
// above 2^53 (snowflakes, ids, timestamps in ms) would be silently rounded
// before Encode ever sees them, and json.Number keeps the literal exact.
func parseJSONArray(s string) ([]map[string]any, bool) {
	dec := jsonv1.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	// Anything after the first value (except whitespace) is not a plain
	// array; treat the text as prose and leave it alone.
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	rows := make([]map[string]any, 0, len(arr))
	for _, el := range arr {
		m, ok := el.(map[string]any)
		if !ok {
			return nil, false
		}
		rows = append(rows, m)
	}
	return rows, true
}

// compactFencedBlocks rewrites every ```json fenced block in the text whose
// content is a compressible JSON array, leaving all other lines untouched.
func compactFencedBlocks(text string, minRows int) (string, bool) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines)+8)
	changed := false
	for i := 0; i < len(lines); {
		line := lines[i]
		if !isJSONFenceOpen(line) {
			out = append(out, line)
			i++
			continue
		}
		closeIdx := findFenceClose(lines, i+1)
		if closeIdx < 0 {
			// Unterminated fence: prose, not a data block. Keep scanning.
			out = append(out, line)
			i++
			continue
		}
		inner := strings.Join(lines[i+1:closeIdx], "\n")
		span := strings.Join(lines[i:closeIdx+1], "\n")
		if replacement, ok := compactJSONArrayText(inner, span, minRows); ok {
			out = append(out, strings.Split(replacement, "\n")...)
			changed = true
		} else {
			out = append(out, lines[i:closeIdx+1]...)
		}
		i = closeIdx + 1
	}
	if !changed {
		return text, false
	}
	return strings.Join(out, "\n"), true
}

// isJSONFenceOpen reports whether a line opens a ```json fence.
func isJSONFenceOpen(line string) bool {
	return strings.TrimSpace(line) == jsonFence
}

// findFenceClose returns the index of the closing fence line at or after
// start, or -1 when the fence is never closed.
func findFenceClose(lines []string, start int) int {
	for j := start; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == fenceClose {
			return j
		}
	}
	return -1
}
