package difc

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLabel_BasicOperations tests basic label operations
func TestLabel_BasicOperations(t *testing.T) {
	t.Run("NewLabel creates empty label", func(t *testing.T) {
		label := NewLabel()
		assert.NotNil(t, label)
		assert.True(t, label.IsEmpty())
		assert.Empty(t, label.GetTags())
	})

	t.Run("Add single tag", func(t *testing.T) {
		label := NewLabel()
		label.Add("repo:owner/name")

		assert.False(t, label.IsEmpty())
		assert.True(t, label.Contains("repo:owner/name"))
		assert.Len(t, label.GetTags(), 1)
	})

	t.Run("Add multiple tags individually", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")
		label.Add("tag2")
		label.Add("tag3")

		assert.False(t, label.IsEmpty())
		assert.True(t, label.Contains("tag1"))
		assert.True(t, label.Contains("tag2"))
		assert.True(t, label.Contains("tag3"))
		assert.Len(t, label.GetTags(), 3)
	})

	t.Run("Add duplicate tags", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")
		label.Add("tag1")
		label.Add("tag1")

		assert.Len(t, label.GetTags(), 1, "Duplicate tags should not be added")
	})

	t.Run("AddAll with slice of tags", func(t *testing.T) {
		label := NewLabel()
		tags := []Tag{"tag1", "tag2", "tag3"}
		label.AddAll(tags)

		assert.Len(t, label.GetTags(), 3)
		assert.True(t, label.Contains("tag1"))
		assert.True(t, label.Contains("tag2"))
		assert.True(t, label.Contains("tag3"))
	})

	t.Run("AddAll with empty slice", func(t *testing.T) {
		label := NewLabel()
		label.AddAll([]Tag{})

		assert.True(t, label.IsEmpty())
	})

	t.Run("AddAll with nil slice", func(t *testing.T) {
		label := NewLabel()
		label.AddAll(nil)

		assert.True(t, label.IsEmpty())
	})

	t.Run("Contains returns false for non-existent tag", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")

		assert.False(t, label.Contains("tag2"))
		assert.False(t, label.Contains(""))
	})
}

// TestLabel_Union tests label union operations
func TestLabel_Union(t *testing.T) {
	t.Run("Union with another label", func(t *testing.T) {
		label1 := NewLabel()
		label1.Add("tag1")
		label1.Add("tag2")

		label2 := NewLabel()
		label2.Add("tag3")
		label2.Add("tag4")

		label1.Union(label2)

		assert.Len(t, label1.GetTags(), 4)
		assert.True(t, label1.Contains("tag1"))
		assert.True(t, label1.Contains("tag2"))
		assert.True(t, label1.Contains("tag3"))
		assert.True(t, label1.Contains("tag4"))
	})

	t.Run("Union with overlapping tags", func(t *testing.T) {
		label1 := NewLabel()
		label1.Add("tag1")
		label1.Add("tag2")

		label2 := NewLabel()
		label2.Add("tag2")
		label2.Add("tag3")

		label1.Union(label2)

		assert.Len(t, label1.GetTags(), 3, "Should have 3 unique tags")
	})

	t.Run("Union with nil label", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")

		label.Union(nil)

		assert.Len(t, label.GetTags(), 1)
		assert.True(t, label.Contains("tag1"))
	})

	t.Run("Union with empty label", func(t *testing.T) {
		label1 := NewLabel()
		label1.Add("tag1")

		label2 := NewLabel()

		label1.Union(label2)

		assert.Len(t, label1.GetTags(), 1)
	})
}

// TestLabel_Clone tests label cloning
func TestLabel_Clone(t *testing.T) {
	t.Run("Clone empty label", func(t *testing.T) {
		label := NewLabel()
		clone := label.Clone()

		assert.NotSame(t, label, clone)
		assert.True(t, clone.IsEmpty())
	})

	t.Run("Clone label with tags", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")
		label.Add("tag2")

		clone := label.Clone()

		assert.NotSame(t, label, clone)
		assert.Len(t, clone.GetTags(), 2)
		assert.True(t, clone.Contains("tag1"))
		assert.True(t, clone.Contains("tag2"))
	})

	t.Run("Clone is independent", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")

		clone := label.Clone()
		clone.Add("tag2")

		assert.Len(t, label.GetTags(), 1, "Original should not be affected")
		assert.Len(t, clone.GetTags(), 2, "Clone should have 2 tags")
		assert.False(t, label.Contains("tag2"))
		assert.True(t, clone.Contains("tag2"))
	})
}

