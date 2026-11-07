// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

// Master-key rotation for the credential vault (F-5). Split from vault.go so
// the sealing write path and the rotation pass stay under the file-size
// target; both hang off the same sealed column pairs (slotColumns).

import (
	"errors"
	"fmt"

	"9routerx/proxy/internal/vault"
)

// errSealedSlotMoved reports that the sealed pair changed between the read and
// the compare-and-swap write — another writer (a token refresh, a re-seal)
// rotated the slot mid-rewrap, so the caller must retry against the fresh pair
// rather than overwrite it.
var errSealedSlotMoved = errors.New("sealed slot changed during rewrap")

// rewrapSlotAttempts bounds the compare-and-swap retries. The slot only moves
// when a concurrent credential writer lands in the window between the read and
// the write, which is rare; four tries leaves a wide margin before the slot is
// reported as a failure (leaving it untouched, which is the safe outcome).
const rewrapSlotAttempts = 4

// RewrapConnectionDEKs re-wraps every stored DEK under newMasterKey, leaving
// ciphertext untouched. Rows whose DEK cannot be unwrapped (wrong current key,
// tampered) are skipped and named in failures, never rewritten: a rotation
// must not destroy the rows it cannot read.
//
// Each slot is written by compare-and-swap: the fresh pair is read, rewrapped,
// and stored back only when the stored pair is still the one that was read. The
// earlier version rewrapped a wrapped DEK snapshotted at the start of the pass
// and then paired it with whatever ciphertext the row held at write time, so a
// token refresh that rotated the slot in between would have stored a wrapped
// DEK that no longer unwraps the stored ciphertext — bricking the credential
// the rotation was meant to protect. A lost race here retries against the new
// pair instead of clobbering it.
func (r *Repo) RewrapConnectionDEKs(v *vault.Vault, newMasterKey []byte) (rewrapped int, failures []string, err error) {
	if v == nil || !v.Enabled() {
		return 0, nil, vault.ErrDisabled
	}
	rows, err := r.db.Query(`SELECT id, ` + sealedColumns + ` FROM providerConnections
		WHERE secretWrappedDEK <> '' OR tokenWrappedDEK <> '' OR refreshWrappedDEK <> ''`)
	if err != nil {
		return 0, nil, fmt.Errorf("rewrap connection DEKs: %w", err)
	}
	type target struct{ id, slot string }
	var targets []target
	for rows.Next() {
		var (
			id                             string
			secretDEK, secretCT, tokenDEK  string
			tokenCT, refreshDEK, refreshCT string
		)
		if err = rows.Scan(&id, &secretDEK, &secretCT, &tokenDEK, &tokenCT, &refreshDEK, &refreshCT); err != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("rewrap connection DEKs: scan: %w", err)
		}
		for i, wrapped := range []string{secretDEK, tokenDEK, refreshDEK} {
			if wrapped != "" {
				targets = append(targets, target{id: id, slot: SealedSlots[i]})
			}
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, nil, fmt.Errorf("rewrap connection DEKs: %w", err)
	}
	rows.Close()

	for _, t := range targets {
		rewrappedSlot, failed := r.rewrapConnectionSlot(v, newMasterKey, t.id, t.slot)
		if rewrappedSlot {
			rewrapped++
		}
		if failed {
			failures = append(failures, t.id+"/"+t.slot)
		}
	}
	return rewrapped, failures, nil
}

// rewrapConnectionSlot rewraps one slot under compare-and-swap and reports
// whether it landed (first bool) and whether it must be named a failure
// (second bool). A lost race is neither: the loop retries against the pair the
// racing writer stored, up to rewrapSlotAttempts, so a busy credential writer
// only delays the rotation instead of failing it.
func (r *Repo) rewrapConnectionSlot(v *vault.Vault, newMasterKey []byte, id, slot string) (bool, bool) {
	for attempt := 0; attempt < rewrapSlotAttempts; attempt++ {
		wrapped, ciphertext, present, getErr := r.GetConnectionSealed(id, slot)
		if getErr != nil {
			return false, true
		}
		// The slot was cleared while this pass ran: there is nothing left to
		// rewrap, and writing a pair would re-seal a deliberately emptied row.
		if !present {
			return false, true
		}
		newWrapped, rewrapErr := v.Rewrap(wrapped, newMasterKey)
		if rewrapErr != nil {
			// A DEK that cannot be unwrapped under the current master key is
			// left exactly as it is: rewriting it would destroy the only copy
			// of a credential this pass could not read anyway.
			return false, true
		}
		if setErr := r.setConnectionSealedIfUnchanged(id, slot, wrapped, ciphertext, newWrapped); setErr != nil {
			if errors.Is(setErr, errSealedSlotMoved) {
				continue // the pair moved mid-rewrap: retry against the fresh one
			}
			return false, true
		}
		return true, false
	}
	// Every attempt lost the race. The slot is untouched and still opens under
	// the current master key, so it is a skipped rotation, not a lost
	// credential — but the operator must know it was not rewrapped.
	return false, true
}

// setConnectionSealedIfUnchanged stores a rewrapped DEK only when the stored
// (wrappedDEK, ciphertext) pair is still the one the caller read, so a writer
// that rotated the slot in between can never be clobbered by the rewrap.
// errSealedSlotMoved names the lost race; any other error is a real write
// failure.
func (r *Repo) setConnectionSealedIfUnchanged(id, field, wantWrapped, wantCiphertext, newWrapped string) error {
	cols, known := slotColumns[field]
	if !known {
		return fmt.Errorf("set connection sealed %s: unknown slot %q", id, field)
	}
	res, err := r.db.Exec(
		`UPDATE providerConnections SET `+cols[0]+` = ?, `+cols[1]+` = ?, updatedAt = ?
		  WHERE id = ? AND `+cols[0]+` = ? AND `+cols[1]+` = ?`,
		newWrapped, wantCiphertext, nowUTC(), id, wantWrapped, wantCiphertext)
	if err != nil {
		return fmt.Errorf("set connection sealed %s/%s: %w", id, field, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errSealedSlotMoved
	}
	return nil
}
