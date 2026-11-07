// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package gcf

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	jsonv1 "encoding/json"
)

// Decode parses a GCF generic-profile block back into rows.
//
// It restores what Encode folded: ">"-joined column paths are unflattened
// into nested maps, sentinels are resolved (~ omits the key entirely, -
// becomes nil), .[N]: cells become primitive slices and ^ cells are parsed
// back from their compact JSON attachments. The declared row count N must
// match the number of row lines exactly — a surplus means the block was
// truncated or corrupted in flight and silently accepting it would present
// the model with rows that were never in the payload.
func Decode(block string) ([]map[string]any, error) {
	lines := splitLines(block)
	if len(lines) == 0 {
		return nil, fmt.Errorf("gcf: decode: empty block")
	}
	if lines[0] != profileLine {
		return nil, fmt.Errorf("gcf: decode: unsupported profile line %q", lines[0])
	}
	if len(lines) < 2 {
		return nil, fmt.Errorf("gcf: decode: missing header")
	}

	n, cols, err := parseHeader(lines[1])
	if err != nil {
		return nil, err
	}
	rowLines := lines[2:]
	if len(rowLines) > n {
		return nil, fmt.Errorf("gcf: decode: header declares %d rows, found %d (surplus)", n, len(rowLines))
	}
	if len(rowLines) < n {
		return nil, fmt.Errorf("gcf: decode: header declares %d rows, found %d", n, len(rowLines))
	}

	rows := make([]map[string]any, 0, n)
	for i, line := range rowLines {
		row, err := parseRow(i, line, cols)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// splitLines splits the block into lines, stripping CR (Windows line endings
// may survive transport) and the blank lines that fences or prose layout can
// add at the edges. Blank lines in the middle are NOT tolerated: they fail
// the cell-count check, which is the corruption signal.
func splitLines(block string) []string {
	raw := strings.Split(block, "\n")
	start, end := 0, len(raw)
	for start < end && strings.TrimRight(raw[start], "\r") == "" {
		start++
	}
	for end > start && strings.TrimRight(raw[end-1], "\r") == "" {
		end--
	}
	lines := make([]string, 0, end-start)
	for _, l := range raw[start:end] {
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	return lines
}

// parseHeader parses "## [N]{col1,col2,...}" into the row count and columns.
func parseHeader(line string) (int, []string, error) {
	const prefix = "## ["
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "}") {
		return 0, nil, fmt.Errorf("gcf: decode: malformed header %q", line)
	}
	rest := line[len(prefix):]
	idx := strings.Index(rest, "]{")
	if idx < 0 {
		return 0, nil, fmt.Errorf("gcf: decode: malformed header %q", line)
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:idx]))
	if err != nil {
		return 0, nil, fmt.Errorf("gcf: decode: bad row count in %q: %w", line, err)
	}
	if n < 0 {
		return 0, nil, fmt.Errorf("gcf: decode: negative row count %d", n)
	}
	namesPart := rest[idx+2 : len(rest)-1]
	if strings.TrimSpace(namesPart) == "" {
		if n != 0 {
			return 0, nil, fmt.Errorf("gcf: decode: header declares %d rows but no columns", n)
		}
		return 0, nil, nil
	}
	rawNames := splitCells(namesPart, ',')
	cols := make([]string, len(rawNames))
	for i, name := range rawNames {
		if strings.HasPrefix(name, "\"") {
			unquoted, err := unquoteString(name)
			if err != nil {
				return 0, nil, fmt.Errorf("gcf: decode: header column %d: %w", i, err)
			}
			cols[i] = unquoted
			continue
		}
		cols[i] = name
	}
	return n, cols, nil
}

// parseRow decodes one row line into a map, cell by cell.
func parseRow(idx int, line string, cols []string) (map[string]any, error) {
	cells := splitCells(line, '|')
	if len(cells) != len(cols) {
		return nil, fmt.Errorf("gcf: decode row %d: got %d cells, want %d", idx, len(cells), len(cols))
	}
	row := make(map[string]any, len(cells))
	for j, cell := range cells {
		if cell == "~" {
			// Absent key: omit entirely so decode(encode(x)) is deep-equal.
			continue
		}
		v, err := parseCell(idx, cols[j], cell)
		if err != nil {
			return nil, err
		}
		if err := setPath(idx, row, cols[j], v); err != nil {
			return nil, err
		}
	}
	return row, nil
}

