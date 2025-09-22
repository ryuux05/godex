package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type HTTPRPC struct{
	// base HTTP URl
	endpoint string
	// requests-per-second
	rateLimit uint16
	// http client
	client *http.Client
}

type rpcError struct  {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Response type for rpc
type rpcResponse[T any] struct {
	JSONRPC string `json:"jsonrpc"`
	ID uint `json:"id"`
	Result T `json:"result"`
	Error *rpcError `json:"error"`
}

// NewHTTPRPC creates an HTTP JSON-RPC client.
// endpoint is the base RPC URL (e.g., https://...).
// rateLimit is the maximum requests per second (0 disables limiting).
func NewHTTPRPC(endpoint string, rateLimit uint16) *HTTPRPC {
	return &HTTPRPC{
		endpoint: endpoint,
		rateLimit: rateLimit,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func(r *HTTPRPC) Head(ctx context.Context) (string, error) {
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
		return "", fmt.Errorf("rrror fetching rpc: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status: %d", res.StatusCode)	
	}


	// Ensure the response body is closed when the function exits

	var resp rpcResponse[string]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return "", fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		return "", fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}


	return resp.Result, nil
}

// GetBlock returns the block header for now (second params is set to false)
func(r *HTTPRPC) GetBlock(ctx context.Context, blockNumber string) (Block, error) {
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
		return Block{}, fmt.Errorf("error marshaling body: %w", err)		
	}


	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return Block{}, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	

	res, err := r.client.Do(req)
	if err != nil {
		return Block{}, fmt.Errorf("error fetching rpc: %w", err)		
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return Block{}, fmt.Errorf("http status: %d", res.StatusCode)	
	}



	var resp rpcResponse[Block]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return Block{}, fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		return Block{}, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

func(r *HTTPRPC) GetLogs(ctx context.Context, filter Filter) ([]Log, error) {
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
		return []Log{}, fmt.Errorf("error marshaling body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return []Log{}, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	
	res, err := r.client.Do(req)
	if err != nil {
		return []Log{}, fmt.Errorf("error fetching rpc: %w", err)				
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return []Log{}, fmt.Errorf("http status: %d", res.StatusCode)	
	}


	var resp rpcResponse[[]Log]

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return []Log{}, fmt.Errorf("error reading response body: %w", err)
	}
	if resp.Error != nil {
		return []Log{}, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}
