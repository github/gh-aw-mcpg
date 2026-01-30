package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabelOperations(t *testing.T) {
	t.Run("SecrecyLabel flow checks", func(t *testing.T) {
		// Create labels
		l1 := NewSecrecyLabel()
		l1.Label.Add("tag1")
		l1.Label.Add("tag2")

		l2 := NewSecrecyLabel()
		l2.Label.Add("tag1")
		l2.Label.Add("tag2")
		l2.Label.Add("tag3")

		// l1 should flow to l2 (l1 ⊆ l2)
		assert.True(t, l1.CanFlowTo(l2), "Expected l1 to flow to l2")

		// l2 should NOT flow to l1 (l2 has extra tags)
		assert.False(t, l2.CanFlowTo(l1), "Expected l2 NOT to flow to l1")
	})

	t.Run("IntegrityLabel flow checks", func(t *testing.T) {
		// Create labels
		l1 := NewIntegrityLabel()
		l1.Label.Add("trust1")
		l1.Label.Add("trust2")

		l2 := NewIntegrityLabel()
		l2.Label.Add("trust1")

		// l1 should flow to l2 (l1 ⊇ l2)
		assert.True(t, l1.CanFlowTo(l2), "Expected l1 to flow to l2")

		// l2 should NOT flow to l1 (l2 missing trust2)
		assert.False(t, l2.CanFlowTo(l1), "Expected l2 NOT to flow to l1")
	})

	t.Run("Empty labels flow to everything", func(t *testing.T) {
		empty := NewSecrecyLabel()
		withTags := NewSecrecyLabel()
		withTags.Label.Add("tag1")

		// Empty should flow to anything
		assert.True(t, empty.CanFlowTo(withTags), "Expected empty to flow to withTags")

		// withTags should NOT flow to empty
		assert.False(t, withTags.CanFlowTo(empty), "Expected withTags NOT to flow to empty")
	})
}

func TestEvaluator(t *testing.T) {
	eval := NewEvaluator()

	t.Run("Read operation - secrecy check", func(t *testing.T) {
		// Agent with no secrecy tags tries to read data with secrecy requirements
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("private-file")
		resource.Secrecy.Label.Add("private")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		assert.False(t, result.IsAllowed(), "Expected access to be denied for read with insufficient secrecy")

		assert.False(t, len(result.SecrecyToAdd) == 0, "Expected SecrecyToAdd to contain required tags")
	})

	t.Run("Read operation - allowed with matching labels", func(t *testing.T) {
		// Agent with secrecy tag can read data with that tag
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("private")
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("private-file")
		resource.Secrecy.Label.Add("private")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)

		if !result.IsAllowed() {
			t.Errorf("Expected access to be allowed: %s", result.Reason)
		}
	})

	t.Run("Write operation - integrity check", func(t *testing.T) {
		// Agent without integrity tries to write to high-integrity resource
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("production-database")
		resource.Integrity.Label.Add("production")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		assert.False(t, result.IsAllowed(), "Expected access to be denied for write with insufficient integrity")

		assert.False(t, len(result.IntegrityToDrop) == 0, "Expected IntegrityToDrop to contain required tags")
	})

	t.Run("Write operation - allowed with matching integrity", func(t *testing.T) {
		// Agent with production integrity can write to production resource
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("production")

		resource := NewLabeledResource("production-database")
		resource.Integrity.Label.Add("production")

		result := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		if !result.IsAllowed() {
			t.Errorf("Expected access to be allowed: %s", result.Reason)
		}
	})

	t.Run("Empty resource allows all operations", func(t *testing.T) {
		// NoopGuard returns empty labels - should allow everything
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()

		resource := NewLabeledResource("noop-resource")
		// No tags added = no restrictions

		// Both read and write should be allowed
		readResult := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationRead)
		writeResult := eval.Evaluate(agentSecrecy, agentIntegrity, resource, OperationWrite)

		if !readResult.IsAllowed() {
			t.Errorf("Expected read to be allowed for empty resource: %s", readResult.Reason)
		}
		if !writeResult.IsAllowed() {
			t.Errorf("Expected write to be allowed for empty resource: %s", writeResult.Reason)
		}
	})
}

