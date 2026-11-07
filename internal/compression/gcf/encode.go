// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package gcf implements generative context folding: a lossless tabular
// compaction for JSON arrays of objects, ported from the OmniRoute GCF
// feature. Repeated keys collapse into one header line and every row becomes
// a pipe-delimited line, which removes the per-row key repetition that
// dominates LLM context tokens on tabular payloads.
//
// Wire format (generic profile):
//
//	GCF profile=generic
//	## [N]{col1,col2,...}
//	v1|v2|v3
//
// Cell sentinels: ~ absent key, - null, ^ compact-JSON attachment,
// .[N]: comma-separated primitive array. Nested objects flatten to path
// columns joined by ">". The block is still valid after the transformation —
// Decode restores the original rows byte-for-byte in value.
package gcf

import (
	"fmt"
	"sort"
	"strings"

	jsonv1 "encoding/json"
	json "encoding/json/v2"
)

// profileLine identifies the wire profile; Decode rejects anything else so a
// future incompatible profile cannot be silently parsed by this decoder.
const profileLine = "GCF profile=generic"

// cellKind enumerates the three cell shapes a flattened value can take.
type cellKind int

const (
	cellScalar cellKind = iota // primitive or null, rendered via formatScalar
	cellArray                  // primitive slice, rendered as .[N]:e1,e2
	cellAttach                 // object or array-of-objects, rendered as ^<json>
)

// flatCell is one resolved value of a row keyed by its (possibly flattened)
// column path.
type flatCell struct {
	kind cellKind
	val  any // scalar any | []any (primitives) | attachment value
}

// unsafeObjectKeys are never descended into when flattening. Flattening a
// "__proto__"/"constructor"/"prototype" key into path columns would give the
// decoded shape the same naming as JavaScript prototype chains (prototype
// pollution), so such values bail to attachment encoding instead.
func unsafeObjectKey(k string) bool {
	switch k {
	case "__proto__", "constructor", "prototype":
		return true
	}
	return false
}

// Encode renders rows as a complete GCF generic-profile block: profile line,
// header with row count and columns, then one line per row.
//
// Column discovery is the union of all top-level and flattened nested keys.
// Keys are visited in sorted order per row and first-seen across rows: Go map
// ranges are randomized, so scanning a range directly would make the same
// input produce a different column order on every run.
func Encode(rows []map[string]any) (string, error) {
	flats := make([]map[string]flatCell, len(rows))
	for i, row := range rows {
		flat, err := flattenRow(row)
		if err != nil {
			return "", fmt.Errorf("gcf: encode row %d: %w", i, err)
		}
		flats[i] = flat
	}
	cols := discoverColumns(flats)

	var b strings.Builder
	b.WriteString(profileLine)
	b.WriteString("\n## [")
	b.WriteString(fmt.Sprintf("%d", len(rows)))
	b.WriteString("]{")
	for i, c := range cols {
		if i > 0 {
			b.WriteByte(',')
		}
		// Column names travel with the same quoting rules as string cells:
		// a key containing a delimiter (e.g. "a,b") must not split the
		// header column list.
		b.WriteString(formatScalar(c))
	}
	b.WriteString("}\n")

	for i, flat := range flats {
		if i > 0 {
			b.WriteByte('\n')
		}
		cells := make([]string, len(cols))
		for j, col := range cols {
			cell, ok := flat[col]
			if !ok {
				// The row has no value on this column (missing key, or a
				// sibling shape occupies the bare key / path).
				cells[j] = "~"
				continue
			}
			s, err := renderCell(cell)
			if err != nil {
				return "", fmt.Errorf("gcf: encode row %d column %q: %w", i, col, err)
			}
			cells[j] = s
		}
		b.WriteString(strings.Join(cells, "|"))
	}
	return b.String(), nil
}

