package sanitize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldRedactPayload(t *testing.T) {
	assert := assert.New(t)

	previous := PayloadRedactionEnabled()
	t.Cleanup(func() {
		SetPayloadRedaction(previous)
		SetRawPayloadLogsAllowed(false)
	})

	SetPayloadRedaction(false)
	SetRawPayloadLogsAllowed(false)
	assert.False(ShouldRedactPayload(false), "non-enclave traffic keeps its existing logging behavior")
	assert.True(ShouldRedactPayload(true), "enclave-scoped traffic is redacted by default")

	SetPayloadRedaction(true)
	assert.True(ShouldRedactPayload(false), "process-wide mode redacts every session")

	SetRawPayloadLogsAllowed(true)
	assert.False(ShouldRedactPayload(true), "privileged opt-in restores raw payload logging")
	assert.True(RawPayloadLogsAllowed())
}

func TestRedactedPayloadRenderings(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	payload := []byte(`{"result":{"body":"SENTINEL-PRIVATE"}}`)

	text := RedactedPayloadText(payload)
	assert.NotContains(text, "SENTINEL-PRIVATE")
	assert.Contains(text, "bytes=38")
	assert.Contains(text, "digest=hmac:")

	encoded := RedactedPayloadJSON(payload)
	assert.NotContains(string(encoded), "SENTINEL-PRIVATE")

	var decoded map[string]any
	require.NoError(json.Unmarshal(encoded, &decoded))
	assert.Equal(true, decoded["redacted"])
	assert.Equal("enclave_payload_redacted", decoded["reason"])
	assert.InDelta(float64(len(payload)), decoded["bytes"], 0)

	// The digest is stable so identical payloads stay correlatable across lines.
	firstDigest := PayloadDigest(payload)
	secondDigest := PayloadDigest(payload)
	assert.Equal(firstDigest, secondDigest)
	assert.NotEqual(PayloadDigest(payload), PayloadDigest([]byte(`{"result":{}}`)))

	// The digest is keyed with a per-process secret, so an artifact reader cannot
	// recover a payload drawn from a small candidate set by hashing guesses.
	plain := sha256.Sum256(payload)
	assert.NotContains(text, hex.EncodeToString(plain[:])[:16])
}

func TestKeyedDigest(t *testing.T) {
	t.Run("same value yields same token and different values differ", func(t *testing.T) {
		a := KeyedDigest("owner/repo")
		assert.Equal(t, a, KeyedDigest("owner/repo"))
		assert.NotEqual(t, a, KeyedDigest("owner/other"))
	})

	t.Run("token is prefixed and does not leak the value", func(t *testing.T) {
		got := KeyedDigest("secret-repo-name")
		assert.True(t, strings.HasPrefix(got, digestPrefix))
		assert.NotContains(t, got, "secret-repo-name")
		assert.Len(t, got, len(digestPrefix)+redactedPayloadDigestLen)
	})

	t.Run("fails closed when the digest key is unavailable", func(t *testing.T) {
		payloadDigestKey() // ensure the Once has fired so the override sticks
		original := digestKey
		digestKey = nil
		t.Cleanup(func() { digestKey = original })

		assert.Equal(t, digestPrefix+"unavailable", KeyedDigest("owner/repo"))
		assert.Equal(t, "(none)", KeyedDigest(""), "empty values still render as none")
	})
}

func TestKeyedDigestEmptyValue(t *testing.T) {
	assert.Equal(t, "(none)", KeyedDigest(""), "empty values must not hash to a guessable token")
}

// TestEnablePayloadRedaction pins the process-wide startup hook used by the
// enclave and delegation profiles: it must actually flip the flag that
// ShouldRedactPayload / PayloadRedactionEnabled read.
func TestEnablePayloadRedaction(t *testing.T) {
	previous := PayloadRedactionEnabled()
	previousRaw := RawPayloadLogsAllowed()
	t.Cleanup(func() {
		SetPayloadRedaction(previous)
		SetRawPayloadLogsAllowed(previousRaw)
	})
	SetRawPayloadLogsAllowed(false)
	SetPayloadRedaction(false)
	assert.False(t, PayloadRedactionEnabled())

	EnablePayloadRedaction()
	assert.True(t, PayloadRedactionEnabled(), "EnablePayloadRedaction must turn on process-wide redaction")
}

