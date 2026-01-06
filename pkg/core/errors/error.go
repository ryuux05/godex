package errors
import (
	"errors"
	"fmt"
	"strings"
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
	BlockNum uint64 `json:"blockNum"`
	BlockHash string `json:"blockHash"`
}

// Error we need to implement the Error function to follow the error interface
func (e *HTTPError) Error() string {
    return fmt.Sprintf("http error %d: %s", e.StatusCode, e.Message)
}

func (e *RPCError) Error() string {
    return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

func (e *ReorgError) Error() string {
	return fmt.Sprintf("reorg error at %d with hash %s", e.BlockNum, e.BlockHash)
}

// Is makes ReorgError work with Error.Is()
func (e *ReorgError) Is(target error) bool {
	return target == ErrReorgDetected
}

// IsRetryableError helper function to check if the error is retriable
func IsRetryableError(err error) bool {
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

	errStr := err.Error()
    if strings.Contains(errStr, "context deadline exceeded") ||
        strings.Contains(errStr, "connection refused") ||
        strings.Contains(errStr, "connection reset") ||
        strings.Contains(errStr, "no such host") ||
        strings.Contains(errStr, "i/o timeout") ||
        strings.Contains(errStr, "temporary failure") {
        return true
    }

	return false
}

func IsResponseTooBigError(err error) bool {
	var e *RPCError
	if !errors.As(err, &e) {
		return false
	}

	if e.Code == -32008 {
		return true
	}

	msg := strings.ToLower(e.Message)

	if strings.Contains(msg, "response is too big") {
		return true
	}

	return false
}

// ErrCursorNotFound is returned when a cursor does not exist for a chain.
// This is expected for clean databases starting fresh.
var ErrCursorNotFound = errors.New("cursor not found")

// ErrReorgDetected is returned when reorg detected
var ErrReorgDetected = errors.New("reorg detected")
