// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"9routerx/proxy/internal/vault"
)

// TestRewrapConnectionDEKsSurvivesConcurrentSlotWrite pins the compare-and-
// swap discipline of the master-key rotation: a token refresh that rotates a
// sealed slot while RewrapConnectionDEKs is in flight must never end up paired
// with a wrapped DEK the rewrap snapshotted before the refresh. The old shape
// rewrapped the wrapped DEK it read at the start of the pass and stored it next
// to whatever ciphertext the row held at write time — a pair that does not
// decrypt, bricking the credential the rotation was meant to protect.
//
// The invariant: when the dust settles, the stored pair opens. Whichever
// writer won the last slot, the wrapped DEK matches the ciphertext beside it.
func TestRewrapConnectionDEKsSurvivesConcurrentSlotWrite(t *testing.T) {
	database, cleanup := setupTestDBAt(t, filepath.Join(t.TempDir(), "rewrap.sqlite"))
	defer cleanup()

	masterA := []byte("rewrap-old-master-key-32-bytes!!")
	masterB := []byte("rewrap-new-master-key-32-bytes!!")
	v, err := vault.New(masterA)
	if err != nil {
		t.Fatalf("build vault: %v", err)
	}
	repo := NewRepo(database)
	repo.SetVault(v)

	if err := repo.CreateProviderConnection("conn-1", "deepseek", "cookie", "account", "the-credential"); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	// Rotating writers replace the secret slot the way a background token
	// refresh does: a brand-new wrapped DEK and ciphertext per write. The
	// first successful write is signaled before the rewrap starts: under a
	// fully loaded test runner the writer goroutine can otherwise stay
	// unscheduled until the rewrap has already come and gone, which makes the
	// final pair the create-time one — a legitimate interleaving the assertion
	// below must be allowed to see, but not the interleaving this test exists
	// to exercise.
	const rotations = 64
	stop := make(chan struct{})
	firstWrite := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rotations; i++ {
			select {
			case <-stop:
				return
			default:
			}
			wrapped, ciphertext, sealErr := v.Seal(fmt.Sprintf("rotated-%d", i))
			if sealErr != nil {
				continue
			}
			if setErr := repo.SetConnectionSealed("conn-1", SlotSecret, wrapped, ciphertext); setErr != nil {
				continue
			}
			select {
			case firstWrite <- struct{}{}:
			default:
			}
		}
	}()

	select {
	case <-firstWrite:
	case <-time.After(5 * time.Second):
		t.Fatal("rotation writer never landed a single write")
	}

	_, _, rewrapErr := repo.RewrapConnectionDEKs(v, masterB)
	close(stop)
	wg.Wait()
	if rewrapErr != nil {
		t.Fatalf("RewrapConnectionDEKs: %v", rewrapErr)
	}

	// The rotation leaves the vault on the new master (the handler rotates
	// after the rewrap), so every writer above sealed under the old one —
	// which stays openable through the previous-key slot.
	if err := v.Rotate(masterB); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	wrapped, ciphertext, present, getErr := repo.GetConnectionSealed("conn-1", SlotSecret)
	if getErr != nil {
		t.Fatalf("read final pair: %v", getErr)
	}
	if !present {
		t.Fatalf("final pair missing")
	}
	plain, openErr := v.Open(wrapped, ciphertext)
	if openErr != nil {
		t.Fatalf("final stored pair does not open (bricked credential): %v", openErr)
	}
	if plain != "the-credential" && !isRotatedPlaintext(plain, rotations) && !isWrappedDataJSON(plain) {
		t.Errorf("final pair opened to %q, want the original, one of the rotated values, or the wrapped-data JSON form", plain)
	}
}

// isRotatedPlaintext matches the rotating writers' payloads.
func isRotatedPlaintext(plain string, rotations int) bool {
	for i := 0; i < rotations; i++ {
		if plain == fmt.Sprintf("rotated-%d", i) {
			return true
		}
	}
	return false
}

// isWrappedDataJSON accepts the create-time sealed value: CreateProviderConnection
// seals the secret fields as their JSON object (`{"apiKey":"..."}`), which is what
// the initial pair carries when the rewrap's CAS lands before any rotation.
func isWrappedDataJSON(plain string) bool {
	return strings.HasPrefix(plain, `{"apiKey":`)
}
