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

//go:embed erc20_abi.json
var erc20ABI embed.FS

//go:embed schema.sql
var applicationSchema string

// ERC20Handler processes ERC20 events within the database transaction
type ERC20Handler struct{}

func (h *ERC20Handler) Handle(ctx context.Context, tx pgx.Tx, event types.Event) error {
	// Custom logic runs in the SAME transaction as event storage
	switch event.EventType {
	case "Transfer":
		return h.handleTransfer(ctx, tx, event)
	case "Approval":
		return h.handleApproval(ctx, tx, event)
	}
	return nil
}

func (h *ERC20Handler) handleTransfer(ctx context.Context, tx pgx.Tx, event types.Event) error {
	// Extract transfer data from decoded event
	from, ok := event.Fields["from"].(string)
	if !ok {
		return fmt.Errorf("invalid 'from' field type")
	}
	to, ok := event.Fields["to"].(string)
	if !ok {
		return fmt.Errorf("invalid 'to' field type")
	}
	value, ok := event.Fields["value"].(*big.Int)
	if !ok || value == nil {
		return fmt.Errorf("invalid 'value' field type")
	}
	contract := event.Address

	// Update transfer statistics atomically with event storage
	_, err := tx.Exec(ctx, `
		INSERT INTO erc20_transfer_stats (contract_address, from_address, to_address, value, block_num, tx_hash,chain_id,event_id,block_hash,log_index)
		VALUES ($1, $2, $3, $4, $5, $6,$7,$8,$9,$10) ON CONFLICT(chain_id,event_id) DO NOTHING
	`, contract, from, to, value.String(), event.BlockNumber, event.TransactionHash, event.ChainId, event.Id, event.BlockHash, event.LogIndex)

	if err != nil {
		return fmt.Errorf("failed to store transfer stats: %w", err)
	}

	// Update token holder balances (simplified - real implementation would track net transfers)
	// This ensures balance updates are atomic with the transfer event
	_, err = tx.Exec(ctx, `
		INSERT INTO erc20_balances (contract_address, holder_address, last_transfer_block,chain_id)
		VALUES ($1, $2, $3,$4)
		ON CONFLICT (chain_id,contract_address, holder_address)
		DO UPDATE SET last_transfer_block = GREATEST(erc20_balances.last_transfer_block, $3)
	`, contract, from, event.BlockNumber, event.ChainId)

	if err != nil {
		return fmt.Errorf("failed to update from balance: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO erc20_balances (contract_address, holder_address, last_transfer_block,chain_id)
		VALUES ($1, $2, $3,$4)
		ON CONFLICT (chain_id,contract_address, holder_address)
		DO UPDATE SET last_transfer_block = GREATEST(erc20_balances.last_transfer_block, $3)
	`, contract, to, event.BlockNumber, event.ChainId)

	return err
}

func (h *ERC20Handler) handleApproval(ctx context.Context, tx pgx.Tx, event types.Event) error {
	// Handle approval events
	owner, ok := event.Fields["owner"].(string)
	if !ok {
		return fmt.Errorf("invalid 'owner' field type")
	}
	spender, ok := event.Fields["spender"].(string)
	if !ok {
		return fmt.Errorf("invalid 'spender' field type")
	}
	value, ok := event.Fields["value"].(*big.Int)
	if !ok || value == nil {
		return fmt.Errorf("invalid 'value' field type")
	}
	contract := event.Address

	_, err := tx.Exec(ctx, `
		INSERT INTO erc20_approvals (contract_address, owner_address, spender_address, value, block_num, tx_hash,chain_id,event_id,block_hash,log_index)
		VALUES ($1, $2, $3, $4, $5, $6,$7,$8,$9,$10) ON CONFLICT(chain_id,event_id) DO NOTHING
	`, contract, owner, spender, value.String(), event.BlockNumber, event.TransactionHash, event.ChainId, event.Id, event.BlockHash, event.LogIndex)

	return err
}

func main() {
	// Setup graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Get configuration from environment
	rpcURL := getEnv("RPC_URL", "https://eth-mainnet.g.alchemy.com/v2/demo")
	databaseURL := getEnv("DATABASE_URL", "postgres://godex:password@localhost:5432/godex?sslmode=disable")
	startBlock := getEnvUint64("START_BLOCK", 10819611)

	logger.Info("initializing ERC20 indexer",
		slog.String("rpc_url", rpcURL),
		slog.String("database_url", maskPassword(databaseURL)),
		slog.Uint64("start_block", startBlock),
	)

	// Initialize RPC client
	rpc := godex.NewHTTPRPC(
		rpcURL,
		100, // requests per second
		100, // burst capacity
	)

	// Create database connection pool
	dbPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		logger.Error("failed to create database pool", slog.Any("error", err))
		os.Exit(1)
	}
	defer dbPool.Close()

	// Initialize metrics
	prometheusMetrics := metrics.New("godex", prometheus.DefaultRegisterer)

	// Create handler for custom processing

	handler := &ERC20Handler{}

	// Create sink with handler
	sinkConfig := postgres.SinkConfig{
		Pool:          dbPool,
		Handler:       handler,
		CopyThreshold: 32,
	}

	sinkCtx, stopSink := context.WithTimeout(ctx, 10*time.Second)
	sink, err := postgres.NewSinkContext(sinkCtx, sinkConfig)
	stopSink()
	if err != nil {
		logger.Error("failed to create sink", slog.Any("error", err))
		os.Exit(1)
	}

	rebuild := os.Getenv("REBUILD_PROJECTIONS") == "1"
	setupCtx := ctx
	if !rebuild {
		var stopSetup context.CancelFunc
		setupCtx, stopSetup = context.WithTimeout(ctx, 30*time.Second)
		defer stopSetup()
	}
	if err := prepareApplication(setupCtx, dbPool, handler, rebuild); err != nil {
		logger.Error("failed to prepare application schema", slog.Any("error", err))
		os.Exit(1)
	}

	// Load ERC20 ABI
	abiData, err := erc20ABI.ReadFile("erc20_abi.json")
	if err != nil {
		logger.Error("failed to load ERC20 ABI", slog.Any("error", err))
		os.Exit(1)
	}

	// Setup decoder
	dec := decoder.NewStandardDecoder()
	err = dec.RegisterABI("ERC20", string(abiData))
	if err != nil {
		logger.Error("failed to register ERC20 ABI", slog.Any("error", err))
		os.Exit(1)
	}

	// Configure indexing options
	opts := &godex.Options{
		RangeSize:          150, // blocks per batch
		FetcherConcurrency: 10,  // concurrent fetchers
		StartBlock:         startBlock,
		ConfirmationDepth:  0,    // wait for confirmations
		EnableTimestamps:   true, // include block timestamps
		Topics: [][]string{
			{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", // Transfer(address,address,uint256)
				"0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925", // Approval(address,address,uint256)
			},
		},
		FetchMode:                godex.FetchModeReceipts,
		UseLogsForHistoricalSync: true,
		RetryConfig: &godex.RetryConfig{
			MaxAttempts:       50, // Increase from 3
			InitialBackoff:    5 * time.Second,
			MaxBackoff:        60 * time.Second, // Increase from 30s
			Multiplier:        2.0,
			EnableJitter:      true,
			PerRequestTimeout: 10 * time.Second,
		},
	}

	// Define Ethereum mainnet
	chain := godex.ChainInfo{
		ChainId: "592",
		Name:    "ERC20",
		RPC:     rpc,
	}

	// Create processor with metrics and sink
	processor := godex.NewProcessor(prometheusMetrics, sink)
	processor.SetLogger(logger)

	router := decoder.NewDecoderRouter().Register(decoder.ByTopicCount(3), "ERC20", dec)
	// Register chain with decoder
	err = processor.AddChain(chain, opts, router)
	if err != nil {
		logger.Error("failed to add chain", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("starting ERC20 indexer",
		slog.String("chain", chain.Name),
		slog.Uint64("start_block", opts.StartBlock),
		slog.Int("range_size", opts.RangeSize),
		slog.Int("fetcher_concurrency", opts.FetcherConcurrency),
	)

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