// TestLabel_Concurrency tests concurrent label operations
func TestLabel_Concurrency(t *testing.T) {
	t.Run("Concurrent Add operations", func(t *testing.T) {
		label := NewLabel()
		var wg sync.WaitGroup

		// Concurrently add 100 different tags
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				label.Add(Tag("tag" + string(rune('A'+id%26)) + string(rune('0'+id/26))))
			}(i)
		}

		wg.Wait()

		// Should have many tags, no panics
		tags := label.GetTags()
		assert.NotEmpty(t, tags)
	})

	t.Run("Concurrent Contains checks", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")
		label.Add("tag2")

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				label.Contains("tag1")
				label.Contains("tag2")
				label.Contains("nonexistent")
			}()
		}

		wg.Wait()
	})

	t.Run("Concurrent GetTags", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")
		label.Add("tag2")
		label.Add("tag3")

		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tags := label.GetTags()
				assert.Len(t, tags, 3)
			}()
		}

		wg.Wait()
	})

	t.Run("Concurrent Union operations", func(t *testing.T) {
		label1 := NewLabel()
		label1.Add("tag1")

		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				label2 := NewLabel()
				label2.Add(Tag("tag" + string(rune('A'+id))))
				label1.Union(label2)
			}(i)
		}

		wg.Wait()

		// Should have many tags, no panics
		tags := label1.GetTags()
		assert.NotEmpty(t, tags)
	})
}

// TestSecrecyLabel_CanFlowTo tests secrecy label flow checks
func TestSecrecyLabel_CanFlowTo(t *testing.T) {
	t.Run("Empty secrecy labels can flow", func(t *testing.T) {
		source := NewSecrecyLabel()
		target := NewSecrecyLabel()

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Nil source can flow to any target", func(t *testing.T) {
		var source *SecrecyLabel
		target := NewSecrecyLabel()
		target.Label.Add("tag1")

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Non-empty source cannot flow to nil target", func(t *testing.T) {
		source := NewSecrecyLabel()
		source.Label.Add("tag1")
		var target *SecrecyLabel

		assert.False(t, source.CanFlowTo(target))
	})

	t.Run("Empty source can flow to nil target", func(t *testing.T) {
		source := NewSecrecyLabel()
		var target *SecrecyLabel

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Same tags can flow", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Subset can flow to superset", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1"})
		target := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2", "tag3"})

		assert.True(t, source.CanFlowTo(target), "Source has fewer tags, can flow to target with more tags")
	})

	t.Run("Superset cannot flow to subset", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2", "tag3"})
		target := NewSecrecyLabelWithTags([]Tag{"tag1"})

		assert.False(t, source.CanFlowTo(target), "Source has extra tags that target doesn't have")
	})

	t.Run("Disjoint tags cannot flow", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewSecrecyLabelWithTags([]Tag{"tag3", "tag4"})

		assert.False(t, source.CanFlowTo(target))
	})

	t.Run("Partial overlap cannot flow", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewSecrecyLabelWithTags([]Tag{"tag2", "tag3"})

		assert.False(t, source.CanFlowTo(target), "Source has tag1 which target doesn't have")
	})
}

