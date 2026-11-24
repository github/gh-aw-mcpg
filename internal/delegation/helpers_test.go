package delegation

// markReconciled clears the flag in memory for tests. Production callers must
// use MarkReconciledAndSaveState to durably record reconciliation.
func (s *Store) markReconciled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recoveryIncomplete = false
}
