// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// unmarshalNumbered decodes exactly one JSON value into v with number
// literals preserved as json.Number instead of float64.
//
// encoding/json/v2 decodes an interface-typed number as float64, so a plain
// Unmarshal-then-marshal round trip silently rounds every integer above 2^53
// (9007199254740993 → 9007199254740992): snowflake IDs and timestamps riding
// in tool call arguments, Claude tool_use input objects, or schema literals
// come back corrupted. The repair/degrade/squeeze passes re-marshal request
// bodies, so they decode through here. Same decision as
// internal/compression/gcf parseJSONArray; internal/fastjson documents why the
// option set lives in one seam per hot path.
func unmarshalNumbered(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Mirror json.Unmarshal's trailing-data rejection: anything after the
	// first value that is not whitespace is malformed, not ignorable.
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing data after JSON value")
	}
	return nil
}
