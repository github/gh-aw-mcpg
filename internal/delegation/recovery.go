package delegation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logDelegationRecovery = logger.ForFile()

// statePersistVersion pins the on-disk state file shape so a future
// incompatible change is detected as corruption (fail closed) rather than
// silently misparsed.
//
// v3 added Identity.RequestedTTL. A v2 file predates that field, so every
// restored identity would come back with RequestedTTL == 0 and compare equal
// to a retry that actually requested a different TTL — silently weakening the
// terminal-mismatch check bindingEquals is there to enforce. Rather than
// guess, a v2 file is now rejected like any other unsupported version and the
// store fails closed until the operator reconciles.
const statePersistVersion = 3

type persistedState struct {
	Version             int                 `json:"version"`
	Generation          uint64              `json:"generation"`
	RecoveryIncomplete  bool                `json:"recovery_incomplete"`
	Identities          map[string]Identity `json:"identities"`
	DynamicSchemaHashes []string            `json:"dynamic_schema_hashes,omitempty"`
}

// MarkReconciledAndSaveState publishes the reconciled state to path and only
// then opens the in-memory admission gate, so reconciliation is atomic from
// the caller's perspective.
//
// Ordering matters: the durable write happens first and the in-memory
// recoveryIncomplete flag is cleared afterwards, while s.persistMu is still
// held. A persistence failure therefore returns with the gate untouched — it
// never has to be "restored", and there is no window in which a concurrent
// CreateOrConfirm can admit a new identity against state that was never
// persisted. The reverse order (clear, then save, then restore on error)
// leaves exactly that window open, because the store lock is released while
// the file is written.
//
// If the process dies between the successful write and the flag flip, the
// on-disk state already records the reconciliation, so the next LoadStore
// comes back reconciled. That is the intended outcome: the operator did
// complete reconciliation.
func (s *Store) MarkReconciledAndSaveState(path string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	if err := s.saveStateLocked(path, true); err != nil {
		return err
	}

	s.mu.Lock()
	s.recoveryIncomplete = false
	s.mu.Unlock()
	return nil
}

// SaveState persists every currently live identity to path so the controller
// can reconstruct labelled live delegations after a restart. The file is
// written with 0600 permissions because it contains executor bearer secrets.
// A trailing SHA-256 checksum lets LoadStore detect truncation or corruption
// and fail closed instead of silently reconstructing partial state.
//
// SaveState serializes concurrent callers on s.persistMu so that whichever
// snapshot is taken later (in lock-acquisition order) is always the one
// published last: an older, already-superseded snapshot can never overwrite
// a newer one on disk merely because its write happened to finish first.
func (s *Store) SaveState(path string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	return s.saveStateLocked(path, false)
}

