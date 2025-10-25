package core

import (
	"errors"
	"fmt"
)

type HTTPError struct {
	StatusCode int `json:"statusCode"`
	Message string `json:"message"`
}

type RPCError struct  {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ReorgError struct {

}

// We need to implement the Error function to follow the error interface
func (e *HTTPError) Error() string {
    return fmt.Sprintf("http error %d: %s", e.StatusCode, e.Message)
}

func (e *RPCError) Error() string {
    return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Helper function to check if the error is retriable
func isRetryableError(err error) bool {
	// Try to extract HTTPError
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
        // Retryable HTTP status codes
        switch httpErr.StatusCode {
        case 429, 502, 503, 504:
            return true
        case 500, 501, 505, 506, 507, 508, 510, 511:
            return true // 5xx server errors
        default:
            return false // 4xx client errors, 2xx success
        }
    }

	// Try to extract RPCError
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		// rpcErr now contains the actual error
		if rpcErr.Code >= -32099 && rpcErr.Code <= -32000 {
			return true
		}
	}

	return false
}