package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStrictLabelAgentPayload_NormalizesStaticRepoScopes(t *testing.T) {
	tests := []struct {
		name  string
		repos []interface{}
		want  []string
	}{
		{
			name:  "exact repository",
			repos: []interface{}{"BashRusakh/DeskForge"},
			want:  []string{"bashrusakh/deskforge"},
		},
		{
			name:  "owner and repository wildcards",
			repos: []interface{}{"MyOrg/*", "MyOrg/Desk*"},
			want:  []string{"myorg/*", "myorg/desk*"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := buildStrictLabelAgentPayload(map[string]interface{}{
				"allow-only": map[string]interface{}{
					"repos":         tt.repos,
					"min-integrity": "none",
				},
			})
			require.NoError(t, err)
			allowOnly, ok := payload["allow-only"].(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, tt.want, allowOnly["repos"])
		})
	}
}

func TestCheckBoolFailure(t *testing.T) {
	tests := []struct {
		name       string
		raw        map[string]interface{}
		resultJSON []byte
		key        string
		wantErr    string
	}{
		{
			name:       "key absent - no failure",
			raw:        map[string]interface{}{},
			resultJSON: []byte(`{}`),
			key:        "success",
			wantErr:    "",
		},
		{
			name:       "key true - no failure",
			raw:        map[string]interface{}{"success": true},
			resultJSON: []byte(`{"success":true}`),
			key:        "success",
			wantErr:    "",
		},
		{
			name:       "key false with error message",
			raw:        map[string]interface{}{"success": false, "error": "policy rejected"},
			resultJSON: []byte(`{"success":false,"error":"policy rejected"}`),
			key:        "success",
			wantErr:    "label_agent rejected policy: policy rejected",
		},
		{
			name:       "key false without error message",
			raw:        map[string]interface{}{"success": false},
			resultJSON: []byte(`{"success":false}`),
			key:        "success",
			wantErr:    "label_agent returned non-success status",
		},
		{
			name:       "key false with empty error message",
			raw:        map[string]interface{}{"success": false, "error": ""},
			resultJSON: []byte(`{"success":false,"error":""}`),
			key:        "success",
			wantErr:    "label_agent returned non-success status",
		},
		{
			name:       "key false with whitespace error message",
			raw:        map[string]interface{}{"success": false, "error": "   "},
			resultJSON: []byte(`{"success":false,"error":"   "}`),
			key:        "success",
			wantErr:    "label_agent returned non-success status",
		},
		{
			name:       "key is non-bool value - treated as absent",
			raw:        map[string]interface{}{"success": "true"},
			resultJSON: []byte(`{"success":"true"}`),
			key:        "success",
			wantErr:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBoolFailure(tt.raw, tt.resultJSON, tt.key)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestNormalizeLabelListField covers all branches of the unexported
// normalizeLabelListField helper used when validating refusal-labels in guard policies.
func TestNormalizeLabelListField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     interface{}
		want    []interface{}
		wantErr string
	}{
		// --- []interface{} path: valid inputs ---
		{
			name: "valid array returns trimmed labels",
			raw:  []interface{}{"unsafe", " needs-triage "},
			want: []interface{}{"unsafe", "needs-triage"},
		},
		{
			name: "empty array returns empty slice",
			raw:  []interface{}{},
			want: []interface{}{},
		},
		// --- []interface{} path: invalid inputs ---
		{
			name:    "array with empty string entry returns error",
			raw:     []interface{}{"valid", ""},
			wantErr: "each entry must be a non-empty string",
		},
		{
			name:    "array with non-string entry returns error",
			raw:     []interface{}{"valid", 99},
			wantErr: "each entry must be a non-empty string",
		},
		// --- neither []interface{} nor string: uncovered branch ---
		{
			name:    "integer value is not array or string",
			raw:     42,
			wantErr: "expected array of strings or comma/newline-delimited string",
		},
		{
			name:    "bool value is not array or string",
			raw:     true,
			wantErr: "expected array of strings or comma/newline-delimited string",
		},
		// --- string path: valid inputs ---
		{
			name: "comma-delimited string returns split labels",
			raw:  "unsafe,needs-triage",
			want: []interface{}{"unsafe", "needs-triage"},
		},
		{
			name: "newline-delimited string returns split labels",
			raw:  "unsafe\nneeds-triage",
			want: []interface{}{"unsafe", "needs-triage"},
		},
		{
			name: "mixed comma and newline delimiters",
			raw:  "unsafe,blocked\nneeds-review",
			want: []interface{}{"unsafe", "blocked", "needs-review"},
		},
		{
			name: "leading and trailing whitespace trimmed from each label",
			raw:  "  unsafe  ,  needs-triage  ",
			want: []interface{}{"unsafe", "needs-triage"},
		},
		{
			name: "single label string",
			raw:  "unsafe",
			want: []interface{}{"unsafe"},
		},
		// --- string path: empty/no-content inputs (len(parts)==0 branch) ---
		{
			name:    "empty string returns error",
			raw:     "",
			wantErr: "must include at least one non-empty label",
		},
		{
			name:    "string with only a comma delimiter returns error",
			raw:     ",",
			wantErr: "must include at least one non-empty label",
		},
		{
			name:    "string with only newline delimiter returns error",
			raw:     "\n",
			wantErr: "must include at least one non-empty label",
		},
		// --- string path: whitespace-only parts (continue and len(out)==0 branches) ---
		{
			// All FieldsFunc parts are whitespace-only: hits both continue and len(out)==0.
			name:    "whitespace-only string returns error",
			raw:     "  ",
			wantErr: "must include at least one non-empty label",
		},
		{
			// Parts separated by commas are all whitespace: hits continue for each then len(out)==0.
			name:    "comma-delimited whitespace-only parts returns error",
			raw:     "  ,  ,  ",
			wantErr: "must include at least one non-empty label",
		},
		{
			// One whitespace-only part among valid parts: hits continue, result is non-empty.
			name: "whitespace-only part among valid parts is skipped",
			raw:  "unsafe,  ,needs-triage",
			want: []interface{}{"unsafe", "needs-triage"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeLabelListField("refusal-labels", tt.raw)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
