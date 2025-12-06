# RPC architecuture

## Overview

RPC is responsible for connecting with blockchain via http or websocket. It talk to blockchain via POST request to designated RPC. It is designed with retry strategy which is defined by user. As well as rate limiting

## Design Principles

### 1. Retry
Every rpc function is recommended to be wrap inside the retry function. In case a retryable error occurs it will attempt to retry until the maximum attempt is being hit. In the future we will implement fallback endpoint in case the primary endpoint unrecoverable.

The retry config configurable to suit any needs and preferences.
There is also **default** config ready to use.

```go
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff: 30 * time.Second,
		Multiplier: 2.0,
		EnableJitter: true,
	}
}
```

Below is example on how to use the retry function from `retry.go`
```go
err := rpc.RetryWithBackoff(ctx, config, func() error {
    var err error
    heaheadHex, err = chain.chainInfo.RPC.Head(rpcCtx)
	return errdHex
})
```

This makes sure that the indexer will persist through rpc failure and will continue when the rpc is recovered.


### 2. Rate limiting
Every RPC endpoint has its own rate-limit policy. To respect these limits, the SDK allows you to configure a per-RPC rate limit and burst allowance during initialization:
```go
func NewHTTPRPC(endpoint string, rateLimit uint16, burstLimit uint16) *HTTPRPC 
```

The rate limiter uses a token-bucket algorithm, chosen for its flexibility and ability to handle both steady throughput and short bursts. This ensures that your indexer stays within the provider’s allowed request rate while still maintaining optimal performance.
