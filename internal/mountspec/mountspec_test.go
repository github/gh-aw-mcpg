package mountspec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name         string
		spec         string
		wantWritable bool
		wantErr      bool
		errorKind    ErrorKind
	}{
		{name: "read-only", spec: "/srv/data:/data:ro"},
		{name: "read-write", spec: "/srv/data:/data:rw", wantWritable: true},
		{name: "omitted mode defaults to read-write", spec: "/srv/data:/data", wantWritable: true},
		{name: "mode whitespace is trimmed", spec: "/srv/data:/data: ro "},
		{name: "duplicate same modes rejected", spec: "/srv/data:/data:ro,ro", wantErr: true, errorKind: InvalidOptions},
		{name: "single segment rejected", spec: "/srv/data", wantErr: true, errorKind: InvalidFormat},
		{name: "empty spec rejected", spec: "", wantErr: true, errorKind: InvalidFormat},
		{name: "empty mode rejected", spec: "/srv/data:/data:", wantErr: true, errorKind: InvalidOptions},
		{name: "duplicate modes rejected", spec: "/srv/data:/data:ro,rw", wantErr: true, errorKind: InvalidOptions},
		{name: "empty mode option rejected", spec: "/srv/data:/data:ro,,rw", wantErr: true, errorKind: InvalidOptions},
		{name: "unsupported option rejected", spec: "/srv/data:/data:rslave", wantErr: true, errorKind: InvalidOptions},
		{name: "empty source rejected", spec: ":/data:ro", wantErr: true, errorKind: EmptySource},
		{name: "empty destination rejected", spec: "/srv/data::ro", wantErr: true, errorKind: EmptyDestination},
		{name: "relative source rejected", spec: "data:/data:ro", wantErr: true, errorKind: RelativeSource},
		{name: "relative destination rejected", spec: "/srv/data:data:ro", wantErr: true, errorKind: RelativeDestination},
		{name: "too many segments rejected", spec: "/srv/data:/data:ro:extra", wantErr: true, errorKind: InvalidFormat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mount, err := Parse(tt.spec)
			if !tt.wantErr {
				require.NoError(t, err)
				assert.Equal(t, "/srv/data", mount.Source)
				assert.Equal(t, "/data", mount.Destination)
				assert.Equal(t, tt.wantWritable, mount.Writable)
				return
			}

			require.Error(t, err)
			var parseErr *ParseError
			require.ErrorAs(t, err, &parseErr)
			assert.Equal(t, tt.errorKind, parseErr.Kind)
		})
	}
}

func TestParseRequiredMode(t *testing.T) {
	tests := []struct {
		name         string
		spec         string
		wantErr      bool
		errorKind    ErrorKind
		wantWritable bool
	}{
		{name: "explicit ro accepted", spec: "/srv/data:/data:ro", wantWritable: false},
		{name: "explicit rw accepted", spec: "/srv/data:/data:rw", wantWritable: true},
		{name: "missing mode rejected", spec: "/srv/data:/data", wantErr: true, errorKind: InvalidFormat},
		{name: "too few segments rejected", spec: "/srv/data", wantErr: true, errorKind: InvalidFormat},
		{name: "too many segments rejected", spec: "/srv/data:/data:ro:extra", wantErr: true, errorKind: InvalidFormat},
		{name: "non ro/rw mode token rejected", spec: "/srv/data:/data:rslave", wantErr: true, errorKind: InvalidOptions},
		{name: "empty mode token rejected", spec: "/srv/data:/data:", wantErr: true, errorKind: InvalidOptions},
		{name: "delegates to Parse for other validation errors", spec: "data:/data:ro", wantErr: true, errorKind: RelativeSource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mount, err := ParseRequiredMode(tt.spec)
			if !tt.wantErr {
				require.NoError(t, err)
				assert.Equal(t, "/srv/data", mount.Source)
				assert.Equal(t, "/data", mount.Destination)
				assert.Equal(t, tt.wantWritable, mount.Writable)
				return
			}

			require.Error(t, err)
			var parseErr *ParseError
			require.ErrorAs(t, err, &parseErr)
			assert.Equal(t, tt.errorKind, parseErr.Kind)
		})
	}
}

func TestParseErrorMessages(t *testing.T) {
	tests := []struct {
		name    string
		kind    ErrorKind
		wantMsg string
	}{
		{name: "invalid format", kind: InvalidFormat, wantMsg: "expected 'source:dest:mode'"},
		{name: "empty source", kind: EmptySource, wantMsg: "source and destination must not be empty"},
		{name: "empty destination", kind: EmptyDestination, wantMsg: "source and destination must not be empty"},
		{name: "relative source", kind: RelativeSource, wantMsg: "host source must be an absolute path"},
		{name: "relative destination", kind: RelativeDestination, wantMsg: "container destination must be an absolute path"},
		{name: "invalid options", kind: InvalidOptions, wantMsg: "invalid mount options"},
		{name: "unknown kind falls back to default", kind: ErrorKind(999), wantMsg: "invalid mount options"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ParseError{Kind: tt.kind}
			assert.Equal(t, tt.wantMsg, err.Error())
		})
	}
}

func TestParseWrappedErrorMessages(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantMsg string
	}{
		{name: "conflicting options", spec: "/a:/b:ro,rw", wantMsg: "conflicting mount options"},
		{name: "empty option", spec: "/a:/b:ro,,", wantMsg: "empty mount option"},
		{name: "unsupported option", spec: "/a:/b:z", wantMsg: "unsupported mount option"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.spec)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
			assert.Contains(t, err.Error(), "invalid mount options")
		})
	}
}
