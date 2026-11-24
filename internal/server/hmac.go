package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/hmacutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

const (
	// HMACTimestampHeader carries the Unix-second timestamp included in the signed payload.
	HMACTimestampHeader = "X-MCP-Timestamp"
	// HMACNonceHeader carries a unique, per-request random nonce included in the signed payload.
	HMACNonceHeader = "X-MCP-Nonce"
	// HMACSignatureHeader carries the hex-encoded HMAC-SHA256 signature.
	HMACSignatureHeader = "X-MCP-Signature"

	// hmacMaxAgeSecs is the maximum age (in seconds) of an acceptable signed request.
	// Requests with a timestamp older than this are rejected as potentially replayed.
	hmacMaxAgeSecs = 30

	// nonceTTL is how long a seen nonce is remembered.  Must be > hmacMaxAgeSecs so
	// that any nonce from a valid request window is still tracked when it expires.
	nonceTTL = 2 * hmacMaxAgeSecs * time.Second
)

var logHMAC = logger.ForFile()

// nonceCache tracks recently-seen nonces to detect replay attacks.
// Nonces are held for nonceTTL seconds after first use, then evicted.
type nonceCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // nonce → eviction deadline
}

func newNonceCache() *nonceCache {
	logHMAC.Printf("Creating nonce cache: maxAgeSecs=%d, nonceTTL=%s", hmacMaxAgeSecs, nonceTTL)
	return &nonceCache{entries: make(map[string]time.Time)}
}

// evictExpired removes entries whose deadline has passed.
// Caller must hold c.mu.
// Returns the count of evicted entries and the count of remaining entries.
func (c *nonceCache) evictExpired(now time.Time) (evicted, remaining int) {
	for n, deadline := range c.entries {
		if now.After(deadline) {
			delete(c.entries, n)
			evicted++
		}
	}
	return evicted, len(c.entries)
}

// checkAndSet returns true (and records the nonce) when the nonce has not been
// seen before.  Returns false if the nonce is a duplicate.
func (c *nonceCache) checkAndSet(nonce string) bool {
	c.mu.Lock()

	now := time.Now()

	// Evict expired nonces on every call.  The nonce set is bounded by
	// requests within the hmacMaxAgeSecs window, so this stays small.
	evicted, remaining := c.evictExpired(now)

	if _, seen := c.entries[nonce]; seen {
		c.mu.Unlock()
		if evicted > 0 {
			logHMAC.Printf("Nonce cache eviction: evicted=%d, remaining=%d", evicted, remaining)
		}
		return false
	}
	c.entries[nonce] = now.Add(nonceTTL)
	cacheSize := len(c.entries)
	c.mu.Unlock()

	if evicted > 0 {
		logHMAC.Printf("Nonce cache eviction: evicted=%d, remaining=%d", evicted, remaining)
	}
	logHMAC.Printf("Nonce registered: cacheSize=%d", cacheSize)
	return true
}

// seenNonce returns true if the nonce is already in the cache without modifying state.
// Used as a fast-reject path before the expensive body-read + signature check,
// so that obvious replays are rejected without consuming I/O resources.
func (c *nonceCache) seenNonce(nonce string) bool {
	c.mu.Lock()
	now := time.Now()
	// Evict expired entries so the map doesn't grow unboundedly.
	evicted, remaining := c.evictExpired(now)
	_, seen := c.entries[nonce]
	c.mu.Unlock()

	if evicted > 0 {
		logHMAC.Printf("Nonce cache eviction: evicted=%d, remaining=%d", evicted, remaining)
	}
	return seen
}

func hmacMessage(timestamp, nonce, path string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	return timestamp + "\n" + nonce + "\n" + path + "\n" + hex.EncodeToString(bodyHash[:])
}

// rejectHMAC rejects a request that fails HMAC validation, always using
// http.StatusUnauthorized and the "hmac_validation_failed" event category.
// Only the human-readable msg and the detail token vary per call.
func rejectHMAC(w http.ResponseWriter, r *http.Request, msg, detail string) {
	rejectRequest(w, r, http.StatusUnauthorized, "unauthorized", msg,
		"auth", "hmac_validation_failed", detail)
}

