package guard

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

var logRemote = logger.New("guard:remote")

// RemoteGuard implements Guard interface by delegating to a remote MCP server
// The remote server exposes guard/label_resource and guard/label_response tools
type RemoteGuard struct {
	name       string
	connection *mcp.Connection
}

// NewRemoteGuard creates a new remote guard that communicates via MCP
func NewRemoteGuard(name string, connection *mcp.Connection) *RemoteGuard {
	logRemote.Printf("Creating remote guard: name=%s", name)
	return &RemoteGuard{
		name:       name,
		connection: connection,
	}
}

// Name returns the identifier for this guard
func (g *RemoteGuard) Name() string {
	return g.name
}

// LabelResource delegates to the remote guard's label_resource tool
// This implements Option B (Gateway-Proxied Metadata) from the DIFC proposal
func (g *RemoteGuard) LabelResource(ctx context.Context, toolName string, args interface{}, backend BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	logRemote.Printf("LabelResource called: toolName=%s", toolName)

	// Prepare arguments for the remote guard
	guardArgs := map[string]interface{}{
		"tool_name": toolName,
		"tool_args": args,
	}

	// Add agent capabilities if provided
	if caps != nil {
		guardArgs["capabilities"] = caps
	}

	// Call the remote guard's label_resource tool
	result, err := g.connection.SendRequest("tools/call", map[string]interface{}{
		"name":      "guard/label_resource",
		"arguments": guardArgs,
	})
	if err != nil {
		logRemote.Printf("Error calling remote guard label_resource: %v", err)
		return nil, difc.OperationWrite, fmt.Errorf("remote guard error: %w", err)
	}

	// Check for RPC error
	if result.Error != nil {
		logRemote.Printf("Guard returned error: %s", result.Error.Message)
		return nil, difc.OperationWrite, fmt.Errorf("guard error: %s", result.Error.Message)
	}

	// Parse the response
	// The response format follows the DIFC proposal section 11.7.5
	var response map[string]interface{}
	if err := json.Unmarshal(result.Result, &response); err != nil {
		return nil, difc.OperationWrite, fmt.Errorf("failed to unmarshal guard response: %w", err)
	}

	// Check if guard needs metadata (two-phase protocol)
	status, _ := response["status"].(string)
	if status == "need_metadata" {
		logRemote.Print("Guard requested metadata, fetching...")

		// Extract metadata requests
		requests, ok := response["requests"].([]interface{})
		if !ok {
			return nil, difc.OperationWrite, fmt.Errorf("invalid metadata requests format")
		}

		// Fetch metadata using backend caller
		metadata := make(map[string]interface{})
		for _, req := range requests {
			reqMap, ok := req.(map[string]interface{})
			if !ok {
				continue
			}

			reqID, _ := reqMap["id"].(string)
			reqTool, _ := reqMap["tool"].(string)
			reqArgs, _ := reqMap["args"]

			if reqID == "" || reqTool == "" {
				logRemote.Printf("Invalid metadata request: %+v", reqMap)
				continue
			}

			logRemote.Printf("Fetching metadata: id=%s, tool=%s", reqID, reqTool)

			// Call backend with privilege (bypasses DIFC)
			metadataResult, err := backend.CallTool(ctx, reqTool, reqArgs)
			if err != nil {
				logRemote.Printf("Error fetching metadata for %s: %v", reqID, err)
				// Continue with other requests
				metadata[reqID] = map[string]interface{}{"error": err.Error()}
			} else {
				metadata[reqID] = metadataResult
			}
		}

		// Call guard again with metadata
		guardArgs["metadata"] = metadata
		result, err = g.connection.SendRequest("tools/call", map[string]interface{}{
			"name":      "guard/label_resource",
			"arguments": guardArgs,
		})
		if err != nil {
			return nil, difc.OperationWrite, fmt.Errorf("remote guard error (phase 2): %w", err)
		}

		// Check for RPC error
		if result.Error != nil {
			return nil, difc.OperationWrite, fmt.Errorf("guard error (phase 2): %s", result.Error.Message)
		}

		if err := json.Unmarshal(result.Result, &response); err != nil {
			return nil, difc.OperationWrite, fmt.Errorf("failed to unmarshal guard response (phase 2): %w", err)
		}

		status, _ = response["status"].(string)
	}

	// Status should now be "complete"
	if status != "complete" {
		return nil, difc.OperationWrite, fmt.Errorf("unexpected guard status: %s", status)
	}

	// Extract labeled resource
	resourceData, ok := response["resource"].(map[string]interface{})
	if !ok {
		return nil, difc.OperationWrite, fmt.Errorf("invalid resource format in guard response")
	}

	// Parse operation type
	operation := difc.OperationWrite // default to most restrictive
	if opStr, ok := response["operation"].(string); ok {
		switch opStr {
		case "read":
			operation = difc.OperationRead
		case "write":
			operation = difc.OperationWrite
		case "read-write":
			operation = difc.OperationReadWrite
		}
	}

	// Parse the labeled resource
	resource, err := parseLabeledResource(resourceData)
	if err != nil {
		return nil, operation, fmt.Errorf("failed to parse labeled resource: %w", err)
	}

	logRemote.Printf("LabelResource complete: operation=%s, description=%s", operation, resource.Description)
	return resource, operation, nil
}

