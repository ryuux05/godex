package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/godex"
)

//go:embed erc721_abi.json
var erc721ABI string

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		slog.Error("NFT indexer stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	for _, name := range []string{"DATABASE_URL", "RPC_URL", "CHAIN_ID", "CONTRACT_ADDRESS", "FROM_BLOCK"} {
		if os.Getenv(name) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	from, err := envUint("FROM_BLOCK", 1)
	if err != nil {
		return err
	}
	depth, err := envUint("CONFIRMATION_DEPTH", 12)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("prepare database pool: %w", err)
	}
	defer pool.Close()
	indexer, err := godex.New(ctx, godex.Config{
		ChainID: os.Getenv("CHAIN_ID"), RPCURL: os.Getenv("RPC_URL"),
		Contracts: []godex.Contract{{
			Address: os.Getenv("CONTRACT_ADDRESS"), ABI: erc721ABI,
			Events: []string{"Transfer"},
		}},
		Postgres:  &godex.PostgresConfig{Pool: pool, Handler: NFTHandler{}},
		FromBlock: from, ConfirmationDepth: depth,
	})
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := prepareApplication(startup, pool); err != nil {
		return fmt.Errorf("prepare NFT schema: %w", err)
	}
	slog.Info("tracking NFT ownership", "chain", os.Getenv("CHAIN_ID"), "contract", os.Getenv("CONTRACT_ADDRESS"))
	return indexer.Run(ctx)
}

func envUint(name string, fallback uint64) (uint64, error) {
	text := os.Getenv(name)
	if text == "" {
		return fallback, nil
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a nonnegative decimal integer", name)
	}
	return n, nil
}
