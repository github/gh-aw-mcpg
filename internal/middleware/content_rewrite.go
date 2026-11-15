package middleware

import (
	"encoding/json"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func rewriteFilteredTextPayload(result *sdk.CallToolResult, data any, filteredText string) (*sdk.CallToolResult, any) {
	logMiddleware.Printf("rewriteFilteredTextPayload: rewriting first text content item, filteredLen=%d", len(filteredText))
	rewrittenResult := mcp.NewTextCallToolResult(filteredText, result.IsError)
	rewrittenResult.Meta = result.Meta
	if len(result.Content) > 1 {
		rewrittenResult.Content = append(rewrittenResult.Content, result.Content[1:]...)
	}

	if rewrittenData, ok := rewriteEnvelopeTextPayload(data, filteredText); ok {
		return rewrittenResult, rewrittenData
	}

	var filteredPayload any
	if err := json.Unmarshal([]byte(filteredText), &filteredPayload); err == nil {
		return rewrittenResult, filteredPayload
	}
	logger.LogWarn("payload", "Failed to unmarshal filtered text payload for rewritten data, returning original backing data")

	return rewrittenResult, data
}

func rewriteEnvelopeTextPayload(data any, filteredText string) (any, bool) {
	switch v := data.(type) {
	case map[string]any:
		contentValue, ok := v["content"]
		if !ok {
			logMiddleware.Print("rewriteEnvelopeTextPayload: no content key in map, skipping envelope rewrite")
			return nil, false
		}
		rewrittenMap := util.ShallowCloneMap(v)

		rewrittenContent, ok := rewriteFirstContentItem(contentValue, filteredText)
		if !ok {
			logMiddleware.Printf("rewriteEnvelopeTextPayload: failed to rewrite first content item (contentType=%T)", contentValue)
			return nil, false
		}
		logMiddleware.Printf("rewriteEnvelopeTextPayload: envelope rewrite complete, filteredLen=%d", len(filteredText))
		rewrittenMap["content"] = rewrittenContent
		return rewrittenMap, true
	default:
		logMiddleware.Printf("rewriteEnvelopeTextPayload: unsupported data type %T, skipping", data)
		return nil, false
	}
}

func rewriteFirstContentItem(contentValue any, filteredText string) (any, bool) {
	switch content := contentValue.(type) {
	case []map[string]any:
		if len(content) == 0 {
			logMiddleware.Print("rewriteFirstContentItem: content is empty slice of maps")
			return nil, false
		}
		rewrittenContent := append([]map[string]any(nil), content...)
		rewrittenContent[0] = rewriteContentItemText(rewrittenContent[0], filteredText)
		return rewrittenContent, true
	case []any:
		if len(content) == 0 {
			logMiddleware.Print("rewriteFirstContentItem: content is empty slice of any")
			return nil, false
		}
		rewrittenContent := append([]any(nil), content...)
		firstItem, ok := rewrittenContent[0].(map[string]any)
		if !ok {
			logMiddleware.Printf("rewriteFirstContentItem: first content item is not map[string]any, got %T", rewrittenContent[0])
			return nil, false
		}
		rewrittenContent[0] = rewriteContentItemText(firstItem, filteredText)
		return rewrittenContent, true
	default:
		return nil, false
	}
}

func rewriteContentItemText(contentItem map[string]any, filteredText string) map[string]any {
	rewrittenItem := util.ShallowCloneMap(contentItem)
	rewrittenItem["text"] = filteredText
	return rewrittenItem
}
