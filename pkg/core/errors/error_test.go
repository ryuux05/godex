package errors

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRetryableError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"rate limit", &HTTPError{StatusCode: 429}, true},
		{"server error", &HTTPError{StatusCode: 500}, true},
		{"gateway", &HTTPError{StatusCode: 502}, true},
		{"unavailable", &HTTPError{StatusCode: 503}, true},
		{"timeout", &HTTPError{StatusCode: 504}, true},
		{"client error", &HTTPError{StatusCode: 400}, false},
		{"unauthorized", &HTTPError{StatusCode: 401}, false},
		{"wrapped HTTP", fmt.Errorf("fetch: %w", &HTTPError{StatusCode: 503}), true},
		{"RPC lower boundary", &RPCError{Code: -32099}, true},
		{"RPC upper boundary", &RPCError{Code: -32000}, true},
		{"RPC below boundary", &RPCError{Code: -32100}, false},
		{"invalid params", &RPCError{Code: -32602}, false},
		{"wrapped RPC", fmt.Errorf("fetch: %w", &RPCError{Code: -32001}), true},
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, false},
		{"connection refused", errors.New("dial: connection refused"), true},
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"DNS", errors.New("no such host"), true},
		{"IO timeout", errors.New("i/o timeout"), true},
		{"temporary failure", errors.New("temporary failure"), true},
		{"permanent", errors.New("invalid request"), false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, IsRetryableError(tc.err)) })
	}
}

func TestIsResponseTooBigError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"code", &RPCError{Code: -32008}, true},
		{"message", &RPCError{Code: -32000, Message: "RESPONSE IS TOO BIG"}, true},
		{"wrapped", fmt.Errorf("fetch: %w", &RPCError{Code: -32008}), true},
		{"other RPC", &RPCError{Code: -32000, Message: "unavailable"}, false},
		{"plain text", errors.New("response is too big"), false},
		{"HTTP", &HTTPError{StatusCode: 413}, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, IsResponseTooBigError(tc.err)) })
	}
}

func TestErrorContracts(t *testing.T) {
	assert.EqualError(t, &HTTPError{StatusCode: 503, Message: "unavailable"}, "http error 503: unavailable")
	assert.EqualError(t, &RPCError{Code: -32008, Message: "too big"}, "rpc error -32008: too big")
	err := &ReorgError{BlockNum: 12, BlockHash: "0xabc"}
	assert.EqualError(t, err, "reorg error at 12 with hash 0xabc")
	assert.ErrorIs(t, fmt.Errorf("process: %w", err), ErrReorgDetected)
	assert.NotErrorIs(t, err, ErrCursorNotFound)
}
