package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

func TestBuildAllowedPromptSets(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want map[string]map[string]bool
	}{
		{
			name: "nil config returns empty map",
			cfg:  nil,
			want: map[string]map[string]bool{},
		},
		{
			name: "no servers returns empty map",
			cfg:  &config.Config{Servers: map[string]*config.ServerConfig{}},
			want: map[string]map[string]bool{},
		},
		{
			name: "server without prompts is unrestricted",
			cfg: &config.Config{Servers: map[string]*config.ServerConfig{
				"a": {},
			}},
			want: map[string]map[string]bool{},
		},
		{
			name: "wildcard permits all prompts",
			cfg: &config.Config{Servers: map[string]*config.ServerConfig{
				"a": {Prompts: []string{"x", "*"}},
			}},
			want: map[string]map[string]bool{},
		},
		{
			name: "explicit list builds set",
			cfg: &config.Config{Servers: map[string]*config.ServerConfig{
				"a": {Prompts: []string{"p1", "p2", "p1"}},
			}},
			want: map[string]map[string]bool{
				"a": {"p1": true, "p2": true},
			},
		},
		{
			name: "mixed servers only restrict those with lists",
			cfg: &config.Config{Servers: map[string]*config.ServerConfig{
				"a": {Prompts: []string{"p1"}},
				"b": {Prompts: []string{"*"}},
				"c": {},
				"d": {Prompts: []string{"q1", "q2"}},
			}},
			want: map[string]map[string]bool{
				"a": {"p1": true},
				"d": {"q1": true, "q2": true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAllowedPromptSets(tt.cfg)
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got)
		})
	}
}
