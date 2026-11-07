// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package gcf

import (
	"fmt"
	"strconv"
	"strings"

	jsonv1 "encoding/json"
)

// Scalar wire format for the generic GCF profile.
//
// A cell holds one of:
//   - a formatted scalar (int64 stays an exact integer, floats keep a decimal
//     point so they never collide with integers on the way back),
//   - a quoted string ("...") whenever the raw form would be ambiguous,
//   - the null sentinel "-".
//
// Quoting is driven by losslessness, not just by the spec's delimiter rule: a
// string that would parse back as a number, boolean or sentinel ("123",
// "true", "-x", "^json") must travel quoted, otherwise decode returns a
// different Go type than the caller encoded and the round-trip is lossy.

// formatScalar renders a scalar value for a GCF cell or array element.
// Non-scalar values never reach it from Encode (they are routed to attachment
// cells); fmt.Sprint is a defensive fallback that keeps the function total.
func formatScalar(v any) string {
	switch x := v.(type) {
	case nil:
		// null sentinel; array elements reuse it, cells get it from flatten.
		return "-"
	case string:
		if needsQuote(x) {
			return quoteString(x)
		}
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case jsonv1.Number:
		// jsonv1.Number carries the literal the parser saw, so the exact JSON
		// form (including exponents and trailing zeros) survives the trip.
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case float64:
		return formatFloat(x, 64)
	case float32:
		return formatFloat(float64(x), 32)
	default:
		return fmt.Sprint(x)
	}
}

// formatFloat renders a float so decode can tell it apart from an integer:
// strconv's shortest form for 2.0 is "2", which parseScalar would read back
// as int64 and break the round-trip. Appending ".0" when the shortest form
// has no decimal point or exponent keeps the float identity on the wire.
// bitSize preserves float32's shorter representation.
func formatFloat(f float64, bitSize int) string {
	s := strconv.FormatFloat(f, 'g', -1, bitSize)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// parseScalar converts an unquoted cell back into a Go value. Integers are
// tried first so whole numbers return as int64 (exact round-trip for values
// beyond float64's 2^53); everything else numeric becomes float64, matching
// JSON semantics. Unrecognized text returns as a string.
func parseScalar(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

// isJSONScalarLiteral reports whether s would be re-read as a non-string
// scalar by parseScalar. Both sides share the predicate, so any string it
// flags travels quoted and therefore comes back as a string.
func isJSONScalarLiteral(s string) bool {
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	// ParseFloat also accepts Inf/NaN spellings; quoting those is harmless.
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return s == "true" || s == "false"
}

// needsQuote reports whether a string must travel quoted to survive decode:
// delimiters (| for cells, , for array elements and the header), the cell
// sentinels (~ - ^ = .[), control characters that would break the line-based
// format, leading/trailing spaces, and anything parseScalar would misread.
func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	if strings.ContainsAny(s, "|,\"") {
		return true
	}
	if s != strings.TrimSpace(s) {
		return true
	}
	for _, r := range s {
		if r < 0x20 {
			return true
		}
	}
	if strings.HasPrefix(s, "~") || strings.HasPrefix(s, "-") ||
		strings.HasPrefix(s, "^") || strings.HasPrefix(s, "=") ||
		strings.HasPrefix(s, ".[") {
		return true
	}
	return isJSONScalarLiteral(s)
}

// quoteString renders s as a GCF quoted string. Internal quotes double
// ("" per the spec) rather than backslash-escape; backslash and control
// characters use backslash escapes so the format stays line-oriented.
func quoteString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`""`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// unquoteString reverses quoteString. The outer quotes are structural (the
// cell splitter already used them), so what is left is escape processing.
func unquoteString(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("malformed quoted cell %q", s)
	}
	inner := s[1 : len(s)-1]
	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '"':
			// "" is a doubled quote; a trailing lone quote is malformed.
			if i+1 >= len(inner) || inner[i+1] != '"' {
				return "", fmt.Errorf("malformed quoted cell %q", s)
			}
			b.WriteByte('"')
			i++
		case '\\':
			if i+1 >= len(inner) {
				return "", fmt.Errorf("dangling escape in cell %q", s)
			}
			i++
			switch inner[i] {
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, n := unescapeUnicode(inner[i+1:])
				if n == 0 {
					return "", fmt.Errorf("bad unicode escape in cell %q", s)
				}
				b.WriteRune(r)
				i += n
			default:
				// Tolerant fallback for escapes this encoder never emits.
				b.WriteByte('\\')
				b.WriteByte(inner[i])
			}
		default:
			b.WriteByte(inner[i])
		}
	}
	return b.String(), nil
}

// unescapeUnicode parses exactly four hex digits after \u and returns the
// rune plus the number of hex digits consumed (0 means invalid). Surrogate
// pairs never occur in encoder output (only C0 controls use \u), so a plain
// rune conversion is sufficient.
func unescapeUnicode(s string) (rune, int) {
	if len(s) < 4 {
		return 0, 0
	}
	v, err := strconv.ParseUint(s[:4], 16, 32)
	if err != nil {
		return 0, 0
	}
	return rune(v), 4
}

// splitCells splits a line on delim while honoring double-quoted sections:
// inside quotes the delimiter has no structural meaning and "" is an escaped
// quote, not a boundary. It is shared by rows (|), array cells (,) and the
// header column list (,).
func splitCells(line string, delim byte) []string {
	cells := make([]string, 0, 8)
	start := 0
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case inQuote:
			if c == '"' {
				// Lookahead: "" stays inside the quote, a lone " closes it.
				if i+1 < len(line) && line[i+1] == '"' {
					i++
				} else {
					inQuote = false
				}
			}
		case c == '"':
			inQuote = true
		case c == delim:
			cells = append(cells, line[start:i])
			start = i + 1
		}
	}
	return append(cells, line[start:])
}
