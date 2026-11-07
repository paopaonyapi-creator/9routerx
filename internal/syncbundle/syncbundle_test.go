// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package syncbundle

import (
	"bytes"
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	json "encoding/json/v2"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/vault"
)

// setupRepo opens a fresh temp-file database the way every db-backed test in
// this repo does, with a vault attached so connections seal exactly as they
// do in a real install.
func setupRepo(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := db.OpenDatabase(filepath.Join(t.TempDir(), "bundle.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	repo := db.NewRepo(conn)
	repo.SetVault(vault.NewFromEnv())
	return repo
}

// setTestMasterKey installs a fixed 32-byte key so the encrypted round trip
// is reproducible and the tamper test can flip a known byte.
func setTestMasterKey(t *testing.T) {
	t.Helper()
	key := make([]byte, vault.KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv(vault.MasterKeyEnv, base64.StdEncoding.EncodeToString(key))
}

// seedRepo writes one row per section, each carrying a value the assertions
// look for after the round trip.
func seedRepo(t *testing.T, repo *db.Repo) {
	t.Helper()
	if err := repo.UpdateSettingsRaw(map[string]any{
		"rtkEnabled":       true,
		"rtkMode":          "balanced",
		"password":         "dashboard-password-hash",
		"oidcClientSecret": "oidc-secret",
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if err := repo.CreateApiKey("k1", "sk-client-secret-value-1234567890", "main key", "mac-1"); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	rpm, tpm, conc := 10, 20, 1
	if err := repo.SetApiKeyRateLimit("k1", rpm, tpm, conc); err != nil {
		t.Fatalf("seed rate limit: %v", err)
	}
	if err := repo.CreateProviderConnection("c1", "openai", "api-key", "Main OpenAI", "sk-provider-secret-value"); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := repo.CreateCombo("cb1", "combo-a", "", `["openai/gpt-4o"]`, "fallback"); err != nil {
		t.Fatalf("seed combo: %v", err)
	}
	if err := repo.CreateTeam(db.Team{ID: "t1", Name: "eng", MaxBudgetUSD: 10, BudgetDuration: db.TeamBudgetDaily}); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if err := repo.SaveWebhookTarget(context.Background(), db.WebhookTarget{
		ID: "w1", Type: "generic", URL: "https://hooks.example/x", Enabled: true, FilterEvents: "*",
	}); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
}

// countRows is the count assertion helper: one section, one number.
func countRows(t *testing.T, repo *db.Repo, table string) int {
	t.Helper()
	var n int
	if err := repo.RawDB().QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestRoundTripExportEncryptDecryptImport(t *testing.T) {
	setTestMasterKey(t)
	src := setupRepo(t)
	seedRepo(t, src)

	plain, err := Export(src)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	blob, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !hasEncryptedPrefix(blob) {
		t.Fatalf("encrypted bundle lost its prefix")
	}
	decrypted, err := Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("decrypt round trip differs from plaintext export")
	}

	dst := setupRepo(t)
	report, err := Import(dst, blob)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.APIKeys.Created != 1 || report.Connections.Created != 1 ||
		report.Combos.Created != 1 || report.Teams.Created != 1 || report.Webhooks.Created != 1 {
		t.Fatalf("unexpected report: %s", report)
	}
	if report.SettingsKeys == 0 {
		t.Fatalf("settings section empty")
	}
	if got := countRows(t, dst, "apiKeys"); got != countRows(t, src, "apiKeys") {
		t.Fatalf("apiKeys count = %d, want %d", got, countRows(t, src, "apiKeys"))
	}
	if got := countRows(t, dst, "combos"); got != countRows(t, src, "combos") {
		t.Fatalf("combos count = %d, want %d", got, countRows(t, src, "combos"))
	}

	// The key metadata survived, not just the row.
	keys, err := dst.GetApiKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("GetApiKeys: %v (%d keys)", err, len(keys))
	}
	if keys[0].Name == nil || *keys[0].Name != "main key" {
		t.Fatalf("key name = %v", keys[0].Name)
	}
	if keys[0].RateLimitRPM == nil || *keys[0].RateLimitRPM != 10 {
		t.Fatalf("rateLimitRPM = %v", keys[0].RateLimitRPM)
	}

	// The provider credential came through the vault: sealed on the way out,
	// re-sealed on the way in.
	conn, err := dst.GetProviderConnectionByID("c1")
	if err != nil || conn == nil {
		t.Fatalf("GetProviderConnectionByID: %v (%v)", err, conn)
	}
	if !strings.Contains(conn.Data, "sk-provider-secret-value") {
		t.Fatalf("imported connection data lost the credential: %s", conn.Data)
	}

	settings, err := dst.GetSettingsRaw()
	if err != nil {
		t.Fatalf("GetSettingsRaw: %v", err)
	}
	if settings["rtkEnabled"] != true || settings["rtkMode"] != "balanced" {
		t.Fatalf("settings did not survive: %v", settings)
	}
}

func TestExportStripsAuthSecretsAndMasksKeys(t *testing.T) {
	setTestMasterKey(t)
	repo := setupRepo(t)
	seedRepo(t, repo)

	plain, err := Export(repo)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	// The bundle is plaintext JSON before encryption; both secrets must be
	// gone by then, or the encrypted form carries them regardless.
	if strings.Contains(string(plain), "dashboard-password-hash") {
		t.Fatalf("dashboard password leaked into the bundle")
	}
	if strings.Contains(string(plain), "oidc-secret") {
		t.Fatalf("oidc client secret leaked into the bundle")
	}
	if strings.Contains(string(plain), "sk-client-secret-value-1234567890") {
		t.Fatalf("client api key value leaked into the bundle")
	}
	if !strings.Contains(string(plain), `"keyMasked":"sk-****7890"`) {
		t.Fatalf("masked key shape missing: %s", plain)
	}
}

func TestImportDoesNotClobberLiveAuthSecrets(t *testing.T) {
	setTestMasterKey(t)
	src := setupRepo(t)
	seedRepo(t, src)
	plain, err := Export(src)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	dst := setupRepo(t)
	// The target has its own dashboard password; the bundle carries none, and
	// the merge must not read that absence as "clear the password".
	if err := dst.UpdateSettingsRaw(map[string]any{"password": "live-password", "autoUpdate": false}); err != nil {
		t.Fatalf("seed target password: %v", err)
	}
	if _, err := Import(dst, plain); err != nil {
		t.Fatalf("Import: %v", err)
	}
	settings, err := dst.GetSettingsRaw()
	if err != nil {
		t.Fatalf("GetSettingsRaw: %v", err)
	}
	if settings["password"] != "live-password" {
		t.Fatalf("import clobbered the live password: %v", settings["password"])
	}
}

func TestMissingMasterKeyFailsClosed(t *testing.T) {
	t.Setenv(vault.MasterKeyEnv, "")
	plain := []byte(`{"version":1,"gateway":"9RouterX"}`)
	if _, err := Encrypt(plain); err == nil {
		t.Fatalf("Encrypt without a master key must fail, not write a plaintext secret file")
	}
	// Export still works: it is a serialization, and the CLI says out loud
	// when it writes the plaintext form.
	repo := setupRepo(t)
	if _, err := Export(repo); err != nil {
		t.Fatalf("Export without a master key should still produce JSON: %v", err)
	}
	if MasterKeyPresent() {
		t.Fatalf("MasterKeyPresent = true with no key set")
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	setTestMasterKey(t)
	plain := []byte(`{"version":1,"gateway":"9RouterX","settings":{"rtkEnabled":true}}`)
	blob, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip one byte deep in the ciphertext body.
	i := len(blob) - 3
	blob[i] ^= 0xFF
	if _, err := Decrypt(blob); err == nil {
		t.Fatalf("Decrypt accepted tampered ciphertext")
	}
}

func TestImportWrongVersionAndGateway(t *testing.T) {
	cases := []struct {
		name   string
		bundle string
	}{
		{"future version", `{"version":99,"gateway":"9RouterX"}`},
		{"foreign gateway", `{"version":1,"gateway":"OtherGateway"}`},
	}
	repo := setupRepo(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Import(repo, []byte(tc.bundle)); err == nil {
				t.Fatalf("Import accepted %q", tc.bundle)
			}
		})
	}
}

func TestPlaintextImportAccepted(t *testing.T) {
	setTestMasterKey(t)
	src := setupRepo(t)
	seedRepo(t, src)
	plain, err := Export(src)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if hasEncryptedPrefix(plain) {
		t.Fatalf("Export must not emit the encrypted prefix itself")
	}
	dst := setupRepo(t)
	report, err := Import(dst, plain)
	if err != nil {
		t.Fatalf("Import of a prefix-less bundle must be accepted: %v", err)
	}
	if report.Combos.Created != 1 || report.APIKeys.Created != 1 {
		t.Fatalf("unexpected report: %s", report)
	}
}

func TestDoubleImportIdempotent(t *testing.T) {
	setTestMasterKey(t)
	src := setupRepo(t)
	seedRepo(t, src)
	plain, err := Export(src)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	dst := setupRepo(t)
	first, err := Import(dst, plain)
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	second, err := Import(dst, plain)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	// Re-import touches nothing new: every section flips from created to
	// updated and the counts stay put.
	if second.APIKeys.Created != 0 || second.Connections.Created != 0 ||
		second.Combos.Created != 0 || second.Teams.Created != 0 || second.Webhooks.Created != 0 {
		t.Fatalf("second import created rows: %s", second)
	}
	if second.APIKeys.Updated != first.APIKeys.Created || second.Combos.Updated != first.Combos.Created {
		t.Fatalf("second import did not mirror the first: first=%s second=%s", first, second)
	}
	for _, table := range []string{"apiKeys", "combos", "providerConnections", "teams", "webhookTargets"} {
		if got := countRows(t, dst, table); got != 1 {
			t.Fatalf("%s count = %d after double import, want 1", table, got)
		}
	}
	settings, err := dst.GetSettingsRaw()
	if err != nil || settings["rtkEnabled"] != true {
		t.Fatalf("settings changed on re-import: %v (%v)", settings, err)
	}
}

func TestCombosCarryMatchRules(t *testing.T) {
	setTestMasterKey(t)
	src := setupRepo(t)
	seedRepo(t, src)
	if err := src.SetComboRules(context.Background(), "cb1", `[{"type":"model","value":"openai/gpt-4o"}]`); err != nil {
		t.Fatalf("SetComboRules: %v", err)
	}

	plain, err := Export(src)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var bundle Bundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(bundle.Combos) != 1 || !strings.Contains(bundle.Combos[0].MatchRules, "gpt-4o") {
		t.Fatalf("match rules did not travel: %+v", bundle.Combos)
	}

	dst := setupRepo(t)
	if _, err := Import(dst, plain); err != nil {
		t.Fatalf("Import: %v", err)
	}
	rules, err := dst.GetComboRules(context.Background(), "cb1")
	if err != nil || !strings.Contains(rules, "gpt-4o") {
		t.Fatalf("combo rules lost on import: %q (%v)", rules, err)
	}
}
