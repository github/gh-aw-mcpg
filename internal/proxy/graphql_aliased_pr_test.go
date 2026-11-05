package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasAliasedPRResource(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"no aliases", `query { repository(owner:"o", name:"r") { pullRequest(number:1) { title } } }`, false},
		{"alias equal to field name is ignored", `query { repository(owner:"o", name:"r") { pullRequest: pullRequest(number:1) { title } } }`, false},
		{"top-level alias", `query { r: repository(owner:"o", name:"r") { name } }`, true},
		{"nested alias", `query { repository(owner:"o", name:"r") { pr: pullRequest(number:1) { title } } }`, true},
		{"alias inside inline fragment", `query { repository(owner:"o", name:"r") { ... on Repository { pr: pullRequest(number:1) { title } } } }`, true},
		{"no alias inside inline fragment", `query { repository(owner:"o", name:"r") { ... on Repository { name } } }`, false},
		{"alias in named fragment", `query { repository(owner:"o", name:"r") { ...F } } fragment F on Repository { pr: pullRequest(number:1) { title } }`, true},
		{"no alias in named fragment", `query { repository(owner:"o", name:"r") { ...F } } fragment F on Repository { name }`, false},
		{"alias in later operation", `query A { viewer { login } } query B { x: viewer { login } }`, true},
		{"unparseable query is treated as aliased", `query { `, true},
		{"empty query has no aliases", ``, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasAliasedPRResource(tt.query))
		})
	}
}
