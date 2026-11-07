// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package fastjson is a thin seam over encoding/json/v2 shared by the proxy
// hot paths: chat request/response translation, upstream payload repair, and
// the semantic cache key builder.
//
// It exists so callers depend on one place for the option set those paths need
// instead of repeating it per call site. Two properties are non-negotiable:
//
//   - Deterministic output. Prompt-prefix caches upstream (DeepSeek, Anthropic)
//     key on the exact request bytes, so serializing the same value twice must
//     produce the same bytes. encoding/json/v2 leaves map members in Go's
//     randomized iteration order unless asked to sort them, so the option is
//     set explicitly here rather than left to each call site's discretion.
//     (The backend this replaced, sonic's ConfigDefault, left them unsorted
//     too — #186 wanted sorting and never got it, which is why marshalStable
//     and the fingerprint/dedupe paths carry their own Deterministic option.)
//   - Identical error surface. Errors are wrapped by the caller, so the
//     underlying encoding/json/v2 error type is what propagates; there is no
//     translation layer.
package fastjson

import (
	"bytes"
	"errors"
	"io"

	json "encoding/json/v2"
	jsonv1 "encoding/json"
)

// Deterministic marshaling: map members are sorted by name.
var marshalOpts = json.Deterministic(true)

// Marshal serializes v to JSON, sorting map members by name.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v, marshalOpts)
}

// Unmarshal deserializes data into v.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// UnmarshalRead deserializes a single JSON value read from r into v.
func UnmarshalRead(r io.Reader, v any) error {
	return json.UnmarshalRead(r, v)
}

// UnmarshalNumbered deserializes data into v preserving numeric literals:
// every number arrives as json.Number instead of the float64 collapse
// encoding/json/v2 applies when the destination is `any`. Without it, an
// integer above 2^53 riding through a decode→mutate→re-marshal round trip
// (tool-argument repair, response guard) silently changes value — a snowflake
// ID 1234567890123456789 comes back ...800. Marshal writes json.Number back
// raw, so the round trip is byte-faithful for the numbers the input carried.
// Trailing non-whitespace data after the value is rejected, matching the
// strictness callers need when a document is extracted from a larger stream.
func UnmarshalNumbered(data []byte, v any) error {
	dec := jsonv1.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// json.Decoder.Decode reads the first value; trailing garbage after it
	// (a second document, stray bytes) must not silently pass.
	if dec.More() {
		return errTrailingData
	}
	return nil
}

// errTrailingData reports trailing content after the first JSON value.
var errTrailingData = errors.New("trailing data after JSON value")