func TestFormatViolationError(t *testing.T) {
	t.Run("returns nil for allowed access", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:        AccessAllow,
			SecrecyToAdd:    []Tag{},
			IntegrityToDrop: []Tag{},
			Reason:          "",
		}
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		resource := NewLabeledResource("test-resource")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NoError(t, err, "Expected nil error for allowed access")
	})

	t.Run("formats secrecy violation error", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:        AccessDeny,
			SecrecyToAdd:    []Tag{"private", "confidential"},
			IntegrityToDrop: []Tag{},
			Reason:          "Resource has secrecy requirements that agent doesn't meet",
		}
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("trusted")

		resource := NewLabeledResource("private-file")
		resource.Secrecy.Label.Add("private")
		resource.Secrecy.Label.Add("confidential")
		resource.Integrity.Label.Add("trusted")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error for denied access")

		errMsg := err.Error()
		// Check that the reason is included
		assert.Contains(t, errMsg, "DIFC Violation:", "Expected DIFC Violation prefix")
		assert.Contains(t, errMsg, "Resource has secrecy requirements", "Expected reason text")

		// Check secrecy tags section
		assert.Contains(t, errMsg, "Required Action: Add secrecy tags", "Expected secrecy action")
		assert.Contains(t, errMsg, "Implications of adding secrecy tags:", "Expected implications header")
		assert.Contains(t, errMsg, "Agent will be restricted from writing", "Expected restriction warning")
		assert.Contains(t, errMsg, "public resources", "Expected public resources mention")
		assert.Contains(t, errMsg, "handling sensitive information", "Expected sensitivity warning")

		// Check current labels section
		assert.Contains(t, errMsg, "Current Agent Labels:", "Expected current labels header")
		assert.Contains(t, errMsg, "Secrecy:", "Expected secrecy label")
		assert.Contains(t, errMsg, "Integrity:", "Expected integrity label")

		// Check resource requirements section
		assert.Contains(t, errMsg, "Resource Requirements:", "Expected resource requirements header")
	})

	t.Run("formats integrity violation error", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:        AccessDeny,
			SecrecyToAdd:    []Tag{},
			IntegrityToDrop: []Tag{"production", "verified"},
			Reason:          "Agent lacks required integrity to write to resource",
		}
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("internal")
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("production")
		agentIntegrity.Label.Add("verified")

		resource := NewLabeledResource("production-database")
		resource.Secrecy.Label.Add("internal")
		resource.Integrity.Label.Add("production")
		resource.Integrity.Label.Add("verified")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error for denied access")

		errMsg := err.Error()
		// Check that the reason is included
		assert.Contains(t, errMsg, "DIFC Violation:", "Expected DIFC Violation prefix")
		assert.Contains(t, errMsg, "Agent lacks required integrity", "Expected reason text")

		// Check integrity tags section
		assert.Contains(t, errMsg, "Required Action: Drop integrity tags", "Expected integrity action")
		assert.Contains(t, errMsg, "Implications of dropping integrity tags:", "Expected implications header")
		assert.Contains(t, errMsg, "no longer be able to write to high-integrity resources", "Expected restriction warning")
		assert.Contains(t, errMsg, "influenced by lower-integrity data", "Expected influence warning")
		assert.Contains(t, errMsg, "outputs will be considered less trustworthy", "Expected trust warning")

		// Check current labels section
		assert.Contains(t, errMsg, "Current Agent Labels:", "Expected current labels header")

		// Check resource requirements section
		assert.Contains(t, errMsg, "Resource Requirements:", "Expected resource requirements header")
	})

	t.Run("formats combined secrecy and integrity violation", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:        AccessDeny,
			SecrecyToAdd:    []Tag{"secret"},
			IntegrityToDrop: []Tag{"high-trust"},
			Reason:          "Multiple constraint violations detected",
		}
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("high-trust")

		resource := NewLabeledResource("complex-resource")
		resource.Secrecy.Label.Add("secret")
		resource.Integrity.Label.Add("high-trust")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error for denied access")

		errMsg := err.Error()
		// Check that both sections are present
		assert.Contains(t, errMsg, "Required Action: Add secrecy tags", "Expected secrecy action")
		assert.Contains(t, errMsg, "Required Action: Drop integrity tags", "Expected integrity action")
		assert.Contains(t, errMsg, "Implications of adding secrecy tags:", "Expected secrecy implications")
		assert.Contains(t, errMsg, "Implications of dropping integrity tags:", "Expected integrity implications")
	})

	t.Run("formats error with empty agent labels", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:     AccessDeny,
			SecrecyToAdd: []Tag{"private"},
			Reason:       "Empty agent cannot access private resource",
		}
		agentSecrecy := NewSecrecyLabel()     // Empty
		agentIntegrity := NewIntegrityLabel() // Empty

		resource := NewLabeledResource("private-resource")
		resource.Secrecy.Label.Add("private")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error")

		errMsg := err.Error()
		// Empty labels should still be displayed
		assert.Contains(t, errMsg, "Current Agent Labels:", "Expected labels section")
		assert.Contains(t, errMsg, "Secrecy:", "Expected secrecy label")
		assert.Contains(t, errMsg, "Integrity:", "Expected integrity label")
	})

	t.Run("formats error with multiple tags", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:        AccessDeny,
			SecrecyToAdd:    []Tag{"tag1", "tag2", "tag3"},
			IntegrityToDrop: []Tag{"int1", "int2"},
			Reason:          "Complex multi-tag violation",
		}
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("existing-secrecy")
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("int1")
		agentIntegrity.Label.Add("int2")
		agentIntegrity.Label.Add("int3")

		resource := NewLabeledResource("multi-tag-resource")
		resource.Secrecy.Label.AddAll([]Tag{"tag1", "tag2", "tag3", "existing-secrecy"})
		resource.Integrity.Label.Add("int3")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error")

		errMsg := err.Error()
		// Verify all tags are mentioned
		assert.Contains(t, errMsg, "tag1", "Expected tag1 in error")
		assert.Contains(t, errMsg, "tag2", "Expected tag2 in error")
		assert.Contains(t, errMsg, "tag3", "Expected tag3 in error")
		assert.Contains(t, errMsg, "int1", "Expected int1 in error")
		assert.Contains(t, errMsg, "int2", "Expected int2 in error")
	})

	t.Run("error message structure is complete", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:        AccessDeny,
			SecrecyToAdd:    []Tag{"s1"},
			IntegrityToDrop: []Tag{"i1"},
			Reason:          "Test violation",
		}
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("s0")
		agentIntegrity := NewIntegrityLabel()
		agentIntegrity.Label.Add("i0")
		agentIntegrity.Label.Add("i1")

		resource := NewLabeledResource("test-resource")
		resource.Secrecy.Label.Add("s0")
		resource.Secrecy.Label.Add("s1")
		resource.Integrity.Label.Add("i0")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error")

		errMsg := err.Error()

		// Verify the complete structure of the error message
		// 1. Violation header
		assert.Contains(t, errMsg, "DIFC Violation: Test violation", "Expected violation header")

		// 2. Secrecy implications (5 lines expected)
		assert.Contains(t, errMsg, "restricted from writing to resources that lack these tags", "Expected restriction line")
		assert.Contains(t, errMsg, "includes public resources", "Expected public resources line")
		assert.Contains(t, errMsg, "marked as handling sensitive information", "Expected sensitivity line")
		assert.Contains(t, errMsg, "Future writes must target resources with tags:", "Expected future writes line")

		// 3. Integrity implications (4 lines expected)
		assert.Contains(t, errMsg, "no longer be able to write to high-integrity resources", "Expected high-integrity line")
		assert.Contains(t, errMsg, "Specifically, agent cannot write to resources requiring tags:", "Expected specific tags line")
		assert.Contains(t, errMsg, "acknowledges that agent has been influenced", "Expected influence line")
		assert.Contains(t, errMsg, "outputs will be considered less trustworthy", "Expected trust line")

		// 4. Current labels section
		assert.Contains(t, errMsg, "Current Agent Labels:", "Expected current labels header")

		// 5. Resource requirements section
		assert.Contains(t, errMsg, "Resource Requirements:", "Expected requirements header")
	})

	t.Run("single tag in violation", func(t *testing.T) {
		result := &EvaluationResult{
			Decision:     AccessDeny,
			SecrecyToAdd: []Tag{"single-tag"},
			Reason:       "Single tag violation",
		}
		agentSecrecy := NewSecrecyLabel()
		agentIntegrity := NewIntegrityLabel()
		resource := NewLabeledResource("single-tag-resource")
		resource.Secrecy.Label.Add("single-tag")

		err := FormatViolationError(result, agentSecrecy, agentIntegrity, resource)
		assert.NotNil(t, err, "Expected non-nil error")

		errMsg := err.Error()
		assert.Contains(t, errMsg, "single-tag", "Expected single-tag in error message")
		assert.Contains(t, errMsg, "DIFC Violation: Single tag violation", "Expected reason in error")
	})
}