// saveStateLocked writes a snapshot of the store to path. The caller must hold
// s.persistMu.
//
// When forceReconciled is set the snapshot records the store as reconciled
// even though the in-memory flag is still set. That is what lets
// MarkReconciledAndSaveState make the durable write first and open the
// admission gate only after it succeeds.
func (s *Store) saveStateLocked(path string, forceReconciled bool) error {
	s.mu.Lock()
	generation := s.generation
	recoveryIncomplete := s.recoveryIncomplete && !forceReconciled
	identities := make(map[string]Identity, len(s.byInvocation))
	for key, identity := range s.byInvocation {
		identities[key] = *identity
	}
	dynamicSchemaHashes := make([]string, 0, len(s.dynamicSchemaHashes))
	for hash := range s.dynamicSchemaHashes {
		dynamicSchemaHashes = append(dynamicSchemaHashes, hash)
	}
	s.mu.Unlock()
	slices.Sort(dynamicSchemaHashes)

	body, err := json.Marshal(persistedState{
		Version:             statePersistVersion,
		Generation:          generation,
		RecoveryIncomplete:  recoveryIncomplete,
		Identities:          identities,
		DynamicSchemaHashes: dynamicSchemaHashes,
	})
	if err != nil {
		return fmt.Errorf("failed to encode delegation state: %w", err)
	}
	checksum := sha256.Sum256(body)
	out := append(body, []byte("\n"+hex.EncodeToString(checksum[:])+"\n")...)
	temp, err := os.CreateTemp(filepath.Dir(path), ".delegation-state-*")
	if err != nil {
		return fmt.Errorf("failed to create delegation state file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("failed to secure delegation state file: %w", err)
	}
	if _, err := temp.Write(out); err != nil {
		temp.Close()
		return fmt.Errorf("failed to write delegation state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("failed to sync delegation state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("failed to close delegation state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("failed to publish delegation state: %w", err)
	}
	logDelegationRecovery.Printf("Persisted delegation state: identities=%d generation=%d", len(identities), generation)
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
		return failedRecoveryStore(envelope, generation)
	}

	state, ok := parsePersistedState(raw)
	if !ok {
		logDelegationRecovery.Print("Delegation state file failed integrity verification, failing closed")
		return failedRecoveryStore(envelope, generation)
	}

	// Validate the entire persisted state before a single index entry is
	// created. Recovery either reconstructs the whole file or reconstructs
	// nothing; there is no partial outcome.
	validated, err := validatePersistedState(state, envelope, generation)
	if err != nil {
		logDelegationRecovery.Printf("Delegation state failed recovery validation, failing closed: %v", err)
		return failedRecoveryStore(envelope, generation)
	}

	store.recoveryIncomplete = state.RecoveryIncomplete
	for _, hash := range state.DynamicSchemaHashes {
		store.dynamicSchemaHashes[hash] = struct{}{}
	}

	restored := 0
	for i := range validated {
		id := validated[i]
		store.indexLocked(&id)
		if id.Revoked || !now.Before(id.ExpiresAt) {
			// revokeLocked removes id from every index it was just added
			// to (byHandle, byBearer, and byLabel), leaving only the
			// terminal byInvocation tombstone. Without this, an
			// already-terminal restored identity would remain reachable
			// from byLabel with no corresponding byHandle entry, letting
			// RevokeByLabels dereference a missing handle.
			store.revokeLocked(&id)
			continue
		}
		restored++
	}

	logDelegationRecovery.Printf("Reconstructed delegation state: restored=%d of %d persisted identities", restored, len(state.Identities))
	return store, nil
}

// failedRecoveryStore is the single outcome of every recovery failure: a brand
// new, completely empty store flagged recoveryIncomplete. Returning the
// partially populated store instead would leave already-scanned identities
// live in byHandle/byBearer/byLabel and their dynamic schema hashes installed,
// so a bearer minted before the crash could still authorize and the in-memory
// indexes would diverge from the persisted file that failed to load.
func failedRecoveryStore(envelope *Envelope, generation uint64) (*Store, error) {
	store, err := NewStore(envelope, generation)
	if err != nil {
		return nil, err
	}
	store.recoveryIncomplete = true
	return store, nil
}

// validatePersistedState validates every part of a persisted state file and
// returns the identities to index, in a deterministic order. It never mutates
// the caller's store, so any error leaves the caller free to discard the file
// entirely.
//
// Duplicate detection deliberately ignores liveness. state.Identities is a JSON
// object keyed by idempotency key, but the invocation scope key is recomputed
// from identity fields, so two distinct object keys can collide on the same
// (run, entry, invocation) tuple. Indexing them one at a time and comparing
// only against the current winner made the outcome depend on Go's randomized
// map iteration order: a live identity indexed before its terminal duplicate
// kept an authorized orphan bearer in byBearer while byInvocation recorded the
// duplicate, and the reverse order produced a different store from the same
// bytes. Any duplicate invocation key, handle, or executor bearer is therefore
// treated as corruption.
func validatePersistedState(state persistedState, envelope *Envelope, generation uint64) ([]Identity, error) {
	if state.Version != statePersistVersion {
		return nil, fmt.Errorf("unsupported state version %d (want %d)", state.Version, statePersistVersion)
	}
	if state.Generation != generation {
		return nil, fmt.Errorf("state generation %d does not match active generation %d", state.Generation, generation)
	}
	if err := validatePersistedSchemaHashes(&state, envelope); err != nil {
		return nil, err
	}

	seenKeys := make(map[string]struct{}, len(state.Identities))
	seenHandles := make(map[string]struct{}, len(state.Identities))
	seenBearers := make(map[string]struct{}, len(state.Identities))
	validated := make([]Identity, 0, len(state.Identities))
	for _, identity := range state.Identities {
		id := identity
		if err := validateRestoredIdentity(&id, envelope, generation); err != nil {
			return nil, fmt.Errorf("identity %s failed active-envelope validation: %w", identityLogID(&id), err)
		}
		key := invocationScopeKey(id.RunID, id.EnclaveEntryID, id.InvocationID)
		if _, dup := seenKeys[key]; dup {
			return nil, fmt.Errorf("duplicate invocation key %s", util.HashForLog(key, 16, "inv:"))
		}
		if _, dup := seenHandles[id.Handle]; dup {
			return nil, fmt.Errorf("duplicate identity handle %s", identityLogID(&id))
		}
		if _, dup := seenBearers[id.ExecutorBearer]; dup {
			return nil, fmt.Errorf("duplicate executor bearer for identity %s", identityLogID(&id))
		}
		seenKeys[key] = struct{}{}
		seenHandles[id.Handle] = struct{}{}
		seenBearers[id.ExecutorBearer] = struct{}{}
		validated = append(validated, id)
	}

	// Index in a stable order so the reconstructed store depends only on the
	// file contents, never on map iteration order.
	slices.SortFunc(validated, func(a, b Identity) int {
		return strings.Compare(a.Handle, b.Handle)
	})
	return validated, nil
}

// validatePersistedSchemaHashes rejects a dynamic schema hash set that the
// active envelope would never have admitted. Restoring an oversized or
// static-envelope set would silently widen the runtime schema bound that
// CreateOrConfirm enforces.
func validatePersistedSchemaHashes(state *persistedState, envelope *Envelope) error {
	seen := make(map[string]struct{})
	for _, hash := range state.DynamicSchemaHashes {
		if hash == "" {
			return fmt.Errorf("empty dynamic schema hash")
		}
		seen[hash] = struct{}{}
	}
	if len(envelope.AllowedSchemaHashes) == 0 {
		for _, identity := range state.Identities {
			if identity.SchemaHash != "" {
				seen[identity.SchemaHash] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	if len(envelope.AllowedSchemaHashes) > 0 {
		return fmt.Errorf("%d dynamic schema hashes persisted under a closed-set envelope", len(seen))
	}
	if len(seen) > envelope.MaxDynamicSchemaHashes {
		return fmt.Errorf("persisted dynamic schema hashes %d exceed envelope bound %d", len(seen), envelope.MaxDynamicSchemaHashes)
	}
	return nil
}

// identityLogID renders an identity for recovery diagnostics. Handles are
// invocation-scoped credentials, so only their stable hash is logged.
func identityLogID(identity *Identity) string {
	return util.HashForLog(identity.Handle, 16, "dlg:")
}

// selectorLogID renders a repository or owner selector as a stable hash so
// validation errors stay correlatable without embedding the raw private
// selector. Envelope validation runs at startup, before proxy.New installs
// the process-wide sensitive-logging redaction, so these errors have to be
// safe on their own: they reach stderr and the operator's terminal with no
// sink-level redaction in front of them.
func selectorLogID(selector string) string {
	return util.HashForLog(selector, 16, "sel:")
}

func validateRestoredIdentity(identity *Identity, envelope *Envelope, generation uint64) error {
	if identity.Handle == "" || identity.ExecutorBearer == "" || identity.PolicyGeneration != generation {
		return fmt.Errorf("invalid identity credential or generation")
	}
	if identity.ExpiresAt.After(envelope.ExpiresAt) {
		return fmt.Errorf("identity expiry exceeds envelope expiry")
	}
	if !identity.InvocationExpiresAt.IsZero() && identity.ExpiresAt.After(identity.InvocationExpiresAt) {
		return fmt.Errorf("identity expiry exceeds invocation expiry")
	}
	return (&Store{envelope: envelope}).validateAgainstEnvelope(CreateOrConfirmRequest{
		RunID:                    identity.RunID,
		EnclaveBackend:           identity.EnclaveBackend,
		EnclaveEntryID:           identity.EnclaveEntryID,
		InvocationID:             identity.InvocationID,
		Repository:               identity.Repository,
		ToolPolicy:               identity.ToolPolicy,
		SchemaHash:               identity.SchemaHash,
		AdmittedDefaultBranchSHA: identity.AdmittedDefaultBranchSHA,
		RequestedTTL:             identity.ExpiresAt.Sub(identity.CreatedAt),
		InvocationExpiresAt:      identity.InvocationExpiresAt,
		IdempotencyKey:           identity.IdempotencyKey,
	}, identity.CreatedAt)
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
