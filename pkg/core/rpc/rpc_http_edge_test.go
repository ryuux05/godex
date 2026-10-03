package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	coreerrors "github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func rpcWithResponse(body string) *HTTPRPC {
	r := NewHTTPRPC("http://rpc.test", 0, 0)
	r.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return r
}

func TestGetBlocksOutOfOrderResponses(t *testing.T) {
	r := rpcWithResponse(`[{"id":2,"result":{"hash":"third"}},{"id":0,"result":{"hash":"first"}},{"id":1,"result":{"hash":"second"}}]`)
	blocks, err := r.GetBlocks(context.Background(), []string{"0x1", "0x2", "0x3"})
	require.NoError(t, err)
	assert.Equal(t, "first", blocks["0x1"].Hash)
	assert.Equal(t, "second", blocks["0x2"].Hash)
	assert.Equal(t, "third", blocks["0x3"].Hash)
}

func TestGetBlocksDuplicateResponseID(t *testing.T) {
	r := rpcWithResponse(`[{"id":0,"result":{"hash":"first"}},{"id":0,"result":{"hash":"duplicate"}}]`)
	blocks, err := r.GetBlocks(context.Background(), []string{"0x1", "0x2"})
	assert.ErrorContains(t, err, "duplicate id 0")
	assert.Nil(t, blocks)
}

func TestRPCErrorPreservesCodeAndData(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			body := `{"id":0,"error":{"code":-32000,"message":"temporarily unavailable","data":{"provider":"busy"}}}`
			if batch {
				body = "[" + body + "]"
			}
			r := rpcWithResponse(body)
			var err error
			if batch {
				_, err = r.GetBlocks(context.Background(), []string{"0x1"})
			} else {
				_, err = r.Head(context.Background())
			}
			var rpcErr *coreerrors.RPCError
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, -32000, rpcErr.Code)
			assert.Equal(t, map[string]any{"provider": "busy"}, rpcErr.Data)
			assert.True(t, coreerrors.IsRetryableError(err), "batch errors must retain their retry classification")
		})
	}
}

func TestRPCRequestEnvelope(t *testing.T) {
	r := NewHTTPRPC("http://rpc.test", 0, 0)
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
		var envelope struct {
			JSONRPC string            `json:"jsonrpc"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&envelope))
		assert.Equal(t, "2.0", envelope.JSONRPC)
		assert.Equal(t, "eth_getLogs", envelope.Method)
		require.Len(t, envelope.Params, 1)
		var filter types.Filter
		require.NoError(t, json.Unmarshal(envelope.Params[0], &filter))
		assert.Equal(t, types.Filter{FromBlock: "0x1", ToBlock: "0x2", Address: []string{"0xabc"}, Topics: [][]string{{"event"}, nil, {"owner"}}}, filter)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":1,"result":[]}`))}, nil
	})
	_, err := r.GetLogs(context.Background(), types.Filter{FromBlock: "0x1", ToBlock: "0x2", Address: []string{"0xabc"}, Topics: [][]string{{"event"}, nil, {"owner"}}})
	require.NoError(t, err)
}

func TestRPCInvalidEndpointAndJSON(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, badEndpoint := range []bool{false, true} {
			r := rpcWithResponse("not JSON")
			if badEndpoint {
				r.endpoint = ":invalid"
			}
			var err error
			if batch {
				_, err = r.GetBlocks(context.Background(), []string{"0x1"})
			} else {
				_, err = r.Head(context.Background())
			}
			assert.Error(t, err)
		}
	}
}
