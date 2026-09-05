package delegation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logDelegationRecovery = logger.ForFile()

// statePersistVersion pins the on-disk state file shape so a future
// incompatible change is detected as corruption (fail closed) rather than
// silently misparsed.
const statePersistVersion = 1

type persistedState struct {
	Version    int                 `json:"version"`
	Generation uint64              `json:"generation"`
	Identities map[string]Identity `json:"identities"`
}

// SaveState persists every currently live identity to path so the controller
// can reconstruct labelled live delegations after a restart. The file is
// written with 0600 permissions because it contains executor bearer secrets.
// A trailing SHA-256 checksum lets LoadStore detect truncation or corruption
// and fail closed instead of silently reconstructing partial state.
func (s *Store) SaveState(path string) error {
	snapshot := s.Snapshot()
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()

	body, err := json.Marshal(persistedState{
		Version:    statePersistVersion,
		Generation: generation,
		Identities: snapshot,
	})
	if err != nil {
		return fmt.Errorf("failed to encode delegation state: %w", err)
	}
	checksum := sha256.Sum256(body)
	out := append(body, []byte("\n"+hex.EncodeToString(checksum[:])+"\n")...)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("failed to write delegation state: %w", err)
	}
	logDelegationRecovery.Printf("Persisted delegation state: identities=%d generation=%d", len(snapshot), generation)
	return nil
}

// LoadStore reconstructs a Store from a prior SaveState file. If path does
// not exist, this is a fresh start (no prior state to reconcile) and an
// empty, fully-reconciled Store is returned. If path exists but is corrupt,
// truncated, or fails checksum verification, an empty Store is returned with
// recoveryIncomplete set: callers must fail closed for new dynamic
// admissions until MarkReconciled is called after an operator confirms
// outstanding identities are safe to disregard.
//
// Identities that already expired at load time are dropped silently: their
// absence is ordinary lifecycle behavior, not incomplete reconstruction.
func LoadStore(path string, envelope *Envelope, generation uint64) (*Store, error) {
	return loadStoreAt(path, envelope, generation, time.Now())
}

func loadStoreAt(path string, envelope *Envelope, generation uint64, now time.Time) (*Store, error) {
	store, err := NewStore(envelope, generation)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		logDelegationRecovery.Print("No prior delegation state file found; starting with zero live identities")
		return store, nil
	}
	if err != nil {
		logDelegationRecovery.Printf("Failed to read delegation state file, failing closed: %v", err)
		store.recoveryIncomplete = true
		return store, nil
	}

	state, ok := parsePersistedState(raw)
	if !ok {
		logDelegationRecovery.Print("Delegation state file failed integrity verification, failing closed")
		store.recoveryIncomplete = true
		return store, nil
	}
	if state.Version != statePersistVersion {
		logDelegationRecovery.Printf("Delegation state file has unsupported version %d, failing closed", state.Version)
		store.recoveryIncomplete = true
		return store, nil
	}

	restored := 0
	for _, identity := range state.Identities {
		id := identity
		if !now.Before(id.ExpiresAt) || id.Revoked {
			continue
		}
		store.indexLocked(&id)
		restored++
	}
	logDelegationRecovery.Printf("Reconstructed delegation state: restored=%d of %d persisted identities", restored, len(state.Identities))
	return store, nil
}

// parsePersistedState verifies the trailing checksum and decodes the JSON
// body. It returns ok=false for any structural or integrity problem.
func parsePersistedState(raw []byte) (persistedState, bool) {
	const checksumHexLen = 64
	// Expect "<json>\n<64-hex-checksum>\n".
	if len(raw) < checksumHexLen+2 || raw[len(raw)-1] != '\n' {
		return persistedState{}, false
	}
	trimmed := raw[:len(raw)-1]
	sep := len(trimmed) - checksumHexLen
	if sep <= 0 || trimmed[sep-1] != '\n' {
		return persistedState{}, false
	}
	body := trimmed[:sep-1]
	checksumHex := string(trimmed[sep:])
	want, err := hex.DecodeString(checksumHex)
	if err != nil || len(want) != sha256.Size {
		return persistedState{}, false
	}
	got := sha256.Sum256(body)
	if !bytes.Equal(got[:], want) {
		return persistedState{}, false
	}
	var state persistedState
	if err := json.Unmarshal(body, &state); err != nil {
		return persistedState{}, false
	}
	return state, true
}
