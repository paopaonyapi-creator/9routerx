// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"encoding/json"
	"fmt"

	"9routerx/proxy/internal/models"
)

// apikeys_perkey_validate.go validates the serenhope per-key governance fields
// (port(serenhope)) before HandleUpdateApiKey writes them. The middleware that
// enforces these fields per request parses them again on the read side, so a
// value the dashboard would refuse never reaches a live key row.

// maxAPIKeySystemPromptLen bounds the per-key system prompt. The prompt rides
// into every request the key sends, so an unbounded one would both bloat the
// row and inflate every upstream payload; 256 KiB leaves room for the largest
// known prompt asset (the anti-slop ultra level is ~130 KB) while capping a
// pasted accident.
const maxAPIKeySystemPromptLen = 256 * 1024

// validateAPIKeyIPWhitelist accepts "" or a list of IP literals and CIDR
// ranges, in either the JSON-array shape the dashboard sends or the
// comma-separated shape a restore from upstream carries. ParseIPWhitelist is
// the same parser the middleware runs per request, so a value accepted here
// cannot fail there.
func validateAPIKeyIPWhitelist(raw string) error {
	if _, err := models.ParseIPWhitelist(raw); err != nil {
		return fmt.Errorf("ipWhitelist: %w", err)
	}
	return nil
}

// validateAPIKeySystemPrompt accepts any prompt up to the size cap. Unlike the
// other fields the prompt is stored verbatim — leading and trailing
// whitespace can be deliberate prompt content, so it is not trimmed.
func validateAPIKeySystemPrompt(prompt string) error {
	if len(prompt) > maxAPIKeySystemPromptLen {
		return fmt.Errorf("systemPrompt: too long (%d bytes, max %d)", len(prompt), maxAPIKeySystemPromptLen)
	}
	return nil
}

// validateAPIKeyPermissions accepts "" or a JSON object of endpoint
// permissions with known family names only. A typo that parsed to "allow
// nothing" would look like a working key until the client hit the one
// endpoint the operator meant to allow, so unknown names are refused here
// rather than ignored by the middleware.
func validateAPIKeyPermissions(raw string) error {
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return fmt.Errorf("permissions: must be a JSON object")
	}
	if _, err := models.ParseKeyPermissions(raw); err != nil {
		return fmt.Errorf("permissions: %w", err)
	}
	return nil
}