// TestSecrecyLabel_CheckFlow tests secrecy label flow checks with details
func TestSecrecyLabel_CheckFlow(t *testing.T) {
	t.Run("Empty labels return no violations", func(t *testing.T) {
		source := NewSecrecyLabel()
		target := NewSecrecyLabel()

		canFlow, extraTags := source.CheckFlow(target)
		assert.True(t, canFlow)
		assert.Empty(t, extraTags)
	})

	t.Run("Nil source can flow", func(t *testing.T) {
		var source *SecrecyLabel
		target := NewSecrecyLabel()
		target.Label.Add("tag1")

		canFlow, extraTags := source.CheckFlow(target)
		assert.True(t, canFlow)
		assert.Empty(t, extraTags)
	})

	t.Run("Non-empty source to nil target returns extra tags", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})
		var target *SecrecyLabel

		canFlow, extraTags := source.CheckFlow(target)
		assert.False(t, canFlow)
		assert.Len(t, extraTags, 2)
		assert.Contains(t, extraTags, Tag("tag1"))
		assert.Contains(t, extraTags, Tag("tag2"))
	})

	t.Run("Same tags can flow with no extra tags", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})

		canFlow, extraTags := source.CheckFlow(target)
		assert.True(t, canFlow)
		assert.Empty(t, extraTags)
	})

	t.Run("Extra tags in source are reported", func(t *testing.T) {
		source := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2", "tag3"})
		target := NewSecrecyLabelWithTags([]Tag{"tag1"})

		canFlow, extraTags := source.CheckFlow(target)
		assert.False(t, canFlow)
		assert.Len(t, extraTags, 2)
		assert.Contains(t, extraTags, Tag("tag2"))
		assert.Contains(t, extraTags, Tag("tag3"))
	})

	t.Run("CheckFlow consistency with CanFlowTo", func(t *testing.T) {
		tests := []struct {
			name       string
			sourceTags []Tag
			targetTags []Tag
		}{
			{"empty to empty", []Tag{}, []Tag{}},
			{"empty to non-empty", []Tag{}, []Tag{"tag1"}},
			{"non-empty to empty", []Tag{"tag1"}, []Tag{}},
			{"subset to superset", []Tag{"tag1"}, []Tag{"tag1", "tag2"}},
			{"superset to subset", []Tag{"tag1", "tag2"}, []Tag{"tag1"}},
			{"disjoint sets", []Tag{"tag1"}, []Tag{"tag2"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				source := NewSecrecyLabelWithTags(tt.sourceTags)
				target := NewSecrecyLabelWithTags(tt.targetTags)

				canFlowResult := source.CanFlowTo(target)
				checkFlowResult, _ := source.CheckFlow(target)

				assert.Equal(t, canFlowResult, checkFlowResult, "CanFlowTo and CheckFlow should agree")
			})
		}
	})
}

// TestSecrecyLabel_Clone tests secrecy label cloning
func TestSecrecyLabel_Clone(t *testing.T) {
	t.Run("Clone empty secrecy label", func(t *testing.T) {
		label := NewSecrecyLabel()
		clone := label.Clone()

		assert.NotSame(t, label, clone)
		assert.NotSame(t, label.Label, clone.Label)
		assert.True(t, clone.Label.IsEmpty())
	})

	t.Run("Clone nil secrecy label", func(t *testing.T) {
		var label *SecrecyLabel
		clone := label.Clone()

		assert.NotNil(t, clone)
		assert.True(t, clone.Label.IsEmpty())
	})

	t.Run("Clone with tags is independent", func(t *testing.T) {
		label := NewSecrecyLabelWithTags([]Tag{"tag1", "tag2"})
		clone := label.Clone()

		clone.Label.Add("tag3")

		assert.Len(t, label.Label.GetTags(), 2, "Original should not be affected")
		assert.Len(t, clone.Label.GetTags(), 3, "Clone should have 3 tags")
	})
}

