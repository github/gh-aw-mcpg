// Package httputil provides shared HTTP helper utilities used across multiple
// HTTP-facing packages (server, proxy, etc.).
package httputil

import (
	"encoding/json"
	"net/http"
)

// WriteJSONResponse sets the Content-Type header, writes the status code, and encodes
// body as JSON. It centralises the three-line pattern used across HTTP handlers.
func WriteJSONResponse(w http.ResponseWriter, statusCode int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	w.Write(data)
}
