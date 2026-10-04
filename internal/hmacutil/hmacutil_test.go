package hmacutil

import (
	"encoding/hex"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignAndVerify(t *testing.T) {
	key := []byte("test-key")
	message := "canonical message"
	signature := Sign(key, message)

	assert.Len(t, signature, 32)
	assert.True(t, Verify(key, message, signature))
	assert.False(t, Verify(key, message+" changed", signature))
	assert.False(t, Verify([]byte("other-key"), message, signature))
	assert.False(t, Verify(key, message, signature[:len(signature)-1]))
	assert.False(t, Verify(key, message, nil))
}

func TestSignKnownVector(t *testing.T) {
	// RFC 4231 test case 2
	sig := Sign([]byte("Jefe"), "what do ya want for nothing?")
	assert.Equal(t, "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843", hex.EncodeToString(sig))
}

func TestVerifyTable(t *testing.T) {
	key := []byte("k")
	valid := Sign(key, "msg")
	flipped := append([]byte(nil), valid...)
	flipped[0] ^= 0xff

	tests := []struct {
		name string
		key  []byte
		msg  string
		mac  []byte
		want bool
	}{
		{"valid", key, "msg", valid, true},
		{"empty MAC", key, "msg", []byte{}, false},
		{"flipped bit", key, "msg", flipped, false},
		{"extra byte", key, "msg", append(append([]byte(nil), valid...), 0), false},
		{"empty key and message round trip", nil, "", Sign(nil, ""), true},
		{"empty message wrong MAC", key, "", valid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Verify(tt.key, tt.msg, tt.mac))
		})
	}
}

func TestSignDeterministic(t *testing.T) {
	assert.Equal(t, Sign([]byte("k"), "m"), Sign([]byte("k"), "m"))
	assert.NotEqual(t, Sign([]byte("k"), "m"), Sign([]byte("k"), "n"))
}

func TestSignAndVerifyDebugLogging(t *testing.T) {
	if os.Getenv("GO_WANT_HMACUTIL_DEBUG_SUBPROCESS") == "1" {
		require.True(t, logHMACUtil.Enabled())

		key := []byte("private-test-key")
		message := "sensitive message"
		signature := Sign(key, message)

		assert.True(t, Verify(key, message, signature))
		assert.False(t, Verify(key, message, []byte("invalid-mac")))
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestSignAndVerifyDebugLogging", "-test.v")
	cmd.Env = append(os.Environ(),
		"GO_WANT_HMACUTIL_DEBUG_SUBPROCESS=1",
		"DEBUG=hmacutil:*",
		"DEBUG_COLORS=0",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess output:\n%s", output)

	logOutput := string(output)
	assert.Contains(t, logOutput, "hmacutil:hmacutil Signing message: keyLen=")
	assert.Contains(t, logOutput, "hmacutil:hmacutil Signature computed: sigLen=32")
	assert.Contains(t, logOutput, "hmacutil:hmacutil Verifying signature: keyLen=")
	assert.Contains(t, logOutput, "hmacutil:hmacutil Signature verification succeeded")
	assert.Contains(t, logOutput, "hmacutil:hmacutil Signature verification failed: MAC mismatch")
	assert.NotContains(t, logOutput, "private-test-key")
	assert.NotContains(t, logOutput, "sensitive message")
}
