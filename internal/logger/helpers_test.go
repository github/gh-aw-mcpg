package logger

import "log/slog"

func LogRPCRequest(direction RPCMessageDirection, serverID, method string, payload []byte, agentSecrecy, agentIntegrity []string) {
	LogRPCRequestForSession(direction, serverID, method, payload, agentSecrecy, agentIntegrity, false)
}

func LogRPCResponse(direction RPCMessageDirection, serverID string, payload []byte, err error, agentSecrecy, agentIntegrity []string) {
	LogRPCResponseForSession(direction, serverID, payload, err, agentSecrecy, agentIntegrity, false)
}

func LogRPCMessage(info *RPCMessageInfo) {
	LogDebug("rpc", "%s", formatRPCMessage(info))
	logRPCMarkdown(info)
}

func LogRPCMessageJSONL(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, payloadBytes []byte, err error) {
	LogRPCMessageJSONLWithTags(direction, messageType, serverID, method, payloadBytes, err, nil, nil)
}

func NewSlogLogger(namespace string) *slog.Logger {
	return NewSlogLoggerWithHandler(New(namespace))
}
