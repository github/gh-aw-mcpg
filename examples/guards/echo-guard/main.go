// Echo Guard - A simple guard that logs all inputs for debugging
//
// This guard prints all request data to help understand what information
// is available to guards during labeling decisions.
//
// Build with:
//
//	export GOROOT=$(~/go/bin/go1.23.4 env GOROOT)
//	tinygo build -o guard.wasm -target=wasi main.go
package main

import (
	"encoding/json"
	"fmt"

	sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"
)

func init() {
	sdk.RegisterLabelResource(labelResource)
	sdk.RegisterLabelResponse(labelResponse)
}

func labelResource(req *sdk.LabelResourceRequest) (*sdk.LabelResourceResponse, error) {
	// Log to gateway host using the new logging API
	sdk.LogInfo(fmt.Sprintf("label_resource called for tool: %s", req.ToolName))

	// Print the request for debugging (goes to WASM stdout)
	fmt.Println("=== label_resource called ===")
	fmt.Printf("Tool Name: %s\n", req.ToolName)
	fmt.Println("Tool Args:")
	prettyPrint(req.ToolArgs)
	if req.Capabilities != nil {
		fmt.Println("Capabilities:")
		prettyPrint(req.Capabilities)
	}
	fmt.Println("=============================")

	// Return a simple public resource label
	return &sdk.LabelResourceResponse{
		Resource:  sdk.NewPublicResource(fmt.Sprintf("echo:%s", req.ToolName)),
		Operation: sdk.OperationRead,
	}, nil
}

func labelResponse(req *sdk.LabelResponseRequest) (*sdk.LabelResponseResponse, error) {
	// Print the response for debugging
	fmt.Println("=== label_response called ===")
	fmt.Printf("Tool Name: %s\n", req.ToolName)
	fmt.Println("Tool Result:")
	prettyPrint(req.ToolResult)
	if req.Capabilities != nil {
		fmt.Println("Capabilities:")
		prettyPrint(req.Capabilities)
	}
	fmt.Println("=============================")

	// No fine-grained labeling
	return nil, nil
}

func prettyPrint(v interface{}) {
	data, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		fmt.Printf("  (error marshaling: %v)\n", err)
		return
	}
	fmt.Printf("  %s\n", string(data))
}

func main() {}
