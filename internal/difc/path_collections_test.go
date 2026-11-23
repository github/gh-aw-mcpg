package difc

import (
	"encoding/json"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/mcpresult"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathCollectionsPreserveEnvelope(t *testing.T) {
	const fixture = `{"pageInfo":{"hasNextPage":true,"endCursor":"next"},"totalCount":2,"review_threads":[
		{"id":"first","is_resolved":true,"is_outdated":true,"total_count":3,"comments":[{"body":"trusted bot"},{"body":"owner reply"},{"body":"untrusted"}]},
		{"id":"second","is_resolved":false,"is_outdated":false,"comments":[{"body":"unlabeled"}]},
		{"id":"empty","comments":[]}]}`
	for _, format := range []string{"plain", "text", "structured", "both"} {
		t.Run(format, func(t *testing.T) {
			var original interface{}
			require.NoError(t, json.Unmarshal([]byte(fixture), &original))
			if format != "plain" {
				envelope := map[string]interface{}{"isError": false}
				if format != "structured" {
					envelope["content"] = []interface{}{map[string]interface{}{"type": "text", "text": fixture}}
				}
				if format != "text" {
					envelope["structuredContent"] = original
				}
				original = envelope
			}
			before, err := json.Marshal(original)
			require.NoError(t, err)
			labels := &PathLabels{
				ItemsPaths: []string{"/review_threads/0/comments", "/review_threads/1/comments", "/review_threads/2/comments"},
				DefaultLabels: &PathLabelEntry{
					Integrity: []string{"none:public"},
				},
				LabeledPaths: []PathLabel{
					{Path: "/review_threads/0/comments/0", Labels: PathLabelEntry{Integrity: []string{"approved:public"}}},
					{Path: "/review_threads/0/comments/1", Labels: PathLabelEntry{Integrity: []string{"approved:public"}}},
					{Path: "/review_threads/0/comments/2", Labels: PathLabelEntry{Integrity: []string{"none:public"}}},
				},
			}
			data, err := NewPathLabeledData(original, labels)
			require.NoError(t, err)
			require.Len(t, data.GetItems(), 4)
			filtered, err := FilterAndConvertLabeledData(
				NewEvaluator(), NewSecrecyLabel(), NewIntegrityLabel("approved:public"),
				OperationRead, data.ToCollectionLabeledData(), EnforcementFilter,
			)
			require.NoError(t, err)
			require.NotNil(t, filtered.Filtered)
			assert.Equal(t, 2, filtered.Filtered.GetFilteredCount())
			var result interface{}
			if format == "plain" {
				result = filtered.FinalResult
			} else {
				envelope := filtered.FinalResult.(map[string]interface{})
				assert.False(t, envelope["isError"].(bool))
				if format != "structured" {
					content, ok := mcpresult.NormalizeContentItems(envelope["content"])
					require.True(t, ok)
					require.Len(t, content, 1)
					require.NoError(t, json.Unmarshal([]byte(mcpresult.ExtractTextContent(envelope)), &result))
					unwrapped, wrapped := unwrapMCPResponse(envelope)
					assert.True(t, wrapped)
					assert.Equal(t, result, unwrapped)
				}
				if format != "text" {
					if result != nil {
						assert.Equal(t, result, envelope["structuredContent"])
					}
					result = envelope["structuredContent"]
				}
			}
			got, err := json.Marshal(result)
			require.NoError(t, err)
			assert.JSONEq(t, `{"pageInfo":{"hasNextPage":true,"endCursor":"next"},"totalCount":2,"review_threads":[
				{"id":"first","is_resolved":true,"is_outdated":true,"total_count":3,"comments":[{"body":"trusted bot"},{"body":"owner reply"}]},
				{"id":"second","is_resolved":false,"is_outdated":false,"comments":[]},
				{"id":"empty","comments":[]}]}`, string(got))
			after, err := json.Marshal(original)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "filtering must not mutate backend data")

			strict, err := FilterAndConvertLabeledData(
				NewEvaluator(), NewSecrecyLabel(), NewIntegrityLabel("approved:public"),
				OperationRead, data.ToCollectionLabeledData(), EnforcementStrict,
			)
			require.NoError(t, err)
			assert.True(t, strict.Blocked)
			assert.Nil(t, strict.FinalResult)
		})
	}
}

