package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOperationType_String tests the String() method for OperationType
func TestOperationType_String(t *testing.T) {
	tests := []struct {
		name     string
		opType   OperationType
		expected string
	}{
		{
			name:     "OperationRead",
			opType:   OperationRead,
			expected: "read",
		},
		{
			name:     "OperationWrite",
			opType:   OperationWrite,
			expected: "write",
		},
		{
			name:     "OperationReadWrite",
			opType:   OperationReadWrite,
			expected: "read-write",
		},
		{
			name:     "Unknown operation type",
			opType:   OperationType(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.opType.String())
		})
	}
}

// TestAccessDecision_String tests the String() method for AccessDecision
func TestAccessDecision_String(t *testing.T) {
	tests := []struct {
		name     string
		decision AccessDecision
		expected string
	}{
		{
			name:     "AccessAllow",
			decision: AccessAllow,
			expected: "allow",
		},
		{
			name:     "AccessDeny",
			decision: AccessDeny,
			expected: "deny",
		},
		{
			name:     "Unknown access decision",
			decision: AccessDecision(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.decision.String())
		})
	}
}

// TestEvaluationResult_IsAllowed tests the IsAllowed() method
func TestEvaluationResult_IsAllowed(t *testing.T) {
	tests := []struct {
		name     string
		decision AccessDecision
		expected bool
	}{
		{
			name:     "Allow decision returns true",
			decision: AccessAllow,
			expected: true,
		},
		{
			name:     "Deny decision returns false",
			decision: AccessDeny,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &EvaluationResult{
				Decision: tt.decision,
			}
			assert.Equal(t, tt.expected, result.IsAllowed())
		})
	}
}

// TestNewEvaluator tests the evaluator constructor
func TestNewEvaluator(t *testing.T) {
	eval := NewEvaluator()
	require.NotNil(t, eval, "NewEvaluator should return non-nil evaluator")
}

// TestEvaluator_Evaluate_ReadWrite tests OperationReadWrite scenarios
func TestEvaluator_Evaluate_ReadWrite(t *testing.T) {
	eval := NewEvaluator()

	t.Run("ReadWrite allowed when both constraints satisfied", func(t *testing.T) {
		// Agent with matching secrecy and integrity
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("secret")
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")

		resource := NewLabeledResource("secure-file")
		resource.Secrecy.Label.Add("secret")
		resource.Integrity.Label.Add("trusted")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationReadWrite)

		assert.True(t, result.IsAllowed(), "ReadWrite should be allowed when both constraints satisfied")
		assert.Empty(t, result.SecrecyToAdd)
		assert.Empty(t, result.IntegrityToDrop)
	})

	t.Run("ReadWrite denied when read constraint fails", func(t *testing.T) {
		// Agent lacks secrecy tag needed to read
		// Note: Agent should NOT have integrity tags that resource doesn't have,
		// otherwise integrity check fails first (resource integrity must flow to agent)
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel() // No integrity requirements

		resource := NewLabeledResource("private-file")
		resource.Secrecy.Label.Add("private")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationReadWrite)

		assert.False(t, result.IsAllowed(), "ReadWrite should be denied when read constraint fails")
		assert.NotEmpty(t, result.SecrecyToAdd, "Should require secrecy tags to be added")
		assert.Contains(t, result.SecrecyToAdd, Tag("private"))
		assert.Contains(t, result.Reason, "secrecy")
	})

	t.Run("ReadWrite denied when write constraint fails", func(t *testing.T) {
		// Agent lacks integrity to write
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("production-db")
		resource.Integrity.Label.Add("production")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationReadWrite)

		// Read should pass (no resource secrecy requirements)
		// But write should fail
		assert.False(t, result.IsAllowed(), "ReadWrite should be denied when write constraint fails")
		assert.NotEmpty(t, result.IntegrityToDrop, "Should require integrity tags to be dropped")
		assert.Contains(t, result.Reason, "integrity")
	})

	t.Run("ReadWrite with multiple tags on both sides", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("secret")
		agentSecrecy.Label.Add("confidential")
		agentSecrecy.Label.Add("internal")

		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("production")
		agentIntegrity.Label.Add("verified")

		resource := NewLabeledResource("complex-resource")
		// For READ: agent secrecy ⊇ resource secrecy (agent has clearance)
		resource.Secrecy.Label.Add("secret")
		resource.Secrecy.Label.Add("confidential")
		resource.Secrecy.Label.Add("internal") // Resource must accept all agent secrecy for WRITE
		// For READ: resource integrity ⊇ agent integrity (resource is trusted enough)
		// For WRITE: agent integrity ⊇ resource integrity (agent is trusted enough)
		resource.Integrity.Label.Add("production")
		resource.Integrity.Label.Add("verified")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationReadWrite)

		assert.True(t, result.IsAllowed(), "ReadWrite should be allowed with matching tags")
	})

	t.Run("ReadWrite denied when both constraints fail", func(t *testing.T) {
		// Agent fails both read (secrecy) and write (integrity) checks
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("restricted-resource")
		resource.Secrecy.Label.Add("topsecret")
		resource.Integrity.Label.Add("verified")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationReadWrite)

		assert.False(t, result.IsAllowed(), "ReadWrite should be denied when both constraints fail")
		// Should fail on read constraint first (checked before write)
		assert.NotEmpty(t, result.SecrecyToAdd)
	})
}

