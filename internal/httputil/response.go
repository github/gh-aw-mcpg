package httputil

import (
	"fmt"
	"io"
	"net/http"
)

// ReadResponseBody reads the full body from an HTTP response, closes it, and
// checks the status code. If the status code is >= 400, it returns an error
// using the provided context string. The response body is always closed before
// returning.
//
// This helper deduplicates the common pattern of defer Body.Close() + io.ReadAll
// + status-code check that appears in proxy, githubhttp, and similar call sites.
func ReadResponseBody(resp *http.Response, context string) ([]byte, error) {
	logHTTP.Printf("ReadResponseBody: context=%s", context)
	body, err := readResponseBody(resp, context, "ReadResponseBody")
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		logHTTP.Printf("ReadResponseBody: HTTP error response: context=%s, status=%d, bodyLen=%d", context, resp.StatusCode, len(body))
		return nil, fmt.Errorf("%s returned HTTP %d", context, resp.StatusCode)
	}

	logHTTP.Printf("ReadResponseBody: success: context=%s, status=%d, bodyLen=%d", context, resp.StatusCode, len(body))
	return body, nil
}

// ReadResponseBodyStrict reads the full body from an HTTP response, closes it,
// and checks that the status code equals expectedStatus exactly. If the status
// code does not match, it returns an error that includes the response body for
// diagnostics. The response body is always closed before returning.
//
// This variant is retained as a test hook for exact status matching and body
// diagnostics; production callers use ReadResponseBody.
func ReadResponseBodyStrict(resp *http.Response, expectedStatus int, context string) ([]byte, error) {
	logHTTP.Printf("ReadResponseBodyStrict: context=%s, expectedStatus=%d", context, expectedStatus)
	body, err := readResponseBody(resp, context, "ReadResponseBodyStrict")
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != expectedStatus {
		logHTTP.Printf("ReadResponseBodyStrict: unexpected status: context=%s, got=%d, expected=%d, bodyLen=%d", context, resp.StatusCode, expectedStatus, len(body))
		return nil, fmt.Errorf("%s returned HTTP %d: %s", context, resp.StatusCode, string(body))
	}

	logHTTP.Printf("ReadResponseBodyStrict: success: context=%s, status=%d, bodyLen=%d", context, resp.StatusCode, len(body))
	return body, nil
}

func readResponseBody(resp *http.Response, context, operation string) ([]byte, error) {
	if resp == nil {
		logHTTP.Printf("%s: nil response for context=%s", operation, context)
		return nil, fmt.Errorf("failed to read %s response: nil response", context)
	}
	if resp.Body == nil {
		logHTTP.Printf("%s: nil body for context=%s, status=%d", operation, context, resp.StatusCode)
		return nil, fmt.Errorf("failed to read %s response: response body is nil", context)
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if readErr != nil {
		logHTTP.Printf("%s: body read failed for context=%s, status=%d, err=%v", operation, context, resp.StatusCode, readErr)
		return nil, fmt.Errorf("failed to read %s response: %w", context, readErr)
	}
	if closeErr != nil {
		logHTTP.Printf("%s: body close failed for context=%s, status=%d, err=%v", operation, context, resp.StatusCode, closeErr)
		return nil, fmt.Errorf("failed to close %s response body: %w", context, closeErr)
	}

	return body, nil
}
