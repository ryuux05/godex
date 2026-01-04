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

// allowed result type constrain
type RPCResult interface {
    string | types.Block | []types.Log | []types.Receipt
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

func call[T RPCResult](ctx context.Context, r *HTTPRPC, method string, params[]interface{}) (T, error) {
	var zero T

	if r.limiter != nil {
		if err := r.limiter.Wait(ctx); err != nil {
			return zero, err
		}
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}

	b, err := json.Marshal(body)
	if err != nil {
		return zero, fmt.Errorf("error marshaling body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return zero, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := r.client.Do(req)
	if err != nil {
		return zero, fmt.Errorf("error fetching rpc: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return zero, &errors.HTTPError{
			StatusCode: res.StatusCode,
			Message:    res.Status,
		}
	}

	var resp rpcResponse[T]
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return zero, fmt.Errorf("error reading response body: %w", err)
	}

	if resp.Error != nil {
		return zero, &errors.RPCError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	return resp.Result, nil
}

// callBatch is the generic batch RPC caller.
func callBatch[T any](ctx context.Context, r *HTTPRPC, requests []map[string]interface{}) ([]rpcResponse[T], error) {
	if len(requests) == 0 {
		return nil, nil
	}

	if r.limiter != nil {
		if err := r.limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	b, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("error marshaling batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching rpc: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, &errors.HTTPError{
			StatusCode: res.StatusCode,
			Message:    res.Status,
		}
	}

	var resp []rpcResponse[T]
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	return resp, nil
}

// ============ Public Methods ============

func (r *HTTPRPC) Head(ctx context.Context) (string, error) {
	return call[string](ctx, r, "eth_blockNumber", []interface{}{})
}

func (r *HTTPRPC) GetBlock(ctx context.Context, blockNumber string) (types.Block, error) {
	return call[types.Block](ctx, r, "eth_getBlockByNumber", []interface{}{blockNumber, false})
}

func (r *HTTPRPC) GetBlocks(ctx context.Context, blockNumbers []string) (map[string]types.Block, error) {
	if len(blockNumbers) == 0 {
		return make(map[string]types.Block), nil
	}

	requests := make([]map[string]interface{}, len(blockNumbers))
	for i, n := range blockNumbers {
		requests[i] = map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      i,
			"method":  "eth_getBlockByNumber",
			"params":  []interface{}{n, false},
		}
	}

	responses, err := callBatch[types.Block](ctx, r, requests)
	if err != nil {
		return nil, err
	}

	blocks := make(map[string]types.Block, len(blockNumbers))
	for _, res := range responses {
		if res.Error != nil {
			continue
		}
		idx := int(res.ID)
		if idx >= 0 && idx < len(blockNumbers) {
			blocks[blockNumbers[idx]] = res.Result
		}
	}

	return blocks, nil
}

func (r *HTTPRPC) GetLogs(ctx context.Context, filter types.Filter) ([]types.Log, error) {
	return call[[]types.Log](ctx, r, "eth_getLogs", []interface{}{filter})
}

func (r *HTTPRPC) GetBlockReceipts(ctx context.Context, blockNumber string) ([]types.Receipt, error) {
	return call[[]types.Receipt](ctx, r, "eth_getBlockReceipts", []interface{}{blockNumber})
}
