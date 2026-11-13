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