// parseCell converts one non-absent cell into a Go value.
func parseCell(idx int, col, cell string) (any, error) {
	if strings.HasPrefix(cell, "\"") {
		s, err := unquoteString(cell)
		if err != nil {
			return nil, fmt.Errorf("gcf: decode row %d column %q: %w", idx, col, err)
		}
		return s, nil
	}
	if cell == "-" {
		return nil, nil
	}
	if strings.HasPrefix(cell, ".[") {
		return parseArrayCell(idx, col, cell)
	}
	if strings.HasPrefix(cell, "^") {
		return parseAttachCell(idx, col, cell)
	}
	return parseScalar(cell), nil
}

// parseArrayCell parses ".[N]:e1,e2,..." validating the declared element
// count against what the delimiter split actually produced.
func parseArrayCell(idx int, col, cell string) (any, error) {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("gcf: decode row %d column %q: cell %q: %s", idx, col, cell, fmt.Sprintf(format, args...))
	}
	sep := strings.Index(cell, "]:")
	if sep < 0 {
		return nil, fail("missing ]: separator")
	}
	want, err := strconv.Atoi(cell[2:sep])
	if err != nil {
		return nil, fmt.Errorf("gcf: decode row %d column %q: cell %q: bad array length: %w", idx, col, cell, err)
	}
	rest := cell[sep+2:]
	var raw []string
	if rest == "" {
		raw = []string{}
	} else {
		raw = splitCells(rest, ',')
	}
	if len(raw) != want {
		return nil, fail("declares %d elements, found %d", want, len(raw))
	}
	elems := make([]any, len(raw))
	for i, e := range raw {
		if e == "-" {
			// Null sentinel applies inside arrays too: formatScalar(nil)
			// renders "-", so decode must read it back as nil.
			elems[i] = nil
			continue
		}
		if strings.HasPrefix(e, "\"") {
			s, err := unquoteString(e)
			if err != nil {
				return nil, fmt.Errorf("gcf: decode row %d column %q: cell %q: element %d: %w", idx, col, cell, i, err)
			}
			elems[i] = s
			continue
		}
		elems[i] = parseScalar(e)
	}
	return elems, nil
}

// parseAttachCell parses "^<compact json>" back into the original object or
// array-of-objects. The v1 decoder with UseNumber is required here for the
// same reason CompactTabular uses it to parse input: encoding/json/v2
// collapses every number into float64, which silently rounds integers above
// 2^53 and would make attachment round-trips lossy.
func parseAttachCell(idx int, col, cell string) (any, error) {
	payload := cell[1:]
	if payload == "" {
		return nil, fmt.Errorf("gcf: decode row %d column %q: empty attachment", idx, col)
	}
	dec := jsonv1.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("gcf: decode row %d column %q: parse attachment: %w", idx, col, err)
	}
	// io.EOF is the clean end; any other outcome (a second token, or a
	// syntax error) means the attachment carried more than one JSON value.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("gcf: decode row %d column %q: trailing data in attachment %q", idx, col, payload)
	}
	return v, nil
}

// setPath stores v at a ">-joined" column path, creating intermediate maps.
// A non-map value already sitting on the path means the block mixes a scalar
// and a nested object under one name in the same row — impossible from
// Encode, so it is treated as corruption rather than overwritten.
func setPath(idx int, row map[string]any, path string, v any) error {
	parts := strings.Split(path, ">")
	cur := row
	for _, p := range parts[:len(parts)-1] {
		existing, ok := cur[p]
		if !ok {
			m := make(map[string]any)
			cur[p] = m
			cur = m
			continue
		}
		m, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("gcf: decode row %d: path %q: value at %q is not an object", idx, path, p)
		}
		cur = m
	}
	cur[parts[len(parts)-1]] = v
	return nil
}
