package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/ryuux05/godex/pkg/godex"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.Error("payment reconciler stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	command := "watch"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	if command != "watch" && command != "init" && command != "invoice" && command != "status" {
		return fmt.Errorf("usage: payment-reconciler [init | invoice -id ID -to ADDRESS -amount RAW_UNITS | status -id ID | watch]")
	}
	if os.Getenv("DATABASE_URL") == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if command == "watch" {
		if len(args) != 0 {
			return fmt.Errorf("watch does not accept arguments")
		}
		return watch(ctx, pool)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch command {
	case "init":
		if len(args) != 0 {
			return fmt.Errorf("init does not accept arguments")
		}
		if err := prepareApplication(commandCtx, pool); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "payment schema ready")
		return err
	case "invoice":
		return createInvoice(commandCtx, pool, args, out)
	case "status":
		flags := flag.NewFlagSet("status", flag.ContinueOnError)
		flags.SetOutput(out)
		id := flags.String("id", "", "checkout invoice ID")
		if err := flags.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*id) == "" || flags.NArg() != 0 {
			return fmt.Errorf("status requires -id ID")
		}
		status, err := readStatus(commandCtx, pool, strings.TrimSpace(*id))
		if err != nil {
			return err
		}
		return writeJSON(out, status)
	}
	return nil
}

func createInvoice(ctx context.Context, pool *pgxpool.Pool, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("invoice", flag.ContinueOnError)
	flags.SetOutput(out)
	id := flags.String("id", "", "checkout invoice ID")
	to := flags.String("to", "", "fresh deposit address allocated by your wallet service")
	amount := flags.String("amount", "", "positive integer in raw token units")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" || flags.NArg() != 0 {
		return fmt.Errorf("invoice requires -id ID -to ADDRESS -amount RAW_UNITS")
	}
	address, err := parseAddress(*to)
	if err != nil {
		return fmt.Errorf("invoice recipient: %w", err)
	}
	expected, err := parseAmount(*amount)
	if err != nil {
		return err
	}
	chainID, token, rpc, err := paymentRPC()
	if err != nil {
		return err
	}
	actual, err := rpc.ChainID(ctx)
	if err != nil {
		return err
	}
	actual, err = canonicalChainID(actual)
	if err != nil || actual != chainID {
		return fmt.Errorf("RPC chain ID does not match CHAIN_ID")
	}
	head, err := rpc.Head(ctx)
	if err != nil {
		return err
	}
	height, err := utils.HexQtyToUint64(head)
	if err != nil || height >= math.MaxInt64 {
		return fmt.Errorf("RPC head cannot be used as an invoice acceptance block")
	}
	invoice, err := registerInvoice(ctx, pool, Invoice{
		ID: strings.TrimSpace(*id), ChainID: chainID, TokenAddress: token,
		DepositAddress: address, ExpectedAmount: expected, AcceptFromBlock: height + 1,
	})
	if err != nil {
		return err
	}
	return writeJSON(out, invoice)
}

func watch(ctx context.Context, pool *pgxpool.Pool) error {
	chainID, token, rpc, err := paymentRPC()
	if err != nil {
		return err
	}
	if os.Getenv("FROM_BLOCK") == "" {
		return fmt.Errorf("FROM_BLOCK is required")
	}
	from, err := envUint("FROM_BLOCK", 0)
	if err != nil {
		return err
	}
	depth, err := envUint("CONFIRMATION_DEPTH", 12)
	if err != nil {
		return err
	}
	filtered := &recipientRPC{HTTPRPC: rpc, pool: pool, chainID: chainID, tokenAddress: token}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	indexer, err := godex.New(startup, godex.Config{
		ChainID: chainID, RPC: filtered,
		Contracts: []godex.Contract{{Address: token, ABI: erc20ABI, Events: []string{"Transfer"}}},
		Postgres:  &godex.PostgresConfig{Pool: pool, Handler: PaymentHandler{}},
		FromBlock: from, ConfirmationDepth: depth,
	})
	if err != nil {
		return err
	}
	if err := prepareApplication(startup, pool); err != nil {
		return err
	}
	slog.Info("reconciling invoice payments", "chain", chainID, "token", token)
	return indexer.Run(ctx)
}

func paymentRPC() (string, string, *godex.HTTPRPC, error) {
	chainID, err := canonicalChainID(os.Getenv("CHAIN_ID"))
	if err != nil {
		return "", "", nil, fmt.Errorf("CHAIN_ID: %w", err)
	}
	token, err := parseAddress(os.Getenv("CONTRACT_ADDRESS"))
	if err != nil {
		return "", "", nil, fmt.Errorf("CONTRACT_ADDRESS: %w", err)
	}
	rpc, err := godex.NewHTTPRPCWithOptions(os.Getenv("RPC_URL"), godex.HTTPRPCOptions{RateLimit: 20, BurstLimit: 5})
	if err != nil {
		return "", "", nil, fmt.Errorf("RPC_URL: %w", err)
	}
	return chainID, token, rpc, nil
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

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