// TestIntegrityLabel_CanFlowTo tests integrity label flow checks
func TestIntegrityLabel_CanFlowTo(t *testing.T) {
	t.Run("Empty integrity labels can flow", func(t *testing.T) {
		source := NewIntegrityLabel()
		target := NewIntegrityLabel()

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Nil source cannot flow to non-empty target", func(t *testing.T) {
		var source *IntegrityLabel
		target := NewIntegrityLabel()
		target.Label.Add("tag1")

		assert.False(t, source.CanFlowTo(target), "Nil source lacks integrity to flow to target")
	})

	t.Run("Non-empty source can flow to nil target", func(t *testing.T) {
		source := NewIntegrityLabel()
		source.Label.Add("tag1")
		var target *IntegrityLabel

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Nil source can flow to nil target", func(t *testing.T) {
		var source *IntegrityLabel
		var target *IntegrityLabel

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Same tags can flow", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})

		assert.True(t, source.CanFlowTo(target))
	})

	t.Run("Superset can flow to subset", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2", "tag3"})
		target := NewIntegrityLabelWithTags([]Tag{"tag1"})

		assert.True(t, source.CanFlowTo(target), "Source has all tags that target requires")
	})

	t.Run("Subset cannot flow to superset", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1"})
		target := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2", "tag3"})

		assert.False(t, source.CanFlowTo(target), "Source lacks integrity tags that target requires")
	})

	t.Run("Disjoint tags cannot flow", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewIntegrityLabelWithTags([]Tag{"tag3", "tag4"})

		assert.False(t, source.CanFlowTo(target))
	})

	t.Run("Partial overlap cannot flow", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewIntegrityLabelWithTags([]Tag{"tag2", "tag3"})

		assert.False(t, source.CanFlowTo(target), "Source lacks tag3")
	})
}

// TestIntegrityLabel_CheckFlow tests integrity label flow checks with details
func TestIntegrityLabel_CheckFlow(t *testing.T) {
	t.Run("Empty labels return no violations", func(t *testing.T) {
		source := NewIntegrityLabel()
		target := NewIntegrityLabel()

		canFlow, missingTags := source.CheckFlow(target)
		assert.True(t, canFlow)
		assert.Empty(t, missingTags)
	})

	t.Run("Nil source to non-empty target returns missing tags", func(t *testing.T) {
		var source *IntegrityLabel
		target := NewIntegrityLabel()
		target.Label.Add("tag1")
		target.Label.Add("tag2")

		canFlow, missingTags := source.CheckFlow(target)
		assert.False(t, canFlow)
		assert.Len(t, missingTags, 2)
		assert.Contains(t, missingTags, Tag("tag1"))
		assert.Contains(t, missingTags, Tag("tag2"))
	})

	t.Run("Non-empty source to nil target can flow", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})
		var target *IntegrityLabel

		canFlow, missingTags := source.CheckFlow(target)
		assert.True(t, canFlow)
		assert.Empty(t, missingTags)
	})

	t.Run("Same tags can flow with no missing tags", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})
		target := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})

		canFlow, missingTags := source.CheckFlow(target)
		assert.True(t, canFlow)
		assert.Empty(t, missingTags)
	})

	t.Run("Missing tags in source are reported", func(t *testing.T) {
		source := NewIntegrityLabelWithTags([]Tag{"tag1"})
		target := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2", "tag3"})

		canFlow, missingTags := source.CheckFlow(target)
		assert.False(t, canFlow)
		assert.Len(t, missingTags, 2)
		assert.Contains(t, missingTags, Tag("tag2"))
		assert.Contains(t, missingTags, Tag("tag3"))
	})

	t.Run("CheckFlow consistency with CanFlowTo", func(t *testing.T) {
		tests := []struct {
			name       string
			sourceTags []Tag
			targetTags []Tag
		}{
			{"empty to empty", []Tag{}, []Tag{}},
			{"empty to non-empty", []Tag{}, []Tag{"tag1"}},
			{"non-empty to empty", []Tag{"tag1"}, []Tag{}},
			{"subset to superset", []Tag{"tag1"}, []Tag{"tag1", "tag2"}},
			{"superset to subset", []Tag{"tag1", "tag2"}, []Tag{"tag1"}},
			{"disjoint sets", []Tag{"tag1"}, []Tag{"tag2"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				source := NewIntegrityLabelWithTags(tt.sourceTags)
				target := NewIntegrityLabelWithTags(tt.targetTags)

				canFlowResult := source.CanFlowTo(target)
				checkFlowResult, _ := source.CheckFlow(target)

				assert.Equal(t, canFlowResult, checkFlowResult, "CanFlowTo and CheckFlow should agree")
			})
		}
	})
}

