package server

import (
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestAllowedSets_BuilderSemantics(t *testing.T) {
	for _, builder := range []struct {
		name  string
		build func(*config.Config) map[string]map[string]bool
	}{
		{"tools", buildAllowedToolSets},
		{"prompts", buildAllowedPromptSets},
	} {
		t.Run(builder.name, func(t *testing.T) {
			assert.Equal(t, map[string]map[string]bool{}, builder.build(nil))
			assert.Equal(t, map[string]map[string]bool{}, builder.build(&config.Config{}))

			cfg := &config.Config{Servers: map[string]*config.ServerConfig{
				"nil":      {},
				"empty":    {Tools: []string{}, Prompts: []string{}},
				"wildcard": {Tools: []string{"*"}, Prompts: []string{"*"}},
				"mixed":    {Tools: []string{"first", "*", "last"}, Prompts: []string{"first", "*", "last"}},
				"limited": {Tools: []string{"tool", "tool", "other"},
					Prompts: []string{"prompt", "prompt", "other"}},
			}}
			name := "tool"
			if builder.name == "prompts" {
				name = "prompt"
			}
			assert.Equal(t, map[string]map[string]bool{
				"limited": {name: true, "other": true},
			}, builder.build(cfg))
			assert.Len(t, cfg.Servers["limited"].Tools, 3)
			assert.Len(t, cfg.Servers["limited"].Prompts, 3)
		})
	}
}

func TestAllowedByServerSet(t *testing.T) {
	tests := []struct {
		name      string
		sets      map[string]map[string]bool
		wantAllow bool
	}{
		{"nil map allows all", nil, true},
		{"empty map allows all", map[string]map[string]bool{}, true},
		{"missing server allows all", map[string]map[string]bool{"other": {"name": false}}, true},
		{"nil set allows all", map[string]map[string]bool{"s": nil}, true},
		{"empty set denies all", map[string]map[string]bool{"s": {}}, false},
		{"listed name allowed", map[string]map[string]bool{"s": {"name": true}}, true},
		{"unlisted name denied", map[string]map[string]bool{"s": {"other": true}}, false},
		{"false entry denied", map[string]map[string]bool{"s": {"name": false}}, false},
		{"other server entry ignored", map[string]map[string]bool{"s": {}, "other": {"name": true}}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantAllow, allowedByServerSet(tc.sets, "s", "name"))

			us := &UnifiedServer{
				allowedToolSets:   tc.sets,
				allowedPromptSets: tc.sets,
			}
			assert.Equal(t, tc.wantAllow, us.isToolAllowed("s", "name"))
			assert.Equal(t, tc.wantAllow, us.isPromptAllowed("s", "name"))
		})
	}
}

func TestUnifiedServer_SeparateAllowlists(t *testing.T) {
	us := &UnifiedServer{
		allowedToolSets:   map[string]map[string]bool{"s": {"tool": true}},
		allowedPromptSets: map[string]map[string]bool{"s": {"prompt": true}},
	}
	assert.True(t, us.isToolAllowed("s", "tool"))
	assert.False(t, us.isToolAllowed("s", "prompt"))
	assert.True(t, us.isPromptAllowed("s", "prompt"))
	assert.False(t, us.isPromptAllowed("s", "tool"))
}
