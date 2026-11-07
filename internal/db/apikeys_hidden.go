// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"database/sql"
	"fmt"
)

// apikeys_hidden.go holds the writes and reads behind the FASE 10.3 model
// visibility toggle. The per-key hide list lives in the apiKeys.hiddenModels
// column (a JSON array of model patterns), the global one in the settings
// blob's hiddenModels key. Hidden is a listing/visibility policy, not access
// control: the allowlist (modelaccess.go) decides what a key MAY call, hidden
// decides what it can SEE.

// SetApiKeyHiddenModels stores a key's hide list: a JSON array of model
// patterns, or "" to hide nothing — the value every key written before the
// feature holds, so the zero value keeps the old behaviour. The JSON is
// validated by the dashboard handler before it reaches here.
func (r *Repo) SetApiKeyHiddenModels(id, models string) error {
	_, err := r.db.Exec(`UPDATE apiKeys SET hiddenModels = ? WHERE id = ?`, models, id)
	if err != nil {
		return fmt.Errorf("set api key hidden models %s: %w", id, err)
	}
	return nil
}

// GetApiKeyHiddenModels returns a key's stored hide list, "" when the key has
// none (the column is DEFAULT ”, so only a genuine storage failure errors). A
// key id that names no row also reads as "", which is what the filter wants:
// a key that vanished between authentication and the policy read hides
// nothing rather than failing every listing.
func (r *Repo) GetApiKeyHiddenModels(id string) (string, error) {
	if id == "" {
		return "", nil
	}
	var raw sql.NullString
	err := r.db.QueryRow(`SELECT hiddenModels FROM apiKeys WHERE id = ? LIMIT 1`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get api key hidden models %s: %w", id, err)
	}
	return raw.String, nil
}

// SetHiddenModels stores the GLOBAL hide list in the settings blob. It merges
// under the settings write lock like every other settings writer, so a
// concurrent save of an unrelated key cannot be dropped.
func (r *Repo) SetHiddenModels(models string) error {
	return r.UpdateSettingsRaw(map[string]any{"hiddenModels": models})
}

// GetHiddenModels returns the global hide list, "" when none is stored. It
// reads the blob directly rather than through GetSettings: the caller needs
// one key, and the request path pays this read once per cache generation.
func (r *Repo) GetHiddenModels() (string, error) {
	raw, err := r.GetSettingsRaw()
	if err != nil {
		return "", fmt.Errorf("get hidden models: %w", err)
	}
	if raw == nil {
		return "", nil
	}
	// The blob is hand-editable through the backup import, so a wrongly typed
	// value is a real input; anything that is not a string reads as unset
	// rather than as a parse failure.
	s, _ := raw["hiddenModels"].(string)
	return s, nil
}
