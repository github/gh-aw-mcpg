// Sample DIFC Guard implemented in JavaScript
// This demonstrates that JavaScript guards are easier than Go guards:
// - No TinyGo requirement
// - Works with any wazero version
// - Native WASM support
// - Easy to compile and use

// Host function imports (provided by gateway via wazero)
// Note: These are imported automatically by the WASM runtime
//
// Available host functions:
// - call_backend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize) -> int32
// - host_log(level, msgPtr, msgLen) -> void
//
// Log levels: 0=debug, 1=info, 2=warn, 3=error

const LOG_DEBUG = 0;
const LOG_INFO = 1;
const LOG_WARN = 2;
const LOG_ERROR = 3;

// Helper function to log messages to the gateway host
function logToHost(level, message) {
    const msgBytes = new TextEncoder().encode(message);
    // Allocate memory for the message (simplified - in real use, use proper WASM memory allocation)
    const ptr = allocateMemory(msgBytes.length);
    new Uint8Array(memory.buffer, ptr, msgBytes.length).set(msgBytes);
    host_log(level, ptr, msgBytes.length);
}

// Guard function: label_resource
// Called before accessing a resource to determine its DIFC labels
function label_resource(inputPtr, inputLen, outputPtr, outputSize) {
    try {
        // Read input JSON from WASM memory
        const inputBytes = new Uint8Array(memory.buffer, inputPtr, inputLen);
        const inputStr = new TextDecoder().decode(inputBytes);
        const input = JSON.parse(inputStr);
        
        // Log the incoming request (if host_log is available)
        if (typeof host_log !== 'undefined') {
            logToHost(LOG_DEBUG, `label_resource called for tool: ${input.tool_name}`);
        }
        
        // Default labels
        const output = {
            resource: {
                description: `resource:${input.tool_name}`,
                secrecy: ["public"],
                integrity: ["untrusted"]
            },
            operation: "read"
        };
        
        // Label based on tool name
        switch (input.tool_name) {
            case "create_issue":
            case "update_issue":
            case "create_pull_request":
                output.operation = "write";
                output.resource.integrity = ["contributor"];
                break;
                
            case "merge_pull_request":
                output.operation = "read-write";
                output.resource.integrity = ["maintainer"];
                break;
                
            case "list_issues":
            case "get_issue":
            case "list_pull_requests":
                output.operation = "read";
                output.resource.secrecy = ["public"];
                break;
        }
        
        // Write output JSON
        const outputStr = JSON.stringify(output);
        const outputBytes = new TextEncoder().encode(outputStr);
        
        if (outputBytes.length > outputSize) {
            return -1; // Output too large
        }
        
        new Uint8Array(memory.buffer, outputPtr, outputBytes.length).set(outputBytes);
        return outputBytes.length;
    } catch (e) {
        return -1; // Error
    }
}

// Guard function: label_response
// Called after a backend call to label response data
function label_response(inputPtr, inputLen, outputPtr, outputSize) {
    try {
        // For this sample, we don't do fine-grained labeling
        return 0;
    } catch (e) {
        return -1;
    }
}