// hmacMiddleware validates that each incoming request carries a correctly signed
// HMAC-SHA256 signature.  It enforces:
//
//  1. Required headers: X-MCP-Timestamp, X-MCP-Nonce, X-MCP-Signature
//  2. Freshness: timestamp must be within hmacMaxAgeSecs seconds of server time
//  3. Replay protection: the (nonce) must not have been seen before
//  4. Signature integrity: HMAC-SHA256(secret, canonical_message) must match
//
// The canonical message is:
//
//	"<timestamp>\n<nonce>\n<path>\n<hex(sha256(body))>"
func hmacMiddleware(secret string, next http.HandlerFunc) http.HandlerFunc {
	cache := newNonceCache()
	logHMAC.Printf("HMAC middleware initialised (maxAge=%ds)", hmacMaxAgeSecs)

	return func(w http.ResponseWriter, r *http.Request) {
		timestamp := r.Header.Get(HMACTimestampHeader)
		nonce := r.Header.Get(HMACNonceHeader)
		sig := r.Header.Get(HMACSignatureHeader)

		if timestamp == "" || nonce == "" || sig == "" {
			logHMAC.Printf("HMAC rejected: missing headers, remote=%s path=%s", r.RemoteAddr, r.URL.Path)
			rejectHMAC(w, r, "missing HMAC signature headers", "missing_hmac_headers")
			return
		}

		// Validate timestamp freshness
		tsUnix, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			logHMAC.Printf("HMAC rejected: invalid timestamp %q, remote=%s", timestamp, r.RemoteAddr)
			rejectHMAC(w, r, "invalid HMAC timestamp", "invalid_timestamp")
			return
		}
		age := time.Since(time.Unix(tsUnix, 0))
		if age > hmacMaxAgeSecs*time.Second || age < -(hmacMaxAgeSecs*time.Second) {
			logHMAC.Printf("HMAC rejected: timestamp too old/future age=%v, remote=%s", age, r.RemoteAddr)
			rejectHMAC(w, r, "HMAC timestamp out of acceptable window", "stale_timestamp")
			return
		}

		// Fast-reject obviously replayed nonces before the body-read cost.
		// This is a read-only check; the authoritative write happens after
		// signature verification to avoid poisoning the cache with invalid requests.
		if cache.seenNonce(nonce) {
			logHMAC.Printf("HMAC rejected: replay detected (pre-check) nonce=%s, remote=%s", nonce, r.RemoteAddr)
			rejectHMAC(w, r, "HMAC nonce already used (replay detected)", "replay_detected")
			return
		}

		// Read and restore body for downstream handlers
		body, err := readAndRestoreRequestBody(r)
		if err != nil {
			rejectRequest(w, r, http.StatusBadRequest, "bad_request", "failed to read request body", "auth", "hmac_validation_failed", "body_read_error")
			return
		}

		providedMAC, err := hex.DecodeString(sig)
		if err != nil || !hmacutil.Verify([]byte(secret), hmacMessage(timestamp, nonce, r.URL.Path, body), providedMAC) {
			logHMAC.Printf("HMAC rejected: signature mismatch, remote=%s path=%s", r.RemoteAddr, r.URL.Path)
			rejectHMAC(w, r, "invalid HMAC signature", "signature_mismatch")
			return
		}

		// Signature is valid — now atomically record the nonce to block replays.
		// Only valid requests consume nonce slots, preventing DoS via cache poisoning.
		// If a concurrent request with the same nonce also passed the pre-check above,
		// exactly one of them will win here; the other is correctly rejected.
		if !cache.checkAndSet(nonce) {
			logHMAC.Printf("HMAC rejected: replay detected (post-check) nonce=%s, remote=%s", nonce, r.RemoteAddr)
			rejectHMAC(w, r, "HMAC nonce already used (replay detected)", "replay_detected")
			return
		}

		logHMAC.Printf("HMAC verified: remote=%s path=%s", r.RemoteAddr, r.URL.Path)
		next(w, r)
	}
}

// applyHMACIfConfigured wraps handler with HMAC validation when secret is non-empty.
// If secret is empty the handler is returned unchanged (backward-compatible plain HTTP).
func applyHMACIfConfigured(secret string, handler http.HandlerFunc) http.HandlerFunc {
	return applyIfConfiguredWithLog(
		secret,
		handler,
		hmacMiddleware,
		logHMAC.Print,
		"HMAC secret configured, applying middleware",
		"No HMAC secret configured, skipping middleware",
	)
}
