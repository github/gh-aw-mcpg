package delegation

import (
	"fmt"
	"slices"
	"time"
)

// Envelope is the compiler-installed, compiler-bounded policy envelope
// bootstrapped into the controller at gateway startup. Every delegated
// identity must be a strict subset of this envelope; the controller rejects
// any request for a server, tool, repository, credential, backend URL, or
// guard policy outside it. The compiler is not a runtime service: once
// installed the envelope is immutable for the lifetime of the process.
type Envelope struct {
	// RunID is the workflow run this envelope, and every identity minted
	// from it, is bound to.
	RunID string
	// EnclaveBackend is the single AWF enclave backend identities may be
	// bound to.
	EnclaveBackend string
	// AllowedRepositories is the closed set of canonical owner/repo
	// selectors the compiler admitted for this run. Selectors are compared
	// as exact ASCII byte sequences; no normalization is performed.
	AllowedRepositories []string
	// ToolPolicy is the single delegated tool policy this envelope allows.
	// Only ToolPolicyGitHubRepositoryReadV1 is currently supported.
	ToolPolicy string
	// AllowedSchemaHashes is the closed set of finite response schema
	// hashes the compiler approved for this run.
	AllowedSchemaHashes []string
	// MaxIdentityTTL bounds how long any single delegated identity may
	// live, and therefore how long an executor bearer remains valid.
	MaxIdentityTTL time.Duration
	// ExpiresAt is the envelope's own absolute expiry, no later than the
	// workflow job lifetime. No identity may be created once the envelope
	// itself has expired.
	ExpiresAt time.Time
}

// Validate checks the envelope's own invariants. It does not check any
// per-request binding; use Envelope.Admits for that.
func (e *Envelope) Validate() error {
	if e == nil {
		return fmt.Errorf("delegation envelope is required")
	}
	if e.RunID == "" {
		return fmt.Errorf("envelope run id is required")
	}
	if e.EnclaveBackend == "" {
		return fmt.Errorf("envelope enclave backend is required")
	}
	if len(e.AllowedRepositories) == 0 {
		return fmt.Errorf("envelope must admit at least one repository")
	}
	seen := make(map[string]struct{}, len(e.AllowedRepositories))
	for _, repo := range e.AllowedRepositories {
		if !IsCanonicalRepositorySelector(repo) {
			return fmt.Errorf("envelope repository %q is not a canonical selector", repo)
		}
		if _, dup := seen[repo]; dup {
			return fmt.Errorf("envelope must not contain duplicate repository %q", repo)
		}
		seen[repo] = struct{}{}
	}
	if e.ToolPolicy != ToolPolicyGitHubRepositoryReadV1 {
		return fmt.Errorf("unsupported envelope tool policy %q", e.ToolPolicy)
	}
	if len(e.AllowedSchemaHashes) == 0 {
		return fmt.Errorf("envelope must admit at least one schema hash")
	}
	if e.MaxIdentityTTL <= 0 {
		return fmt.Errorf("envelope max identity ttl must be positive")
	}
	if e.ExpiresAt.IsZero() {
		return fmt.Errorf("envelope expiry is required")
	}
	return nil
}

// AllowsRepository reports whether repo is an exact-byte member of the
// envelope's admitted repository set.
func (e *Envelope) AllowsRepository(repo string) bool {
	return slices.Contains(e.AllowedRepositories, repo)
}

// AllowsSchemaHash reports whether schemaHash is an exact-byte member of the
// envelope's admitted schema hash set.
func (e *Envelope) AllowsSchemaHash(schemaHash string) bool {
	return slices.Contains(e.AllowedSchemaHashes, schemaHash)
}
