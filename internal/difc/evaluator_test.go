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
		assert.Equal(t, "DIFC policy", filtered.FilterReason)
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
						Secrecy:     *NewSecrecyLabel([]Tag{"public"}...),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "item2",
					Labels: &LabeledResource{
						Description: "public item 2",
						Secrecy:     *NewSecrecyLabel([]Tag{"public"}...),
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
						Secrecy:     *NewSecrecyLabel([]Tag{"secret"}...),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "secret2",
					Labels: &LabeledResource{
						Description: "secret item 2",
						Secrecy:     *NewSecrecyLabel([]Tag{"topsecret"}...),
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
						Integrity:   *NewIntegrityLabel([]Tag{"trusted"}...),
					},
				},
				{
					Data: "high-integrity-target",
					Labels: &LabeledResource{
						Description: "high integrity",
						Secrecy:     *NewSecrecyLabel(),
						Integrity:   *NewIntegrityLabel([]Tag{"trusted", "verified", "certified"}...),
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
						Secrecy:     *NewSecrecyLabel([]Tag{"internal"}...),
						Integrity:   *NewIntegrityLabel([]Tag{"trusted", "audited"}...),
					},
				},
				{
					Data: "read-write",
					Labels: &LabeledResource{
						Description: "can read and write",
						Secrecy:     *NewSecrecyLabel([]Tag{"internal"}...),
						Integrity:   *NewIntegrityLabel([]Tag{"trusted"}...),
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
						Secrecy:     *NewSecrecyLabel([]Tag{"public"}...),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "internal-only",
					Labels: &LabeledResource{
						Description: "internal",
						Secrecy:     *NewSecrecyLabel([]Tag{"internal"}...),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "secret",
					Labels: &LabeledResource{
						Description: "secret",
						Secrecy:     *NewSecrecyLabel([]Tag{"secret"}...),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "low-integrity",
					Labels: &LabeledResource{
						Description: "low integrity",
						Secrecy:     *NewSecrecyLabel([]Tag{"public"}...),
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
				require.NoError(t, err)
				assert.Equal(t, tt.expected, mode)
			}
		})
	}
}