// TestEvaluator_EvaluateRead_Comprehensive tests read scenarios comprehensively
func TestEvaluator_EvaluateRead_Comprehensive(t *testing.T) {
	eval := NewEvaluator()

	t.Run("Read denied due to integrity mismatch", func(t *testing.T) {
		// Agent requires high integrity but resource has low integrity
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")
		agentIntegrity.Label.Add("verified")

		resource := NewLabeledResource("untrusted-data")
		resource.Integrity.Label.Add("trusted") // Missing "verified"

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed(), "Read should be denied due to integrity mismatch")
		assert.NotEmpty(t, result.IntegrityToDrop, "Should identify missing integrity tags")
		assert.Contains(t, result.IntegrityToDrop, Tag("verified"))
		assert.Contains(t, result.Reason, "integrity")
		assert.Contains(t, result.Reason, "lower integrity")
	})

	t.Run("Read denied due to secrecy mismatch", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("public")
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("secret-file")
		resource.Secrecy.Label.Add("public")
		resource.Secrecy.Label.Add("secret") // Agent doesn't have "secret"

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed(), "Read should be denied due to secrecy mismatch")
		assert.NotEmpty(t, result.SecrecyToAdd, "Should identify missing secrecy tags")
		assert.Contains(t, result.SecrecyToAdd, Tag("secret"))
		assert.Contains(t, result.Reason, "secrecy")
	})

	t.Run("Read allowed with agent having superset of resource secrecy", func(t *testing.T) {
		// Agent has more secrecy tags than resource requires
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("public")
		agentSecrecy.Label.Add("secret")
		agentSecrecy.Label.Add("topsecret")
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("semi-secret-file")
		resource.Secrecy.Label.Add("public")
		resource.Secrecy.Label.Add("secret")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Read should be allowed when agent has superset")
	})

	t.Run("Read allowed with empty labels", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		resource := NewLabeledResource("public-resource")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Read should be allowed with empty labels")
	})

	t.Run("Read with multiple missing secrecy tags", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("highly-classified")
		resource.Secrecy.Label.Add("secret")
		resource.Secrecy.Label.Add("confidential")
		resource.Secrecy.Label.Add("restricted")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed())
		assert.Len(t, result.SecrecyToAdd, 3, "Should identify all missing tags")
		assert.Contains(t, result.SecrecyToAdd, Tag("secret"))
		assert.Contains(t, result.SecrecyToAdd, Tag("confidential"))
		assert.Contains(t, result.SecrecyToAdd, Tag("restricted"))
	})
}