// TestRawPayloadLogsAllowed_EnvParsing exercises rawPayloadLogsAllowed's
// sync.Once-guarded environment-variable parse branch, which
// SetRawPayloadLogsAllowed bypasses in every other test in this file. Because
// rawPayloadOnce can only run its function once per process, this test
// re-execs itself in subprocesses with EnvRawPayloadLogs set to a valid and
// then an invalid value, verifying both the truthy-parse and parse-error
// paths of the Do body.
func TestRawPayloadLogsAllowed_EnvParsing(t *testing.T) {
	if os.Getenv("GO_WANT_RAW_PAYLOAD_SUBPROCESS") == "1" {
		want := os.Getenv("GO_WANT_RAW_PAYLOAD_EXPECTED") == "true"
		assert.Equal(t, want, rawPayloadLogsAllowed())
		return
	}

	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"valid true value enables raw payload logs", "true", true},
		{"invalid value falls back to disabled", "not-a-bool", false},
		{"unset value falls back to disabled", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestRawPayloadLogsAllowed_EnvParsing", "-test.v")
			cmd.Env = append(os.Environ(),
				"GO_WANT_RAW_PAYLOAD_SUBPROCESS=1",
				"GO_WANT_RAW_PAYLOAD_EXPECTED="+boolString(tt.expected),
				EnvRawPayloadLogs+"="+tt.envValue,
			)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "subprocess output:\n%s", out)
			assert.Contains(t, string(out), "PASS")
		})
	}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestRedactMessageForLog(t *testing.T) {
	assert := assert.New(t)

	msg := "sensitive: repo=private-org/secret-repo"
	assert.Equal(msg, RedactMessageForLog(msg, false))

	got1 := RedactMessageForLog(msg, true)
	got2 := RedactMessageForLog(msg, true)
	assert.NotContains(got1, "private-org/secret-repo")
	assert.Equal(got1, got2, "digest must be deterministic for identical input")
	assert.Equal(KeyedDigest(msg), got1)
}

func TestRedactErrorForLogIf(t *testing.T) {
	assert := assert.New(t)

	err := errors.New("backend returned SENTINEL-PRIVATE")
	assert.Equal("backend returned SENTINEL-PRIVATE", RedactErrorForLogIf(err, false))
	assert.Equal("<nil>", RedactErrorForLogIf(nil, false))
	assert.Equal(RedactErrorForLog(err), RedactErrorForLogIf(err, true))
	assert.NotContains(RedactErrorForLogIf(err, true), "SENTINEL-PRIVATE")
	assert.Equal("timeout", RedactErrorForLogIf(context.DeadlineExceeded, true))
}

func TestRedactErrorForLog(t *testing.T) {
	assert := assert.New(t)

	assert.Empty(RedactErrorForLog(nil))
	assert.Equal("timeout", RedactErrorForLog(context.DeadlineExceeded))
	assert.Equal("canceled", RedactErrorForLog(context.Canceled))

	category := RedactErrorForLog(errors.New("backend returned SENTINEL-PRIVATE"))
	assert.NotContains(category, "SENTINEL-PRIVATE")
	assert.Contains(category, "error hmac:")
}

func TestPayloadTextForLog(t *testing.T) {
	// The payload carries both a sentinel value (which must never survive the
	// redacted path) and a secret assignment (which the sanitized path must
	// still scrub), so an inverted condition fails this test in both directions.
	payload := []byte(`{"body":"SENTINEL-PRIVATE","token":"abcd1234efgh5678ijkl"}`)

	tests := []struct {
		name   string
		redact bool
	}{
		{name: "redacted", redact: true},
		{name: "sanitized", redact: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			text := PayloadTextForLog(payload, tc.redact)
			assert.NotContains(text, "abcd1234efgh5678ijkl", "secret must never be logged")

			if tc.redact {
				assert.Equal(RedactedPayloadText(payload), text)
				assert.NotContains(text, "SENTINEL-PRIVATE", "redacted rendering must not expose payload content")
			} else {
				assert.Equal(SanitizeString(string(payload)), text)
				assert.Contains(text, "SENTINEL-PRIVATE", "sanitized rendering keeps non-secret content")
				assert.Contains(text, "[REDACTED]")
			}
		})
	}
}

func TestPayloadTextForLogEmptyPayload(t *testing.T) {
	assert := assert.New(t)

	assert.Equal(RedactedPayloadText(nil), PayloadTextForLog(nil, true))
	assert.Empty(PayloadTextForLog(nil, false))
}
