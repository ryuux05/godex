package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"golang.org/x/time/rate"
	"github.com/ryuux05/godex/pkg/core/errors"
	"github.com/ryuux05/godex/pkg/core/types"
)

type HTTPRPC struct{
	// base HTTP URl
	endpoint string
	// requests-per-second
	rateLimit uint16
	// maximum token usage at once.
	burstLimit uint16
	// http client
	client *http.Client
	// rate limiter
	limiter *rate.Limiter
}



// Response type for rpc
type rpcResponse[T any] struct {
	JSONRPC string `json:"jsonrpc"`
	ID uint `json:"id"`
	Result T `json:"result"`
	Error *errors.RPCError `json:"error"`
}

// NewHTTPRPC creates an HTTP JSON-RPC client.
// endpoint is the base RPC URL (e.g., https://...).
// rateLimit is the maximum requests per second (0 disables limiting).
func NewHTTPRPC(endpoint string, rateLimit uint16, burstLimit uint16) *HTTPRPC {
	var lim *rate.Limiter
	if rateLimit > 0 {
		lim = rate.NewLimiter(rate.Limit(rateLimit), int(burstLimit))
	}
	return &HTTPRPC{
		endpoint: endpoint,
		rateLimit: rateLimit,
		burstLimit: burstLimit,
		client: &http.Client{Timeout: 10 * time.Second},
		limiter: lim,
	}
}

func(r *HTTPRPC) Head(ctx context.Context) (string, error) {
	if r.limiter != nil {
		if err := r.limiter.Wait(ctx); err != nil {
			return "", err
		}
	}

	body := map[string]interface{} {
		"jsonrpc": "2.0",
		"id": 1,
		"method": "eth_blockNumber",
		"params": []interface{}{},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("error marshaling body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",r.endpoint, bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := r.client.Do(req)

	if err != nil {
		return "", fmt.Errorf("error fetching rpc: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		// HTTP error - transport layer failed
		return "", &errors.HTTPError{
			StatusCode: res.StatusCode,
			Message: res.Status,
		}
	}


	// Ensure the response body is closed when the function exits

	var resp rpcResponse[string]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return "", fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		// RPC error - RPC protocol error
		return "", &errors.RPCError{
			Code: resp.Error.Code,
			Message: resp.Error.Message,
		}
	}


	return resp.Result, nil
}

// GetBlock returns the block header for now (second params is set to false)
func(r *HTTPRPC) GetBlock(ctx context.Context, blockNumber string) (types.Block, error) {
	if r.limiter != nil {
		if err := r.limiter.Wait(ctx); err != nil {
			return types.Block{}, err
		}
	}

	body := map[string]interface{} {
		"jsonrpc": "2.0",
		"id": 1,
		"method": "eth_getBlockByNumber",
		"params": []interface{}{
			blockNumber,
			false,
		},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return types.Block{}, fmt.Errorf("error marshaling body: %w", err)		
	}


	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return types.Block{}, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	

	res, err := r.client.Do(req)
	if err != nil {
		return types.Block{}, fmt.Errorf("error fetching rpc: %w", err)		
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return types.Block{}, &errors.HTTPError{
			StatusCode: res.StatusCode,
			Message: res.Status,
		}
	}



	var resp rpcResponse[types.Block]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return types.Block{}, fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		return types.Block{}, &errors.RPCError{
			Code: resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	return resp.Result, nil
}

func(r *HTTPRPC) GetLogs(ctx context.Context, filter types.Filter) ([]types.Log, error) {
	if r.limiter != nil {
		if err := r.limiter.Wait(ctx); err != nil {
			return []types.Log{}, err
		}
	}
	
	body := map[string]interface{} {
		"jsonrpc": "2.0",
		"id": 1,
		"method": "eth_getLogs",
		"params": []interface{}{
			filter,
		},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return []types.Log{}, fmt.Errorf("error marshaling body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return []types.Log{}, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	res, err := r.client.Do(req)
	if err != nil {
		return []types.Log{}, fmt.Errorf("error fetching rpc: %w", err)				
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return []types.Log{}, &errors.HTTPError{
			StatusCode: res.StatusCode,
			Message: res.Status,
		}
	}


	var resp rpcResponse[[]types.Log]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return []types.Log{}, fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		return []types.Log{}, &errors.RPCError{
			Code: resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	return resp.Result, nil
}

func(r *HTTPRPC) GetBlockReceipts(ctx context.Context, blockNumber string) ([]types.Receipt, error) {
	if r.limiter != nil {
		if err := r.limiter.Wait(ctx); err != nil {
			return []types.Receipt{}, err
		}
	}

	body := map[string]interface{} {
		"jsonrpc": "2.0",
		"id": 1,
		"method": "eth_getBlockReceipts",
		"params": []interface{}{
			blockNumber,
		},		
	}

	b, err := json.Marshal(body)
	if err != nil {
		return []types.Receipt{}, fmt.Errorf("error marshaling body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return []types.Receipt{}, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := r.client.Do(req)
	if err != nil {
		return []types.Receipt{}, fmt.Errorf("error fetching rpc: %w", err)	
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return []types.Receipt{},  &errors.HTTPError{
			StatusCode: res.StatusCode,
			Message: res.Status,
		}
	} 

	var resp rpcResponse[[]types.Receipt]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return []types.Receipt{}, fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		return []types.Receipt{}, &errors.RPCError{
			Code: resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	return resp.Result, nil
}