func TestAgentRegistry(t *testing.T) {
	registry := NewAgentRegistry()

	t.Run("GetOrCreate creates new agent", func(t *testing.T) {
		agent := registry.GetOrCreate("agent-1")
		if agent.AgentID != "agent-1" {
			t.Errorf("Expected agent ID to be 'agent-1', got %s", agent.AgentID)
		}

		// Should have empty labels initially
		assert.True(t, agent.Secrecy.Label.IsEmpty(), "Expected new agent to have empty secrecy labels")
		assert.True(t, agent.Integrity.Label.IsEmpty(), "Expected new agent to have empty integrity labels")
	})

	t.Run("GetOrCreate returns existing agent", func(t *testing.T) {
		agent1 := registry.GetOrCreate("agent-2")
		agent1.Secrecy.Label.Add("secret")

		agent2 := registry.GetOrCreate("agent-2")
		assert.Equal(t, agent2, agent1, "to get same agent instance")

		assert.True(t, agent2.Secrecy.Label.Contains("secret"), "Expected agent to retain added tags")
	})

	t.Run("AccumulateFromRead is a no-op (labels immutable after init)", func(t *testing.T) {
		agent := registry.GetOrCreate("agent-3")

		// Record initial state
		initialSecrecyCount := len(agent.Secrecy.Label.GetTags())
		initialIntegrityCount := len(agent.Integrity.Label.GetTags())

		resource := NewLabeledResource("data-source")
		resource.Secrecy.Label.Add("confidential")
		resource.Integrity.Label.Add("verified")

		// AccumulateFromRead should be a no-op
		agent.AccumulateFromRead(resource)

		// Labels should be unchanged
		assert.Equal(t, initialSecrecyCount, len(agent.Secrecy.Label.GetTags()), "Secrecy labels should not change")
		assert.Equal(t, initialIntegrityCount, len(agent.Integrity.Label.GetTags()), "Integrity labels should not change")
		assert.False(t, agent.Secrecy.Label.Contains("confidential"), "Agent should NOT gain secrecy tag from read")
		assert.False(t, agent.Integrity.Label.Contains("verified"), "Agent should NOT gain integrity tag from read")
	})
}

func TestCollectionFiltering(t *testing.T) {
	eval := NewEvaluator()

	t.Run("FilterCollection filters inaccessible items", func(t *testing.T) {
		// Agent with limited clearance
		agentSecrecy := NewSecrecyLabel()
		agentSecrecy.Label.Add("public")
		agentIntegrity := NewIntegrityLabel()

		// Create collection with mixed access
		collection := &CollectionLabeledData{
			Items: []LabeledItem{
				{
					Data: map[string]string{"name": "public-item"},
					Labels: &LabeledResource{
						Description: "public item",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: map[string]string{"name": "secret-item"},
					Labels: &LabeledResource{
						Description: "secret item",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"secret"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
		}

		filtered := eval.FilterCollection(agentSecrecy, agentIntegrity, collection, OperationRead)

		if filtered.GetAccessibleCount() != 1 {
			t.Errorf("Expected 1 accessible item, got %d", filtered.GetAccessibleCount())
		}
		if filtered.GetFilteredCount() != 1 {
			t.Errorf("Expected 1 filtered item, got %d", filtered.GetFilteredCount())
		}
	})
}
