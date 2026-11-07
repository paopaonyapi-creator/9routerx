// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package tokensaver

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
// (9007199254740993 → 9007199254740992): snowflake IDs riding in tool_use
// input objects or message content come back corrupted after CompressMessages
// or InjectKeySystemPrompt rewrites the body. Same decision as
// internal/compression/gcf parseJSONArray and the translator's
// unmarshalNumbered; the two copies exist because this package cannot import
// the translator and the option set must not live behind an exported seam
// either package would have to stabilize.
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