func TestDefaultEnforcementMode(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{name: "no env var returns strict", envValue: "", want: "strict"},
		{name: "env var strict", envValue: "strict", want: "strict"},
		{name: "env var filter", envValue: "filter", want: "filter"},
		{name: "env var propagate", envValue: "propagate", want: "propagate"},
		{name: "env var FILTER uppercase", envValue: "FILTER", want: "filter"},
		{name: "env var invalid falls back to strict", envValue: "invalid", want: "strict"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_GATEWAY_GUARDS_MODE", tt.envValue)
			assert.Equal(t, tt.want, DefaultEnforcementMode())
		})
	}
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

	t.Run("blocked integrity is a hard denial in propagate mode", func(t *testing.T) {
		eval := NewEvaluatorWithMode(EnforcementPropagate)
		for _, tag := range []string{
			"blocked:owner/repo",
			"integrity=blocked;scopes=owner/first,owner/second",
			"blocked",
		} {
			resource := NewLabeledResource("unclassified GraphQL subtree")
			resource.Integrity.Label.Add(Tag(tag))

			result := eval.Evaluate(
				NewSecrecyLabel(),
				NewIntegrityLabel("approved:owner/repo"),
				resource,
				OperationRead,
			)

			assert.False(t, result.IsAllowed(), "tag %q must be denied", tag)
			assert.Equal(t, AccessDeny, result.Decision, "tag %q", tag)
			assert.True(t, result.hardDeny, "tag %q", tag)
			assert.Empty(t, result.IntegrityToDrop, "tag %q", tag)
		}
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

// TestEvaluator_FilterCollection_DetectionFailureRate tests that FilterCollection
// accurately tracks items blocked by DIFC policy at realistic detection-failure
// rates.  The audit found ~26.7 % of workflow runs had detection failures (8 of
// 30 runs); this test uses equivalent proportions to confirm that
// GetFilteredCount and GetAccessibleCount remain consistent with TotalCount.
func TestEvaluator_FilterCollection_DetectionFailureRate(t *testing.T) {
	eval := NewEvaluator()
	assert.Equal(t, EnforcementStrict, eval.GetMode(), "evaluator must use strict mode for detection-failure tests")

	t.Run("8 of 30 items blocked mirrors 26.7% detection failure rate", func(t *testing.T) {
		// Agent has public clearance only — cannot access private-scoped items.
		agentSecrecy := NewSecrecyLabel([]Tag{"public"}...)
		agentIntegrity := NewIntegrityLabel()

		items := make([]LabeledItem, 30)
		for i := range items {
			secrecy := NewSecrecyLabel()
			if i < 22 {
				secrecy.Label.Add("public") // accessible
			} else {
				secrecy.Label.Add("private:restricted") // inaccessible (detection failure)
			}
			items[i] = LabeledItem{
				Data: i,
				Labels: &LabeledResource{
					Description: "workflow-run",
					Secrecy:     *secrecy,
					Integrity:   *NewIntegrityLabel(),
				},
			}
		}

		collection := &CollectionLabeledData{Items: items}
		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 30, filtered.TotalCount, "TotalCount must reflect all items before filtering")
		assert.Equal(t, 22, filtered.GetAccessibleCount(), "22 items should be accessible")
		assert.Equal(t, 8, filtered.GetFilteredCount(), "8 items (26.7%) should be filtered")
		assert.Equal(t, filtered.TotalCount,
			filtered.GetAccessibleCount()+filtered.GetFilteredCount(),
			"accessible + filtered must equal total")
	})

	t.Run("accessible+filtered always equals total for any filter rate", func(t *testing.T) {
		// Verify invariant holds across several different filter rates.
		rates := []struct{ total, blocked int }{
			{10, 0},
			{10, 5},
			{10, 10},
			{30, 8},
			{100, 27},
		}
		for _, r := range rates {
			agentSecrecy := NewSecrecyLabel()
			agentIntegrity := NewIntegrityLabel()

			items := make([]LabeledItem, r.total)
			for i := range items {
				secrecy := NewSecrecyLabel()
				if i < r.total-r.blocked {
					// accessible: resource has no secrecy restrictions
				} else {
					secrecy.Label.Add("private:blocked")
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

			assert.Equal(t, r.total, filtered.TotalCount)
			assert.Equal(t, filtered.TotalCount,
				filtered.GetAccessibleCount()+filtered.GetFilteredCount(),
				"invariant violated for total=%d blocked=%d", r.total, r.blocked)
		}
	})
}

// TestEvaluator_FilterCollection_FilteredItemsHaveReasons verifies that every item
// removed by DIFC enforcement carries a non-empty denial reason.  Audit trail
// continuity (recommendation #5 from the integrity audit) requires that audit
// agents can determine *why* each item was withheld.
func TestEvaluator_FilterCollection_FilteredItemsHaveReasons(t *testing.T) {
	eval := NewEvaluator()

	t.Run("secrecy violation reason is non-empty", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel() // no clearance
		agentIntegrity := NewIntegrityLabel()

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "private-issue",
					Labels: &LabeledResource{
						Description: "issue in private repo",
						Secrecy:     *NewSecrecyLabel([]Tag{"private:org/repo"}...),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		require.Equal(t, 1, filtered.GetFilteredCount(), "item should be filtered")
		assert.NotEmpty(t, filtered.Filtered[0].Reason,
			"filtered item must carry a denial reason for the audit trail")
		assert.True(t, filtered.Filtered[0].IsSecrecyViolation,
			"secrecy-blocked item must have IsSecrecyViolation=true")
	})

	t.Run("integrity violation reason is non-empty", func(t *testing.T) {
		// Agent has integrity "approved"; resource has no integrity → resource ⊄ agent
		// so the read is denied in strict mode.
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel([]Tag{"approved"}...)

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "untrusted-item",
					Labels: &LabeledResource{
						Description: "item with no integrity tags",
						Secrecy:     *NewSecrecyLabel(),
						Integrity:   *NewIntegrityLabel(), // empty → below agent's threshold
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		require.Equal(t, 1, filtered.GetFilteredCount(), "item should be filtered")
		assert.NotEmpty(t, filtered.Filtered[0].Reason,
			"filtered item must carry a denial reason for the audit trail")
		assert.False(t, filtered.Filtered[0].IsSecrecyViolation,
			"integrity-blocked item must have IsSecrecyViolation=false")
	})

	t.Run("every filtered item in a mixed collection has a reason", func(t *testing.T) {
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{Data: "accessible", Labels: &LabeledResource{Description: "open", Secrecy: *NewSecrecyLabel(), Integrity: *NewIntegrityLabel()}},
				{Data: "blocked-1", Labels: &LabeledResource{Description: "secret-1", Secrecy: *NewSecrecyLabel([]Tag{"private:a/b"}...), Integrity: *NewIntegrityLabel()}},
				{Data: "blocked-2", Labels: &LabeledResource{Description: "secret-2", Secrecy: *NewSecrecyLabel([]Tag{"private:c/d"}...), Integrity: *NewIntegrityLabel()}},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		assert.Equal(t, 2, filtered.GetFilteredCount())
		for i, detail := range filtered.Filtered {
			assert.NotEmpty(t, detail.Reason,
				"filtered item[%d] must have a non-empty denial reason", i)
			assert.True(t, detail.IsSecrecyViolation,
				"filtered item[%d] blocked by secrecy must have IsSecrecyViolation=true", i)
		}
	})

	t.Run("IsSecrecyViolation is false for integrity-only violation", func(t *testing.T) {
		// Agent requires approved integrity; item has none.
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel([]Tag{"approved:org/repo"}...)

		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: "low-integrity-item",
					Labels: &LabeledResource{
						Description: "unapproved PR",
						Secrecy:     *NewSecrecyLabel(),
						Integrity:   *NewIntegrityLabel(), // empty
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		require.Equal(t, 1, filtered.GetFilteredCount())
		assert.False(t, filtered.Filtered[0].IsSecrecyViolation,
			"integrity-only violation must not be marked as secrecy")
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

// TestNewComponents verifies that NewComponents initializes all DIFC components
// correctly and handles mode parsing, empty mode string, and invalid mode string.
func TestNewComponents(t *testing.T) {
	t.Run("empty mode string uses default", func(t *testing.T) {
		components, err := NewComponents("", EnforcementFilter)
		require.NoError(t, err)
		assert.Equal(t, EnforcementFilter, components.Mode)
		assert.NotNil(t, components.AgentRegistry)
		assert.NotNil(t, components.Capabilities)
		assert.NotNil(t, components.Evaluator)
	})

	t.Run("valid mode string overrides default", func(t *testing.T) {
		components, err := NewComponents("filter", EnforcementStrict)
		require.NoError(t, err)
		assert.Equal(t, EnforcementFilter, components.Mode)
		assert.NotNil(t, components.AgentRegistry)
		assert.NotNil(t, components.Capabilities)
		assert.NotNil(t, components.Evaluator)
	})

	t.Run("invalid mode string falls back to default and returns error", func(t *testing.T) {
		components, err := NewComponents("invalid-mode", EnforcementFilter)
		require.Error(t, err, "invalid mode string should return an error")
		assert.Equal(t, EnforcementFilter, components.Mode, "should fall back to default on parse error")
		assert.NotNil(t, components.AgentRegistry)
		assert.NotNil(t, components.Capabilities)
		assert.NotNil(t, components.Evaluator)
	})

	t.Run("propagate mode string", func(t *testing.T) {
		components, err := NewComponents("propagate", EnforcementStrict)
		require.NoError(t, err)
		assert.Equal(t, EnforcementPropagate, components.Mode)
		assert.NotNil(t, components.AgentRegistry)
		assert.NotNil(t, components.Capabilities)
		assert.NotNil(t, components.Evaluator)
	})

	t.Run("strict mode string explicit", func(t *testing.T) {
		components, err := NewComponents("strict", EnforcementFilter)
		require.NoError(t, err)
		assert.Equal(t, EnforcementStrict, components.Mode)
		assert.NotNil(t, components.AgentRegistry)
		assert.NotNil(t, components.Capabilities)
		assert.NotNil(t, components.Evaluator)
	})
}

// BenchmarkFilterCollection measures FilterCollection throughput for a large,
// mostly-accessible collection (the common case for list-returning tools such as
// list_issues/list_pull_requests/list_commits). It guards against a regression of
// the Accessible slice pre-allocation optimization, which avoids repeated append
// reallocations on this hot path.
func BenchmarkFilterCollection(b *testing.B) {
	eval := NewEvaluator()
	agentSecrecy := NewSecrecyLabel([]Tag{"public"}...)
	agentIntegrity := NewIntegrityLabel()

	const itemCount = 500
	items := make([]LabeledItem, itemCount)
	for i := 0; i < itemCount; i++ {
		items[i] = LabeledItem{
			Data: i,
			Labels: &LabeledResource{
				Description: "benchmark item",
				Secrecy:     *NewSecrecyLabel([]Tag{"public"}...),
				Integrity:   *NewIntegrityLabel(),
			},
		}
	}
	collection := &CollectionLabeledData{Items: items}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)
	}
}