// TestEvaluator_EvaluateWrite_Comprehensive tests write scenarios comprehensively
func TestEvaluator_EvaluateWrite_Comprehensive(t *testing.T) {
	eval := NewEvaluator()

	t.Run("Write denied due to agent secrecy exceeding resource", func(t *testing.T) {
		// Agent has secrecy tags that resource doesn't accept
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("secret")
		agentSecrecy.Label.Add("classified")
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("public-repo")
		resource.Secrecy.Label.Add("secret") // Doesn't accept "classified"

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed(), "Write should be denied when agent has extra secrecy")
		assert.NotEmpty(t, result.SecrecyToAdd, "Should identify extra secrecy tags")
		assert.Contains(t, result.SecrecyToAdd, Tag("classified"))
		assert.Contains(t, result.Reason, "secrecy")
	})

	t.Run("Write denied due to insufficient agent integrity", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("dev")

		resource := NewLabeledResource("production-db")
		resource.Integrity.Label.Add("dev")
		resource.Integrity.Label.Add("production") // Agent lacks this

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed(), "Write should be denied due to insufficient integrity")
		assert.NotEmpty(t, result.IntegrityToDrop, "Should identify missing integrity tags")
		assert.Contains(t, result.IntegrityToDrop, Tag("production"))
		assert.Contains(t, result.Reason, "integrity")
	})

	t.Run("Write allowed when agent integrity is superset", func(t *testing.T) {
		// Agent has more integrity tags than resource requires
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")
		agentIntegrity.Label.Add("verified")
		agentIntegrity.Label.Add("audited")

		resource := NewLabeledResource("secure-storage")
		resource.Integrity.Label.Add("trusted")
		resource.Integrity.Label.Add("verified")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.True(t, result.IsAllowed(), "Write should be allowed when agent has superset")
	})

	t.Run("Write with empty resource allows all", func(t *testing.T) {
		// Public resource with no restrictions
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("internal") // Agent has secrecy
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("public-output")
		// No labels = no restrictions

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed(), "Write should be denied when agent has secrecy that resource doesn't")
		// Agent with internal secrecy cannot write to completely public resource
	})

	t.Run("Write with matching empty secrecy", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")

		resource := NewLabeledResource("public-output")
		resource.Integrity.Label.Add("trusted")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.True(t, result.IsAllowed(), "Write should be allowed with matching empty secrecy")
	})

	t.Run("Write with multiple missing integrity tags", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("critical-system")
		resource.Integrity.Label.Add("verified")
		resource.Integrity.Label.Add("audited")
		resource.Integrity.Label.Add("certified")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed())
		assert.Len(t, result.IntegrityToDrop, 3, "Should identify all missing integrity tags")
	})
}