// flattenRow resolves one row into path-keyed cells. Nested maps flatten into
// "path>subpath" columns; slices of primitives become array cells; arrays of
// objects and anything not JSON-scalar become attachments.
func flattenRow(row map[string]any) (map[string]flatCell, error) {
	flat := make(map[string]flatCell, len(row)*2)
	for _, k := range sortedKeys(row) {
		if unsafeObjectKey(k) {
			// Leaf treatment: no descent, so the value under the unsafe key
			// is preserved as a whole (objects via attachment).
			if err := flattenLeaf(k, row[k], flat); err != nil {
				return nil, err
			}
			continue
		}
		if err := flattenValue(k, row[k], true, flat); err != nil {
			return nil, err
		}
	}
	return flat, nil
}

// flattenValue classifies v at path. allowDescend is false under unsafe keys,
// forcing objects to attachments instead of path columns.
func flattenValue(path string, v any, allowDescend bool, flat map[string]flatCell) error {
	switch x := v.(type) {
	case map[string]any:
		if !allowDescend || len(x) == 0 {
			// Empty maps flatten to nothing and would be lost, so they
			// travel as attachments to keep the round-trip lossless.
			flat[path] = flatCell{kind: cellAttach, val: x}
			return nil
		}
		prefix := path + ">"
		for _, k := range sortedKeys(x) {
			sub := prefix + k
			if unsafeObjectKey(k) {
				if err := flattenLeaf(sub, x[k], flat); err != nil {
					return err
				}
				continue
			}
			if err := flattenValue(sub, x[k], true, flat); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if allPrimitives(x) {
			flat[path] = flatCell{kind: cellArray, val: x}
			return nil
		}
		flat[path] = flatCell{kind: cellAttach, val: x}
		return nil
	default:
		if !isJSONScalar(v) {
			// Unknown Go types are not representable as GCF scalars; the
			// attachment JSON encoder decides whether they survive at all.
			flat[path] = flatCell{kind: cellAttach, val: x}
			return nil
		}
		flat[path] = flatCell{kind: cellScalar, val: x}
		return nil
	}
}

// flattenLeaf stores a value under an unsafe key without descending into it.
func flattenLeaf(path string, v any, flat map[string]flatCell) error {
	return flattenValue(path, v, false, flat)
}

// allPrimitives reports whether every element is a JSON scalar (null counts:
// a null element renders as "-" inside the array).
func allPrimitives(xs []any) bool {
	for _, x := range xs {
		if !isJSONScalar(x) {
			return false
		}
	}
	return true
}

// isJSONScalar reports whether v is directly representable as a GCF scalar
// cell value. Maps, slices and arbitrary Go types are not.
func isJSONScalar(v any) bool {
	switch v.(type) {
	case nil, bool, string, jsonv1.Number,
		int, int8, int16, int32, int64,
		uint, uint32, uint64,
		float32, float64:
		return true
	}
	return false
}

// discoverColumns unions the flattened paths of all rows in first-seen order,
// visiting each row's paths in sorted order for determinism.
func discoverColumns(flats []map[string]flatCell) []string {
	seen := make(map[string]bool)
	cols := make([]string, 0, 16)
	for _, flat := range flats {
		for _, k := range sortedPathKeys(flat) {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	return cols
}

// renderCell turns a flat cell into its wire representation.
func renderCell(c flatCell) (string, error) {
	switch c.kind {
	case cellScalar:
		return formatScalar(c.val), nil
	case cellArray:
		elems := c.val.([]any)
		parts := make([]string, len(elems))
		for i, e := range elems {
			parts[i] = formatScalar(e)
		}
		return ".[" + fmt.Sprintf("%d", len(elems)) + "]:" + strings.Join(parts, ","), nil
	case cellAttach:
		s, err := marshalCompact(c.val)
		if err != nil {
			return "", err
		}
		return "^" + s, nil
	}
	return "", fmt.Errorf("unknown cell kind %d", c.kind)
}

// marshalCompact produces deterministic compact JSON. Determinism matters
// because map iteration is randomized: the same row must always encode to
// the same bytes or the no-regression gate in CompactTabular would flake.
func marshalCompact(v any) (string, error) {
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return "", fmt.Errorf("marshal attachment: %w", err)
	}
	return string(b), nil
}

// sortedKeys returns a map's keys in sorted order.
func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// sortedPathKeys returns a flat-cell map's keys in sorted order.
func sortedPathKeys(m map[string]flatCell) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