// TestIntegrityLabel_Clone tests integrity label cloning
func TestIntegrityLabel_Clone(t *testing.T) {
	t.Run("Clone empty integrity label", func(t *testing.T) {
		label := NewIntegrityLabel()
		clone := label.Clone()

		assert.NotSame(t, label, clone)
		assert.NotSame(t, label.Label, clone.Label)
		assert.True(t, clone.Label.IsEmpty())
	})

	t.Run("Clone nil integrity label", func(t *testing.T) {
		var label *IntegrityLabel
		clone := label.Clone()

		assert.NotNil(t, clone)
		assert.True(t, clone.Label.IsEmpty())
	})

	t.Run("Clone with tags is independent", func(t *testing.T) {
		label := NewIntegrityLabelWithTags([]Tag{"tag1", "tag2"})
		clone := label.Clone()

		clone.Label.Add("tag3")

		assert.Len(t, label.Label.GetTags(), 2, "Original should not be affected")
		assert.Len(t, clone.Label.GetTags(), 3, "Clone should have 3 tags")
	})
}

// TestViolationError tests DIFC violation error formatting
func TestViolationError(t *testing.T) {
	t.Run("Secrecy violation error message", func(t *testing.T) {
		err := &ViolationError{
			Type:      SecrecyViolation,
			Resource:  "test-resource",
			ExtraTags: []Tag{"secret:private", "secret:confidential"},
		}

		msg := err.Error()
		assert.Contains(t, msg, "Secrecy violation")
		assert.Contains(t, msg, "test-resource")
		assert.Contains(t, msg, "secret:private")
		assert.Contains(t, msg, "secret:confidential")
		assert.Contains(t, msg, "Remediation")
	})

	t.Run("Integrity write violation error message", func(t *testing.T) {
		err := &ViolationError{
			Type:        IntegrityViolation,
			Resource:    "write-resource",
			IsWrite:     true,
			MissingTags: []Tag{"verified", "trusted"},
		}

		msg := err.Error()
		assert.Contains(t, msg, "Integrity violation")
		assert.Contains(t, msg, "write")
		assert.Contains(t, msg, "write-resource")
		assert.Contains(t, msg, "verified")
		assert.Contains(t, msg, "trusted")
		assert.Contains(t, msg, "Remediation")
	})

	t.Run("Integrity read violation error message", func(t *testing.T) {
		err := &ViolationError{
			Type:        IntegrityViolation,
			Resource:    "read-resource",
			IsWrite:     false,
			MissingTags: []Tag{"verified"},
		}

		msg := err.Error()
		assert.Contains(t, msg, "Integrity violation")
		assert.Contains(t, msg, "read")
		assert.Contains(t, msg, "read-resource")
		assert.Contains(t, msg, "verified")
		assert.Contains(t, msg, "Remediation")
	})

	t.Run("Detailed error includes agent and resource tags", func(t *testing.T) {
		err := &ViolationError{
			Type:         SecrecyViolation,
			Resource:     "test-resource",
			ExtraTags:    []Tag{"secret:private"},
			AgentTags:    []Tag{"secret:private", "public"},
			ResourceTags: []Tag{"public"},
		}

		detailed := err.Detailed()
		assert.Contains(t, detailed, "Agent")
		assert.Contains(t, detailed, "Resource")
		assert.Contains(t, detailed, "secret:private")
		assert.Contains(t, detailed, "public")
	})

	t.Run("Secrecy violation with empty extra tags", func(t *testing.T) {
		err := &ViolationError{
			Type:      SecrecyViolation,
			Resource:  "test-resource",
			ExtraTags: []Tag{},
		}

		msg := err.Error()
		assert.Contains(t, msg, "Secrecy violation")
		assert.Contains(t, msg, "test-resource")
	})

	t.Run("Integrity violation with empty missing tags", func(t *testing.T) {
		err := &ViolationError{
			Type:        IntegrityViolation,
			Resource:    "test-resource",
			IsWrite:     true,
			MissingTags: []Tag{},
		}

		msg := err.Error()
		assert.Contains(t, msg, "Integrity violation")
		assert.Contains(t, msg, "test-resource")
	})

	t.Run("ViolationError implements error interface", func(t *testing.T) {
		var err error = &ViolationError{
			Type:     SecrecyViolation,
			Resource: "test",
		}

		require.NotNil(t, err)
		assert.NotEmpty(t, err.Error())
	})
}

