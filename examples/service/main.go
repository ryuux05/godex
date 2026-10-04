// The service example borrows application resources and stores decoded events.
// Set the environment described in docs/sdk.md before running it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/godex"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		slog.Error("indexer stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	abi, err := os.ReadFile(os.Getenv("ABI_PATH"))
	if err != nil {
		return fmt.Errorf("read ABI_PATH: %w", err)
	}
	from, err := envUint("FROM_BLOCK")
	if err != nil {
		return err
	}
	depth, err := envUint("CONFIRMATION_DEPTH")
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("prepare service pool: %w", err)
	}
	defer pool.Close()
	// In an existing service, reuse its pool and HTTP client instead.
	client := &http.Client{Timeout: 10 * time.Second}
	var events []string
	if names := os.Getenv("EVENTS"); names != "" {
		for _, name := range strings.Split(names, ",") {
			events = append(events, strings.TrimSpace(name))
		}
	}
	indexer, err := godex.New(ctx, godex.Config{
		ChainID: os.Getenv("CHAIN_ID"), RPCURL: os.Getenv("RPC_URL"),
		RPCOptions: &godex.HTTPRPCOptions{Client: client, RateLimit: 20, BurstLimit: 5},
		Postgres:   &godex.PostgresConfig{Pool: pool},
		Contracts:  []godex.Contract{{Address: os.Getenv("CONTRACT_ADDRESS"), ABI: string(abi), Events: events}},
		FromBlock:  from, ConfirmationDepth: depth,
	})
	if err != nil {
		return err
	}
	return indexer.Run(ctx)
}

func envUint(name string) (uint64, error) {
	text := os.Getenv(name)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a nonnegative decimal integer", name)
	}
	return n, nil
}
