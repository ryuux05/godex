package rpc_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceHTTPClientAndChainIdentity(t *testing.T) {
	calls := 0
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"eth_chainId"`)
		assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
		assert.Equal(t, "rpc.service", req.URL.Host)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`)), Header: make(http.Header)}, nil
	})
	client := &http.Client{Transport: transport, Timeout: 37 * time.Second}
	r, err := godex.NewHTTPRPCWithOptions("https://rpc.service", godex.HTTPRPCOptions{Client: client})
	require.NoError(t, err)
	id, err := r.ChainID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "0x1", id)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 37*time.Second, client.Timeout)

}

func TestServiceHTTPTransportObservesCancellation(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	r, err := godex.NewHTTPRPCWithOptions("https://rpc.service", godex.HTTPRPCOptions{Client: client})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = r.ChainID(ctx)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}

func TestHTTPOptionsRejectInvalidEndpointsWithoutLeakingCredentials(t *testing.T) {
	for _, endpoint := range []string{"", "rpc.service", "ftp://rpc.service", "https://", "https://rpc.service/#fragment", "https://user:secret-token@[broken"} {
		_, err := godex.NewHTTPRPCWithOptions(endpoint, godex.HTTPRPCOptions{})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret-token")
	}
}