func TestPullRequestArtifactPathsPreserveFinalMCPResponses(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]interface{}
		labels      *PathLabels
		wantPayload string
	}{
		{
			name: "nested list",
			payload: map[string]interface{}{
				"list": map[string]interface{}{
					"artifacts": []interface{}{
						map[string]interface{}{"id": 1, "body": "blocked"},
						map[string]interface{}{"id": 2, "body": "visible"},
					},
					"page": 1,
				},
			},
			labels: &PathLabels{
				ItemsPaths:    []string{"/list/artifacts"},
				DefaultLabels: &PathLabelEntry{Integrity: []string{"none:public"}},
				LabeledPaths: []PathLabel{
					{Path: "/list/artifacts/0", Labels: PathLabelEntry{Integrity: []string{"blocked:public"}}},
					{Path: "/list/artifacts/1", Labels: PathLabelEntry{Integrity: []string{"none:public"}}},
				},
			},
			wantPayload: `{"list":{"artifacts":[{"body":"visible","id":2}],"page":1}}`,
		},
		{
			name: "nested upsert result",
			payload: map[string]interface{}{
				"artifact": map[string]interface{}{"id": 1, "body": "blocked"},
				"message":  "upserted",
			},
			labels: &PathLabels{
				ItemsPaths:    []string{},
				DefaultLabels: &PathLabelEntry{Integrity: []string{"none:public"}},
				LabeledPaths: []PathLabel{
					{Path: "/artifact", Labels: PathLabelEntry{Integrity: []string{"blocked:public"}}},
				},
			},
			wantPayload: `{"artifact":null,"message":"upserted"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, err := json.Marshal(test.payload)
			require.NoError(t, err)
			original := map[string]interface{}{
				"isError": false,
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": string(text)},
				},
				"structuredContent": test.payload,
			}
			data, err := NewPathLabeledData(original, test.labels)
			require.NoError(t, err)

			filtered, err := FilterAndConvertLabeledData(
				NewEvaluator(), NewSecrecyLabel(), NewIntegrityLabel(),
				OperationRead, data.ToCollectionLabeledData(), EnforcementFilter,
			)
			require.NoError(t, err)
			envelope, ok := filtered.FinalResult.(map[string]interface{})
			require.True(t, ok)
			assert.False(t, envelope["isError"].(bool))
			assert.JSONEq(t, test.wantPayload, mcpresult.ExtractTextContent(envelope))
			structured, err := json.Marshal(envelope["structuredContent"])
			require.NoError(t, err)
			assert.JSONEq(t, test.wantPayload, string(structured))
		})
	}
}

func TestPathCollectionsRejectInvalidCoverage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{"missing path", []string{"/missing"}},
		{"not an array", []string{"/items/0"}},
		{"duplicate", []string{"/items", "/items"}},
		{"overlap", []string{"/items", "/items/0/nested"}},
		{"relative", []string{"items"}},
		{"non-canonical index", []string{"/items/00/nested"}},
		{"invalid escape", []string{"/item~2"}},
		{"trailing slash", []string{"/items/"}},
		{"unlabeled", []string{"/items"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPathLabeledData(map[string]interface{}{
				"items": []interface{}{map[string]interface{}{"nested": []interface{}{}}},
			}, &PathLabels{ItemsPaths: tc.paths})
			require.Error(t, err)
		})
	}
}

func TestFlatPathLabelsStructuredContent(t *testing.T) {
	const text = `{"items":[{"body":"trusted"},{"body":"untrusted"}]}`
	for _, format := range []string{"structured", "both"} {
		t.Run(format, func(t *testing.T) {
			var structured interface{}
			require.NoError(t, json.Unmarshal([]byte(text), &structured))
			response := map[string]interface{}{"structuredContent": structured}
			if format == "both" {
				response["content"] = []interface{}{map[string]interface{}{"type": "text", "text": text}}
			}

			data, err := NewPathLabeledData(response, &PathLabels{
				ItemsPath: "/items",
				LabeledPaths: []PathLabel{
					{Path: "/items/0", Labels: PathLabelEntry{Integrity: []string{"approved:public"}}},
				},
				DefaultLabels: &PathLabelEntry{Integrity: []string{"none:public"}},
			})
			require.NoError(t, err)
			filtered, err := FilterAndConvertLabeledData(
				NewEvaluator(), NewSecrecyLabel(), NewIntegrityLabel("approved:public"),
				OperationRead, data.ToCollectionLabeledData(), EnforcementFilter,
			)
			require.NoError(t, err)
			assert.Equal(t, map[string]interface{}{
				"items": []interface{}{map[string]interface{}{"body": "trusted"}},
			}, filtered.FinalResult)
		})
	}
}

func TestPathCollectionsParentAccess(t *testing.T) {
	for _, parentAllowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied parent", true: "allowed parent"}[parentAllowed], func(t *testing.T) {
			const fixture = `{"data":{"pullRequest":{"number":42,"reviews":{"nodes":[{"id":"allowed"},{"id":"denied"}]}}}}`
			var original interface{}
			require.NoError(t, json.Unmarshal([]byte(fixture), &original))
			parentIntegrity := []string{"none:public"}
			if parentAllowed {
				parentIntegrity = []string{"approved:public"}
			}
			data, err := NewPathLabeledData(original, &PathLabels{
				ItemsPaths:    []string{"/data/pullRequest/reviews/nodes"},
				DefaultLabels: &PathLabelEntry{Integrity: []string{"none:public"}},
				LabeledPaths: []PathLabel{
					{Path: "/data/pullRequest", Labels: PathLabelEntry{Integrity: parentIntegrity}},
					{Path: "/data/pullRequest/reviews/nodes/0", Labels: PathLabelEntry{Integrity: []string{"approved:public"}}},
				},
			})
			require.NoError(t, err)
			result, err := FilterAndConvertLabeledData(
				NewEvaluator(), NewSecrecyLabel(), NewIntegrityLabel("approved:public"),
				OperationRead, data.ToCollectionLabeledData(), EnforcementFilter,
			)
			require.NoError(t, err)
			got, err := json.Marshal(result.FinalResult)
			require.NoError(t, err)
			if parentAllowed {
				assert.JSONEq(t, `{"data":{"pullRequest":{"number":42,"reviews":{"nodes":[{"id":"allowed"}]}}}}`, string(got))
			} else {
				assert.JSONEq(t, `{"data":{"pullRequest":null}}`, string(got))
			}
			unchanged, err := json.Marshal(original)
			require.NoError(t, err)
			assert.JSONEq(t, fixture, string(unchanged))
		})
	}
}

func TestPathCollectionsOverallIntersectsIntegrity(t *testing.T) {
	const fixture = `{"data":{"pullRequest":{"number":42,"reviews":{"nodes":[{"body":"unapproved"}]}}}}`
	var original interface{}
	require.NoError(t, json.Unmarshal([]byte(fixture), &original))

	data, err := NewPathLabeledData(original, &PathLabels{
		ItemsPaths: []string{"/data/pullRequest/reviews/nodes"},
		LabeledPaths: []PathLabel{
			{Path: "/data/pullRequest", Labels: PathLabelEntry{Integrity: []string{"none:public", "unapproved:public", "approved:public"}}},
			{Path: "/data/pullRequest/reviews/nodes/0", Labels: PathLabelEntry{Integrity: []string{"none:public", "unapproved:public"}}},
		},
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, []Tag{"none:public", "unapproved:public"}, data.Overall().Integrity.Label.GetTags())
}