// LabelResponse delegates to the remote guard's label_response tool
func (g *RemoteGuard) LabelResponse(ctx context.Context, toolName string, result interface{}, backend BackendCaller, caps *difc.Capabilities) (difc.LabeledData, error) {
	logRemote.Printf("LabelResponse called: toolName=%s", toolName)

	// Prepare arguments for the remote guard
	guardArgs := map[string]interface{}{
		"tool_name":   toolName,
		"tool_result": result,
	}

	// Add agent capabilities if provided
	if caps != nil {
		guardArgs["capabilities"] = caps
	}

	// Call the remote guard's label_response tool
	responseData, err := g.connection.SendRequest("tools/call", map[string]interface{}{
		"name":      "guard/label_response",
		"arguments": guardArgs,
	})
	if err != nil {
		logRemote.Printf("Error calling remote guard label_response: %v", err)
		return nil, fmt.Errorf("remote guard error: %w", err)
	}

	// Check for RPC error
	if responseData.Error != nil {
		logRemote.Printf("Guard returned error: %s", responseData.Error.Message)
		return nil, fmt.Errorf("guard error: %s", responseData.Error.Message)
	}

	// If the guard returns empty result, it means no fine-grained labeling
	if len(responseData.Result) == 0 {
		logRemote.Print("Guard returned empty result, no fine-grained labeling")
		return nil, nil
	}

	// Parse the labeled response
	// The format depends on whether it's a collection or single item
	var responseMap map[string]interface{}
	if err := json.Unmarshal(responseData.Result, &responseMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal guard response: %w", err)
	}

	// Check if it's a collection
	if items, ok := responseMap["items"].([]interface{}); ok {
		return parseCollectionLabeledData(items)
	}

	// If no fine-grained labeling specified, return nil
	// The reference monitor will use the resource labels from LabelResource
	return nil, nil
}

// parseLabeledResource converts a map to a LabeledResource
func parseLabeledResource(data map[string]interface{}) (*difc.LabeledResource, error) {
	resource := &difc.LabeledResource{}

	// Parse description
	if desc, ok := data["description"].(string); ok {
		resource.Description = desc
	}

	// Parse secrecy tags
	if secrecy, ok := data["secrecy"].([]interface{}); ok {
		tags := make([]difc.Tag, 0, len(secrecy))
		for _, t := range secrecy {
			if tagStr, ok := t.(string); ok {
				tags = append(tags, difc.Tag(tagStr))
			}
		}
		resource.Secrecy = *difc.NewSecrecyLabelWithTags(tags)
	} else {
		resource.Secrecy = *difc.NewSecrecyLabel()
	}

	// Parse integrity tags
	if integrity, ok := data["integrity"].([]interface{}); ok {
		tags := make([]difc.Tag, 0, len(integrity))
		for _, t := range integrity {
			if tagStr, ok := t.(string); ok {
				tags = append(tags, difc.Tag(tagStr))
			}
		}
		resource.Integrity = *difc.NewIntegrityLabelWithTags(tags)
	} else {
		resource.Integrity = *difc.NewIntegrityLabel()
	}

	// Parse structure (optional nested resources)
	if structure, ok := data["structure"].(map[string]interface{}); ok && len(structure) > 0 {
		// Convert the generic map to ResourceStructure
		resourceStruct := &difc.ResourceStructure{
			Fields: make(map[string]*difc.FieldLabels),
		}
		// For now, just store the raw structure
		// A full implementation would parse the nested labels
		resource.Structure = resourceStruct
	}

	return resource, nil
}

// parseCollectionLabeledData converts an array of items to CollectionLabeledData
func parseCollectionLabeledData(items []interface{}) (*difc.CollectionLabeledData, error) {
	collection := &difc.CollectionLabeledData{
		Items: make([]difc.LabeledItem, 0, len(items)),
	}

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		labeledItem := difc.LabeledItem{
			Data: itemMap["data"],
		}

		// Parse labels
		if labelsData, ok := itemMap["labels"].(map[string]interface{}); ok {
			labels, err := parseLabeledResource(labelsData)
			if err != nil {
				return nil, err
			}
			labeledItem.Labels = labels
		}

		collection.Items = append(collection.Items, labeledItem)
	}

	return collection, nil
}