// TestEvaluator_FilterCollection_Advanced tests advanced collection filtering
func TestEvaluator_FilterCollection_Advanced(t *testing.T) {
	eval := NewEvaluator()

	t.Run("Filter empty collection", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		collection := &CollectionLabeledData{
			Items: []LabeledItem{},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 0, filtered.TotalCount)
		assert.Equal(t, 0, filtered.GetAccessibleCount())
		assert.Equal(t, 0, filtered.GetFilteredCount())
	})

	t.Run("Filter collection with all items accessible", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("public")
		agentIntegrity := NewIntegrityLabel()

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "item1",
					Labels: &LabeledResource{
						Description: "public item 1",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "item2",
					Labels: &LabeledResource{
						Description: "public item 2",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 2, filtered.TotalCount)
		assert.Equal(t, 2, filtered.GetAccessibleCount(), "All items should be accessible")
		assert.Equal(t, 0, filtered.GetFilteredCount())
	})

	t.Run("Filter collection with all items filtered", func(t *testing.T) {
		// Agent with no clearance
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "secret1",
					Labels: &LabeledResource{
						Description: "secret item 1",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"secret"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "secret2",
					Labels: &LabeledResource{
						Description: "secret item 2",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"topsecret"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 2, filtered.TotalCount)
		assert.Equal(t, 0, filtered.GetAccessibleCount(), "No items should be accessible")
		assert.Equal(t, 2, filtered.GetFilteredCount())
	})

	t.Run("Filter collection with OperationWrite", func(t *testing.T) {
		// Agent with high integrity
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")
		agentIntegrity.Label.Add("verified")

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "low-integrity-target",
					Labels: &LabeledResource{
						Description: "low integrity",
						Secrecy:     *NewSecrecyLabel(),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"trusted"}),
					},
				},
				{
					Data: "high-integrity-target",
					Labels: &LabeledResource{
						Description: "high integrity",
						Secrecy:     *NewSecrecyLabel(),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"trusted", "verified", "certified"}),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationWrite)

		assert.Equal(t, 2, filtered.TotalCount)
		assert.Equal(t, 1, filtered.GetAccessibleCount(), "Only low-integrity target should be writable")
		assert.Equal(t, 1, filtered.GetFilteredCount())
	})

	t.Run("Filter collection with OperationReadWrite", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("internal")
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "read-only",
					Labels: &LabeledResource{
						Description: "can read but not write",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"internal"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"trusted", "audited"}),
					},
				},
				{
					Data: "read-write",
					Labels: &LabeledResource{
						Description: "can read and write",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"internal"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"trusted"}),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationReadWrite)

		assert.Equal(t, 2, filtered.TotalCount)
		assert.Equal(t, 1, filtered.GetAccessibleCount(), "Only items satisfying both read and write should be accessible")
	})

	t.Run("Filter large collection", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("level1")
		agentSecrecy.Label.Add("level2")
		agentIntegrity := NewIntegrityLabel()

		// Create collection with 100 items
		items := make([]LabeledItem, 100)
		for i := 0; i < 100; i++ {
			secrecy := NewSecrecyLabel()
			// First 50 items accessible, next 50 not
			if i < 50 {
				secrecy.Label.Add("level1")
			} else {
				secrecy.Label.Add("level3") // Agent doesn't have level3
			}

			items[i] = LabeledItem{
				Data: i,
				Labels: &LabeledResource{
					Description: "item",
					Secrecy:     *secrecy,
					Integrity:   *NewIntegrityLabel(),
				},
			}
		}

		collection := &CollectionLabeledData{Items: items}
		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 100, filtered.TotalCount)
		assert.Equal(t, 50, filtered.GetAccessibleCount(), "First 50 items should be accessible")
		assert.Equal(t, 50, filtered.GetFilteredCount(), "Last 50 items should be filtered")
	})

	t.Run("Filter collection with mixed labels", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("public")
		agentSecrecy.Label.Add("internal")
		// Note: Agent should NOT have integrity requirements if we want to test secrecy filtering
		// on resources with no integrity tags (read check requires resource to have agent's integrity tags)
		agentIntegrity := NewIntegrityLabel()

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "public-only",
					Labels: &LabeledResource{
						Description: "public",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "internal-only",
					Labels: &LabeledResource{
						Description: "internal",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"internal"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "secret",
					Labels: &LabeledResource{
						Description: "secret",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"secret"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "low-integrity",
					Labels: &LabeledResource{
						Description: "low integrity",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 4, filtered.TotalCount)
		// Items 0, 1, and 3 should be accessible (agent has required secrecy tags)
		// Item 2 (secret) should be filtered (agent lacks "secret" secrecy tag)
		assert.Equal(t, 3, filtered.GetAccessibleCount(), "public, internal, and low-integrity should be accessible")
		assert.Equal(t, 1, filtered.GetFilteredCount(), "secret should be filtered")
	})
}

// TestEvaluator_EdgeCases tests edge cases and boundary conditions
func TestEvaluator_EdgeCases(t *testing.T) {
	eval := NewEvaluator()

	t.Run("Resource with many tags", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		for i := 0; i < 15; i++ {
			agentSecrecy.Label.Add(Tag("tag" + string(rune('0'+i))))
		}
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("complex-resource")
		for i := 0; i < 10; i++ {
			resource.Secrecy.Label.Add(Tag("tag" + string(rune('0'+i))))
		}

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Should handle many tags correctly")
	})

	t.Run("Agent with no tags reading resource with no tags", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		resource := NewLabeledResource("empty-resource")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed())
		assert.Empty(t, result.SecrecyToAdd)
		assert.Empty(t, result.IntegrityToDrop)
	})

	t.Run("Agent with no tags writing to resource with no tags", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		resource := NewLabeledResource("empty-resource")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.True(t, result.IsAllowed())
	})

	t.Run("Single tag mismatch on read", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("single-tag-resource")
		resource.Secrecy.Label.Add("x")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed())
		assert.Len(t, result.SecrecyToAdd, 1)
		assert.Contains(t, result.SecrecyToAdd, Tag("x"))
	})

	t.Run("Single tag mismatch on write", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("single-tag-resource")
		resource.Integrity.Label.Add("y")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed())
		assert.Len(t, result.IntegrityToDrop, 1)
		assert.Contains(t, result.IntegrityToDrop, Tag("y"))
	})
}

