package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/ryuux05/godex/adapters/metrics"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/godex"
)

//go:embed uniswap_abi.json
var uniswapABI embed.FS

// UniswapHandler processes Uniswap swap events within the database transaction
type UniswapHandler struct {
	logger *slog.Logger
}

func (h *UniswapHandler) Handle(ctx context.Context, tx pgx.Tx, event types.Event) error {
	switch event.EventType {
	case "Swap":
		return h.handleSwap(ctx, tx, event)
	}

	return nil
}

func (h *UniswapHandler) handleSwap(ctx context.Context, tx pgx.Tx, event types.Event) error {
	contract := event.Address
	txHash := event.TransactionHash
	blockNum := event.BlockNumber
	timestamp := event.Timestamp

	// Extract fields
	var sender string
	var poolID string
	var fee *big.Int
	var amount0, amount1 *big.Int
	var sqrtPriceX96, liquidity, tick *big.Int

	// Extract pool ID (bytes32, indexed)
	if id, ok := event.Fields["id"].(string); ok {
		poolID = id
	} else if idBytes, ok := event.Fields["id"].([]byte); ok {
		poolID = fmt.Sprintf("0x%x", idBytes)
	}

	// Extract sender (address, indexed)
	if s, ok := event.Fields["sender"].(string); ok {
		sender = s
	} else {
		return fmt.Errorf("invalid 'sender' field type: %T", event.Fields["sender"])
	}

	// Extract amounts (int128)
	if a0, ok := event.Fields["amount0"].(*big.Int); ok {
		amount0 = a0
	} else {
		return fmt.Errorf("invalid 'amount0' field type: %T", event.Fields["amount0"])
	}

	if a1, ok := event.Fields["amount1"].(*big.Int); ok {
		amount1 = a1
	} else {
		return fmt.Errorf("invalid 'amount1' field type: %T", event.Fields["amount1"])
	}

	// Extract sqrtPriceX96, liquidity, tick
	if sp, ok := event.Fields["sqrtPriceX96"].(*big.Int); ok {
		sqrtPriceX96 = sp
	}
	if liq, ok := event.Fields["liquidity"].(*big.Int); ok {
		liquidity = liq
	}
	if t, ok := event.Fields["tick"].(*big.Int); ok {
		tick = t
	}

	// Extract fee (uint24)
	if f, ok := event.Fields["fee"].(*big.Int); ok {
		fee = f
	}

	// Compute human-readable values
	amount0Abs := absBigInt(amount0)
	amount1Abs := absBigInt(amount1)
	
	// Determine swap direction (simplified: positive amount0 = buy token0, negative = sell)
	direction := "sell"
	if amount0.Sign() > 0 {
		direction = "buy"
	}

	// Convert timestamp to PostgreSQL timestamp
	var blockTimestamp *time.Time
	if timestamp > 0 {
		ts := time.Unix(int64(timestamp), 0)
		blockTimestamp = &ts
	}

	// Calculate fee tier (fee is in basis points, e.g., 500 = 0.05%)
	var feeTier *int
	if fee != nil && fee.IsInt64() {
		ft := int(fee.Int64())
		feeTier = &ft
	}

	chainId := event.ChainId

	// Store swap in database
	var swapID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO uniswap_swaps (
			chain_id, contract_address, tx_hash, block_number, timestamp, block_timestamp,
			pool_id, fee_tier,
			sender, recipient, version,
			amount0_raw, amount1_raw,
			amount0_abs, amount1_abs,
			direction,
			sqrt_price_x96, liquidity, tick,
			amount0_in, amount1_in, amount0_out, amount1_out
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		ON CONFLICT (chain_id, tx_hash, contract_address, block_number) DO NOTHING
		RETURNING id
	`,
		chainId, contract, txHash, blockNum, timestamp, blockTimestamp,
		nullString(poolID), feeTier,
		sender, sender, "V4",
		amount0.String(), amount1.String(),
		amount0Abs.String(), amount1Abs.String(),
		direction,
		nullBigInt(sqrtPriceX96), nullBigInt(liquidity), nullInt(tick),
		nil, nil, nil, nil, // V2/V3 fields
	).Scan(&swapID)

	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil
		}
		return fmt.Errorf("failed to store swap: %w", err)
	}

	// Update pool statistics using absolute values
	_, err = tx.Exec(ctx, `
		INSERT INTO uniswap_pool_stats (
			chain_id, contract_address, last_swap_block, swap_count, total_volume0, total_volume1
		) VALUES ($1, $2, $3, 1, $4, $5)
		ON CONFLICT (chain_id, contract_address)
		DO UPDATE SET
			last_swap_block = GREATEST(uniswap_pool_stats.last_swap_block, $3),
			swap_count = uniswap_pool_stats.swap_count + 1,
			total_volume0 = uniswap_pool_stats.total_volume0 + $4,
			total_volume1 = uniswap_pool_stats.total_volume1 + $5
	`,
		chainId, contract, blockNum,
		amount0Abs.String(), amount1Abs.String(),
	)
	if err != nil {
		return fmt.Errorf("failed to update pool stats: %w", err)
	}

	// Connect related swaps
	err = h.connectSwaps(ctx, tx, swapID, chainId, sender, sender, timestamp)
	if err != nil {
		h.logger.Warn("failed to connect swaps", slog.Any("error", err))
		return nil
	}

	return nil
}

// Helper functions
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullInt(b *big.Int) *int {
	if b == nil || !b.IsInt64() {
		return nil
	}
	i := int(b.Int64())
	return &i
}

// connectSwaps finds and connects related swaps across chains
func (h *UniswapHandler) connectSwaps(ctx context.Context, tx pgx.Tx, swapID int64, chainId, sender, recipient string, timestamp uint64) error {
	// Find related swaps in the last 5 minutes (cross-chain arbitrage window)
	timeWindow := timestamp - 300 // 5 minutes in seconds

	// Find swaps with same sender on different chain (cross-chain activity)
	rows, err := tx.Query(ctx, `
		SELECT id, chain_id, timestamp
		FROM uniswap_swaps
		WHERE sender = $1 
			AND chain_id != $2
			AND timestamp >= $3
			AND timestamp <= $4
			AND id != $5
		ORDER BY ABS(timestamp - $4)
		LIMIT 10
	`, sender, chainId, timeWindow, timestamp, swapID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var relatedID int64
		var relatedChainId string
		var relatedTimestamp uint64
		if err := rows.Scan(&relatedID, &relatedChainId, &relatedTimestamp); err != nil {
			continue
		}

		timeDiff := int64(timestamp) - int64(relatedTimestamp)
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		// Insert connection
		_, err = tx.Exec(ctx, `
			INSERT INTO swap_connections (swap_id_1, swap_id_2, connection_type, time_diff_seconds)
			VALUES ($1, $2, 'cross_chain_sender', $3)
			ON CONFLICT (swap_id_1, swap_id_2) DO NOTHING
		`, swapID, relatedID, timeDiff)
		if err != nil {
			continue
		}
	}

	// Find swaps with same recipient on different chain
	rows, err = tx.Query(ctx, `
		SELECT id, chain_id, timestamp
		FROM uniswap_swaps
		WHERE recipient = $1 
			AND chain_id != $2
			AND timestamp >= $3
			AND timestamp <= $4
			AND id != $5
		ORDER BY ABS(timestamp - $4)
		LIMIT 10
	`, recipient, chainId, timeWindow, timestamp, swapID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var relatedID int64
		var relatedChainId string
		var relatedTimestamp uint64
		if err := rows.Scan(&relatedID, &relatedChainId, &relatedTimestamp); err != nil {
			continue
		}

		timeDiff := int64(timestamp) - int64(relatedTimestamp)
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		// Insert connection
		_, err = tx.Exec(ctx, `
			INSERT INTO swap_connections (swap_id_1, swap_id_2, connection_type, time_diff_seconds)
			VALUES ($1, $2, 'cross_chain_recipient', $3)
			ON CONFLICT (swap_id_1, swap_id_2) DO NOTHING
		`, swapID, relatedID, timeDiff)
		if err != nil {
			continue
		}
	}

	return nil
}

func nullBigInt(b *big.Int) *string {
	if b == nil {
		return nil
	}
	s := b.String()
	return &s
}

func absBigInt(b *big.Int) *big.Int {
	if b == nil {
		return big.NewInt(0)
	}
	abs := new(big.Int).Abs(b)
	return abs
}

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Get configuration from environment
	ethRPCURL := getEnv("ETH_RPC_URL", "https://eth-mainnet.g.alchemy.com/v2/demo")
	arbRPCURL := getEnv("ARB_RPC_URL", "https://arb-mainnet.g.alchemy.com/v2/demo")
	databaseURL := getEnv("DATABASE_URL", "postgres://godex:password@localhost:5432/godex?sslmode=disable")
	// V4 launched on January 31, 2025
	// Set start blocks to V4 deployment blocks (approximate - verify actual deployment blocks)
	// Ethereum: ~21,000,000+ (verify actual V4 deployment block)
	// Arbitrum: ~250,000,000+ (verify actual V4 deployment block)
	ethStartBlock := getEnvUint64("ETH_START_BLOCK", 21000000)  // Approximate V4 deployment - VERIFY
	arbStartBlock := getEnvUint64("ARB_START_BLOCK", 250000000) // Approximate V4 deployment - VERIFY

	logger.Info("initializing cross-chain Uniswap V4 indexer",
		slog.String("eth_rpc_url", ethRPCURL),
		slog.String("arb_rpc_url", arbRPCURL),
		slog.String("database_url", maskPassword(databaseURL)),
		slog.Uint64("eth_start_block", ethStartBlock),
		slog.Uint64("arb_start_block", arbStartBlock),
		slog.String("note", "V4 launched Jan 31, 2025 - verify deployment blocks"),
	)

	// Initialize RPC clients
	ethRPC := godex.NewHTTPRPC(
		ethRPCURL,
		2, // requests per second
		2, // burst capacity
	)

	arbRPC := godex.NewHTTPRPC(
		arbRPCURL,
		5, // requests per second
		5, // burst capacity
	)

	// Create database connection pool
	dbPool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		logger.Error("failed to create database pool", slog.Any("error", err))
		os.Exit(1)
	}
	defer dbPool.Close()

	// Initialize metrics
	prometheusMetrics := metrics.New("godex", prometheus.DefaultRegisterer)

	// Create handler for custom processing
	handler := &UniswapHandler{
		logger: logger,
	}

	// Create sink with handler
	sinkConfig := postgres.SinkConfig{
		Pool:          dbPool,
		Handler:       handler,
		CopyThreshold: 32,
		Metrics:       prometheusMetrics,
	}

	sink, err := postgres.NewSink(sinkConfig)
	if err != nil {
		logger.Error("failed to create sink", slog.Any("error", err))
		os.Exit(1)
	}

	// Load Uniswap ABI
	abiData, err := uniswapABI.ReadFile("uniswap_abi.json")
	if err != nil {
		logger.Error("failed to load Uniswap ABI", slog.Any("error", err))
		os.Exit(1)
	}

	// Setup decoder
	dec := decoder.NewStandardDecoder()
	err = dec.RegisterABI("Uniswap", string(abiData))
	if err != nil {
		logger.Error("failed to register Uniswap ABI", slog.Any("error", err))
		os.Exit(1)
	}

	// Configure indexing options for Ethereum
	ethOpts := &godex.Options{
		RangeSize:          5,  // blocks per batch
		FetcherConcurrency: 10, // concurrent fetchers
		StartBlock:         ethStartBlock,
		ConfirmationDepth:  12,   // wait for 12 confirmations
		EnableTimestamps:   true, // include block timestamps
		Topics: [][]string{
			{
				"0x40e9cecb9f5f1f1c5b9c97dec2917b7ee92e57ba5563708daca94dd84ad7112f",
			},
		},
		Addresses: []string {
			"0x000000000004444c5dc75cb358380d2e3de08a90",
		},
		FetchMode:                godex.FetchModeLogs,
		UseLogsForHistoricalSync: true,
		RetryConfig: &godex.RetryConfig{
			MaxAttempts:       3,
			InitialBackoff:    1 * time.Second,
			MaxBackoff:        30 * time.Second,
			Multiplier:        2.0,
			EnableJitter:      true,
			PerRequestTimeout: 10 * time.Second,
		},
	}

	// Configure indexing options for Arbitrum
	arbOpts := &godex.Options{
		RangeSize:          5,  // blocks per batch
		FetcherConcurrency: 10, // concurrent fetchers
		StartBlock:         arbStartBlock,
		ConfirmationDepth:  12,   // wait for 12 confirmations
		EnableTimestamps:   true, // include block timestamps
		Topics: [][]string{
			{
				"0x40e9cecb9f5f1f1c5b9c97dec2917b7ee92e57ba5563708daca94dd84ad7112f",
			},
		},
		Addresses: []string {
			"0x360e68faccca8ca495c1b759fd9eee466db9fb32",
		},
		FetchMode:                godex.FetchModeLogs,
		UseLogsForHistoricalSync: true,
		RetryConfig: &godex.RetryConfig{
			MaxAttempts:       3,
			InitialBackoff:    1 * time.Second,
			MaxBackoff:        30 * time.Second,
			Multiplier:        2.0,
			EnableJitter:      true,
			PerRequestTimeout: 10 * time.Second,
		},
	}

	// Define chains
	ethChain := godex.ChainInfo{
		ChainId: "1",
		Name:    "Ethereum",
		RPC:     ethRPC,
	}

	arbChain := godex.ChainInfo{
		ChainId: "42161",
		Name:    "Arbitrum",
		RPC:     arbRPC,
	}

	// Create processor with metrics and sink
	processor := godex.NewProcessor(prometheusMetrics, sink)
	processor.SetLogger(logger)

	// Use decoder router to match Swap events (3 topics: event signature, pool id, sender)
	router := decoder.NewDecoderRouter().Register(decoder.ByTopicCount(3), "Uniswap", dec)

	// Register Ethereum chain
	err = processor.AddChain(ethChain, ethOpts, router)
	if err != nil {
		logger.Error("failed to add Ethereum chain", slog.Any("error", err))
		os.Exit(1)
	}

	// Register Arbitrum chain
	err = processor.AddChain(arbChain, arbOpts, router)
	if err != nil {
		logger.Error("failed to add Arbitrum chain", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("starting cross-chain Uniswap V4 indexer",
		slog.String("eth_chain", ethChain.Name),
		slog.Uint64("eth_start_block", ethOpts.StartBlock),
		slog.String("arb_chain", arbChain.Name),
		slog.Uint64("arb_start_block", arbOpts.StartBlock),
		slog.Int("range_size", ethOpts.RangeSize),
		slog.Int("fetcher_concurrency", ethOpts.FetcherConcurrency),
		slog.String("v4_topic_hash", "0x77b638a85d8def4f2c541d550b973ab09afd8bcce6bc0c26084c58c05ca379e9"),
	)

	// Setup graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		logger.Info("metrics server starting", slog.String("addr", ":9090"))
		if err := http.ListenAndServe(":9090", nil); err != nil {
			logger.Error("metrics server failed", slog.Any("error", err))
		}
	}()

	// Start indexing - events automatically decoded and stored
	err = processor.Run(ctx)
	if err != nil && err != context.Canceled {
		logger.Error("indexer stopped with error", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("indexer stopped gracefully")
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvUint64(key string, defaultValue uint64) uint64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func maskPassword(url string) string {
	// Simple masking for logging - don't log passwords
	if len(url) > 20 {
		return url[:20] + "..."
	}
	return url
}
