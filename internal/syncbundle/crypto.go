// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// The bundle file's own encryption: AES-256-GCM under ROUTER_MASTER_KEY,
// packed as "sxb1:" + base64(nonce || ciphertext).
//
// This does not go through internal/vault's Seal/Open because those are the
// envelope pair (a DEK wrapped under the master key, both base64 halves) and
// the bundle format is a single self-describing blob an operator can pipe or
// checksum. The primitives are the same and the key handling is shared:
// the key comes from the same env var, decoded by vault.DecodeMasterKey, so
// a key the server would reject at boot is rejected here too.
//
// Fail closed: without a master key Encrypt refuses rather than emitting a
// plaintext secret file under an encrypted-looking name. Import is the only
// place plaintext is accepted, and only when the prefix says so.
package syncbundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"9routerx/proxy/internal/vault"
)

// encryptedPrefix marks a bundle blob as AES-256-GCM ciphertext. ":" is safe
// because the payload is std base64, whose alphabet has no colon.
const encryptedPrefix = "sxb1:"

// ErrNoMasterKey is returned by Encrypt when ROUTER_MASTER_KEY is absent or
// malformed. Callers branch on it to fall back to an explicit, warned-about
// plaintext export instead of a silent one.
var ErrNoMasterKey = errors.New("syncbundle: no usable master key (" + vault.MasterKeyEnv + "); refusing to write an unencrypted secret bundle")

// MasterKeyPresent reports whether a usable ROUTER_MASTER_KEY is configured,
// so the CLI can choose encrypted output up front instead of discovering the
// absence from Encrypt's error.
func MasterKeyPresent() bool {
	_, err := masterKeyFromEnv()
	return err == nil
}

// Encrypt AES-256-GCM seals the bundle JSON under ROUTER_MASTER_KEY. A fresh
// nonce per call makes two Encryptions of the same bundle differ.
func Encrypt(plaintext []byte) ([]byte, error) {
	key, err := masterKeyFromEnv()
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Encrypt: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("syncbundle.Encrypt: read nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	out := make([]byte, 0, len(encryptedPrefix)+base64.StdEncoding.EncodedLen(len(sealed)))
	out = append(out, encryptedPrefix...)
	out = base64.StdEncoding.AppendEncode(out, sealed)
	return out, nil
}

// Decrypt reverses Encrypt. Anything that fails authentication — a tampered
// byte, a different master key — is ErrTampered, never a best-effort parse:
// decrypting garbage into plausible JSON and importing it would silently
// rewrite an install's config.
func Decrypt(blob []byte) ([]byte, error) {
	rest, ok := strings.CutPrefix(string(blob), encryptedPrefix)
	if !ok {
		return nil, errors.New("syncbundle.Decrypt: blob is not an encrypted bundle (missing " + encryptedPrefix + " prefix)")
	}
	key, err := masterKeyFromEnv()
	if err != nil {
		return nil, err
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Decrypt: decode payload: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Decrypt: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("syncbundle.Decrypt: %w", vault.ErrTampered)
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Decrypt: %w", vault.ErrTampered)
	}
	return plain, nil
}

// masterKeyFromEnv decodes ROUTER_MASTER_KEY the same way the vault does, so
// the two features accept exactly the same key material.
func masterKeyFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(vault.MasterKeyEnv))
	if raw == "" {
		return nil, ErrNoMasterKey
	}
	key, err := vault.DecodeMasterKey(raw)
	if err != nil {
		return nil, errors.Join(ErrNoMasterKey, err)
	}
	return key, nil
}

// newGCM is the one AES-GCM constructor this package needs. It mirrors
// internal/vault's construction so a decryption failure names the same
// primitive regardless of which package reported it.
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