// TestEnforcementMode_String tests the String() method for EnforcementMode
func TestEnforcementMode_String(t *testing.T) {
	tests := []struct {
		name     string
		mode     EnforcementMode
		expected string
	}{
		{
			name:     "EnforcementStrict",
			mode:     EnforcementStrict,
			expected: "strict",
		},
		{
			name:     "EnforcementFilter",
			mode:     EnforcementFilter,
			expected: "filter",
		},
		{
			name:     "EnforcementPropagate",
			mode:     EnforcementPropagate,
			expected: "propagate",
		},
		{
			name:     "Unknown mode",
			mode:     EnforcementMode(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.mode.String())
		})
	}
}

// TestParseEnforcementMode tests parsing enforcement mode strings
func TestParseEnforcementMode(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    EnforcementMode
		expectError bool
	}{
		{name: "strict", input: "strict", expected: EnforcementStrict},
		{name: "filter", input: "filter", expected: EnforcementFilter},
		{name: "propagate", input: "propagate", expected: EnforcementPropagate},
		{name: "empty string defaults to strict", input: "", expected: EnforcementStrict},
		{name: "STRICT (case insensitive)", input: "STRICT", expected: EnforcementStrict},
		{name: "Propagate (case insensitive)", input: "Propagate", expected: EnforcementPropagate},
		{name: "unknown mode", input: "invalid", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, err := ParseEnforcementMode(tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, mode)
			}
		})
	}
}

// TestNewEvaluatorWithMode tests creating evaluator with specific mode
func TestNewEvaluatorWithMode(t *testing.T) {
	t.Run("creates evaluator with strict mode", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementStrict)
		assert.Equal(t, EnforcementStrict, eval.GetMode())
	})

	t.Run("creates evaluator with propagate mode", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)
		assert.Equal(t, EnforcementPropagate, eval.GetMode())
	})

	t.Run("SetMode changes mode", func(t *testing.T) {
		eval := NewEvaluator()
		assert.Equal(t, EnforcementStrict, eval.GetMode())
		eval.SetMode(EnforcementPropagate)
		assert.Equal(t, EnforcementPropagate, eval.GetMode())
	})
}

// TestEvaluationResult_RequiresPropagation tests the RequiresPropagation method
func TestEvaluationResult_RequiresPropagation(t *testing.T) {
	tests := []struct {
		name     string
		decision AccessDecision
		expected bool
	}{
		{name: "Allow does not require propagation", decision: AccessAllow, expected: false},
		{name: "Deny does not require propagation", decision: AccessDeny, expected: false},
		{name: "AllowWithPropagate requires propagation", decision: AccessAllowWithPropagate, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &EvaluationResult{Decision: tt.decision}
			assert.Equal(t, tt.expected, result.RequiresPropagation())
		})
	}
}

// TestAccessDecision_String_AllowWithPropagate tests the new decision type
func TestAccessDecision_String_AllowWithPropagate(t *testing.T) {
	assert.Equal(t, "allow-with-propagate", AccessAllowWithPropagate.String())
}