// TestSecrecyIntegritySemanticDifference tests that secrecy and integrity have opposite flow semantics
func TestSecrecyIntegritySemanticDifference(t *testing.T) {
	t.Run("Secrecy: subset flows to superset, integrity: opposite", func(t *testing.T) {
		sourceTags := []Tag{"tag1"}
		targetTags := []Tag{"tag1", "tag2"}

		// Secrecy: source ⊆ target → can flow
		secrecySource := NewSecrecyLabelWithTags(sourceTags)
		secrecyTarget := NewSecrecyLabelWithTags(targetTags)
		assert.True(t, secrecySource.CanFlowTo(secrecyTarget), "Secrecy: subset can flow to superset")

		// Integrity: source ⊇ target → source needs all target's tags to flow
		// source has tag1, target needs tag1 and tag2 → source lacks tag2
		integritySource := NewIntegrityLabelWithTags(sourceTags)
		integrityTarget := NewIntegrityLabelWithTags(targetTags)
		assert.False(t, integritySource.CanFlowTo(integrityTarget), "Integrity: subset cannot flow to superset")
	})

	t.Run("Secrecy: superset cannot flow to subset, integrity: can flow", func(t *testing.T) {
		sourceTags := []Tag{"tag1", "tag2"}
		targetTags := []Tag{"tag1"}

		// Secrecy: source has tag2 that target doesn't have → cannot flow
		secrecySource := NewSecrecyLabelWithTags(sourceTags)
		secrecyTarget := NewSecrecyLabelWithTags(targetTags)
		assert.False(t, secrecySource.CanFlowTo(secrecyTarget), "Secrecy: superset cannot flow to subset")

		// Integrity: source has all tags target needs → can flow
		integritySource := NewIntegrityLabelWithTags(sourceTags)
		integrityTarget := NewIntegrityLabelWithTags(targetTags)
		assert.True(t, integritySource.CanFlowTo(integrityTarget), "Integrity: superset can flow to subset")
	})
}

// TestLabel_EdgeCases tests edge cases
func TestLabel_EdgeCases(t *testing.T) {
	t.Run("Empty tag string", func(t *testing.T) {
		label := NewLabel()
		label.Add("")

		assert.False(t, label.IsEmpty(), "Empty string is still a valid tag")
		assert.True(t, label.Contains(""))
	})

	t.Run("Special characters in tags", func(t *testing.T) {
		label := NewLabel()
		specialTags := []Tag{
			"tag:with:colons",
			"tag/with/slashes",
			"tag-with-dashes",
			"tag_with_underscores",
			"tag.with.dots",
			"tag with spaces",
			"tag@with@at",
		}

		for _, tag := range specialTags {
			label.Add(tag)
		}

		assert.Len(t, label.GetTags(), len(specialTags))
		for _, tag := range specialTags {
			assert.True(t, label.Contains(tag))
		}
	})

	t.Run("Very long tag", func(t *testing.T) {
		label := NewLabel()
		longTag := Tag(strings.Repeat("a", 1000))
		label.Add(longTag)

		assert.True(t, label.Contains(longTag))
	})

	t.Run("GetTags returns independent slice", func(t *testing.T) {
		label := NewLabel()
		label.Add("tag1")

		tags1 := label.GetTags()
		require.Len(t, tags1, 1)

		// Modify returned slice
		tags1 = append(tags1, "tag2")

		// Original label should not be affected
		tags2 := label.GetTags()
		assert.Len(t, tags2, 1, "Label internal state should not be affected")
	})
}
