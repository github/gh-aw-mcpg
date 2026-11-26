package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashForLog_SharedLengths(t *testing.T) {
	tests := []struct {
		name   string
		hexLen int
		want   string
	}{
		{"log", LogHashHexLength, "hash:ba7816bf8f01cfea"},
		{"audit", AuditHashHexLength, "hash:ba7816bf8f01cfea414140de5dae2223"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HashForLog("abc", tt.hexLen, "hash:"))
			assert.Equal(t, "(none)", HashForLog("", tt.hexLen, "hash:"))
		})
	}
}

func TestHashPathForLogIf(t *testing.T) {
	const path = "/repos/org/repo/issues/1"
	tests := []struct {
		name      string
		sensitive bool
		path      string
		want      string
	}{
		{"ordinary", false, path, path},
		{"ordinary empty", false, "", ""},
		{"sensitive", true, path, "path:c555d171c60c9501"},
		{"sensitive empty", true, "", "(none)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HashPathForLogIf(tt.sensitive, tt.path))
		})
	}
}