// TestEvaluator_PropagateMode_Read tests propagate mode read behavior
func TestEvaluator_PropagateMode_Read(t *testing.T) {
	t.Run("read with missing secrecy tags - propagate mode allows and marks tags to add", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)

		// Agent has no secrecy tags
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		// Resource has secret tag
		resource := NewLabeledResource("secret-file")
		resource.Secrecy.Label.Add("secret")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Read should be allowed in propagate mode")
		assert.True(t, result.RequiresPropagation(), "Should require propagation")
		assert.Equal(t, AccessAllowWithPropagate, result.Decision)
		assert.Contains(t, result.SecrecyToAdd, Tag("secret"), "Should indicate secret tag needs to be added")
		assert.Empty(t, result.IntegrityToDrop, "No integrity changes needed")
	})

	t.Run("read with missing integrity tags on resource - propagate mode allows and marks tags to drop", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)

		// Agent has high integrity
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")
		agentIntegrity.Label.Add("verified")

		// Resource has no integrity (untrusted)
		resource := NewLabeledResource("untrusted-file")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Read should be allowed in propagate mode")
		assert.True(t, result.RequiresPropagation(), "Should require propagation")
		assert.Equal(t, AccessAllowWithPropagate, result.Decision)
		assert.Empty(t, result.SecrecyToAdd, "No secrecy changes needed")
		assert.Contains(t, result.IntegrityToDrop, Tag("trusted"), "Should indicate trusted tag needs to be dropped")
		assert.Contains(t, result.IntegrityToDrop, Tag("verified"), "Should indicate verified tag needs to be dropped")
	})

	t.Run("read with both secrecy and integrity violations - propagate mode allows with both changes", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)

		// Agent has integrity but no secrecy clearance
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")

		// Resource is secret and untrusted
		resource := NewLabeledResource("secret-untrusted-file")
		resource.Secrecy.Label.Add("classified")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Read should be allowed in propagate mode")
		assert.True(t, result.RequiresPropagation(), "Should require propagation")
		assert.Contains(t, result.SecrecyToAdd, Tag("classified"), "Should add classified secrecy tag")
		assert.Contains(t, result.IntegrityToDrop, Tag("trusted"), "Should drop trusted integrity tag")
	})

	t.Run("read with no violations - propagate mode allows without propagation", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)

		// Agent has matching labels
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("secret")
		agentIntegrity := NewIntegrityLabel()

		// Resource matches agent's capabilities
		resource := NewLabeledResource("accessible-file")
		resource.Secrecy.Label.Add("secret")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.True(t, result.IsAllowed(), "Read should be allowed")
		assert.False(t, result.RequiresPropagation(), "Should NOT require propagation when labels match")
		assert.Equal(t, AccessAllow, result.Decision)
	})
}

// TestEvaluator_PropagateMode_Write tests that writes are still blocked in propagate mode
func TestEvaluator_PropagateMode_Write(t *testing.T) {
	t.Run("write to public resource by agent with secret - still blocked", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)

		// Agent has secret data
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("secret")
		agentIntegrity := NewIntegrityLabel()

		// Public resource (no secrecy)
		resource := NewLabeledResource("public-internet")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed(), "Write should be BLOCKED even in propagate mode")
		assert.Equal(t, AccessDeny, result.Decision)
		assert.Contains(t, result.Reason, "secrecy")
	})

	t.Run("write to high-integrity resource by low-integrity agent - still blocked", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)

		// Agent has no integrity
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		// Resource requires high integrity
		resource := NewLabeledResource("production-database")
		resource.Integrity.Label.Add("production")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed(), "Write should be BLOCKED even in propagate mode")
		assert.Equal(t, AccessDeny, result.Decision)
		assert.Contains(t, result.Reason, "integrity")
	})
}

// TestNewEmptyEvaluationResult tests the newEmptyEvaluationResult helper
func TestNewEmptyEvaluationResult(t *testing.T) {
	t.Run("creates result with default values", func(t *testing.T) {
		result := newEmptyEvaluationResult()
		require.NotNil(t, result)
		assert.Equal(t, AccessAllow, result.Decision)
		assert.NotNil(t, result.SecrecyToAdd)
		assert.Empty(t, result.SecrecyToAdd)
		assert.NotNil(t, result.IntegrityToDrop)
		assert.Empty(t, result.IntegrityToDrop)
		assert.Empty(t, result.Reason)
	})

	t.Run("multiple calls create independent results", func(t *testing.T) {
		result1 := newEmptyEvaluationResult()
		result2 := newEmptyEvaluationResult()

		// Modify result1
		result1.Decision = AccessDeny
		result1.SecrecyToAdd = append(result1.SecrecyToAdd, Tag("test"))

		// result2 should be unaffected
		assert.Equal(t, AccessAllow, result2.Decision)
		assert.Empty(t, result2.SecrecyToAdd)
	})
}

// TestEvaluator_StrictMode_Read_Unchanged verifies strict mode still denies reads
func TestEvaluator_StrictMode_Read_Unchanged(t *testing.T) {
	eval := NewEvaluator() // Default is strict mode

	t.Run("read with missing secrecy - denied in strict mode", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("secret-file")
		resource.Secrecy.Label.Add("secret")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed(), "Read should be denied in strict mode")
		assert.Equal(t, AccessDeny, result.Decision)
	})

	t.Run("read with missing integrity - denied in strict mode", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")

		resource := NewLabeledResource("untrusted-file")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed(), "Read should be denied in strict mode")
		assert.Equal(t, AccessDeny, result.Decision)
	})
}
