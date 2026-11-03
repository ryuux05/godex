package godex

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/core/utils"
)

// Contract selects events from an ABI at one EVM address. Events may contain
// names or canonical signatures, such as Transfer(address,address,uint256).
// An empty Events slice selects every event. Overloaded names require signatures.
type Contract struct {
	Address string
	ABI     string
	Events  []string
}

type PostgresConfig = postgres.SinkConfig

// Config assembles a single-chain processor. Supply exactly one of Sink or
// Postgres. All pools and custom RPC clients remain owned by the caller.
type Config struct {
	ChainID string
	Name    string
	RPCURL  string
	// RPC supplies a custom adapter instead of RPCURL and RPCOptions.
	RPC RPC
	// Nil uses 20 requests/second, burst 5, and a ten-second HTTP client timeout.
	RPCOptions *HTTPRPCOptions
	Sink       Sink
	// A nil Postgres.Handler stores decoded events without projection writes.
	Postgres  *PostgresConfig
	Contracts []Contract
	// FromBlock is the first block to index, inclusively. Zero resumes stored
	// progress, or starts at block 1 when there is no cursor. Higher stored
	// progress takes precedence; this setting does not rewind an existing indexer.
	FromBlock uint64
	// Choose confirmation depth for your chain. Zero indexes unconfirmed blocks.
	ConfirmationDepth uint64
	// Options overrides tuning. Zero RangeSize and FetcherConcurrency receive
	// defaults. StartBlock, ConfirmationDepth, Addresses and Topics must be unset;
	// use Config and Contracts for those settings.
	Options *Options
	Metrics Metrics
	Logger  *slog.Logger
	// StartupTimeout bounds chain verification, schema preparation and cursor
	// loading together. Zero defaults to 30 seconds.
	StartupTimeout time.Duration
	// SkipChainIDCheck explicitly disables endpoint identity verification, e.g.
	// for a custom RPC that does not implement the optional ChainIDRPC capability.
	SkipChainIDCheck bool
}

type preparedConfig struct {
	chainID string
	rpc     RPC
	opts    *Options
	router  *decoder.DecoderRouter
	pg      *PostgresConfig
}

// Validate checks configuration, ABIs and dependencies without network calls or
// database writes. New additionally verifies endpoint identity and loads progress.
func (cfg Config) Validate() error {
	_, err := cfg.prepare()
	return err
}

// New assembles and registers one chain. It derives fetch filters and routing
// from Contracts, verifies RPC identity before accessing storage, and resumes
// persisted progress. Existing NewProcessor/AddChain APIs remain available.
func New(ctx context.Context, cfg Config) (*Processor, error) {
	if ctx == nil {
		return nil, fmt.Errorf("startup context is required")
	}
	preset, err := cfg.prepare()
	if err != nil {
		return nil, err
	}
	budget := cfg.StartupTimeout
	if budget == 0 {
		budget = 30 * time.Second
	}
	startup, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := startup.Err(); err != nil {
		return nil, err
	}
	if !cfg.SkipChainIDCheck {
		id, err := preset.rpc.(ChainIDRPC).ChainID(startup)
		if err != nil {
			return nil, fmt.Errorf("verify RPC chain ID: %w", err)
		}
		actual, err := canonicalChainID(id)
		if err != nil {
			return nil, fmt.Errorf("RPC returned an invalid chain ID")
		}
		if actual != preset.chainID {
			return nil, fmt.Errorf("RPC chain ID %s does not match configured chain ID %s", actual, preset.chainID)
		}
	}
	s := cfg.Sink
	if preset.pg != nil {
		s, err = postgres.NewSinkContext(startup, *preset.pg)
		if err != nil {
			return nil, fmt.Errorf("prepare PostgreSQL sink: %w", err)
		}
	}
	p := NewProcessor(cfg.Metrics, s)
	if cfg.Logger != nil {
		p.SetLogger(cfg.Logger)
	}
	if err := p.AddChainContext(startup, ChainInfo{ChainId: preset.chainID, Name: cfg.Name, RPC: preset.rpc}, preset.opts, preset.router); err != nil {
		return nil, fmt.Errorf("register chain: %w", err)
	}
	return p, nil
}

func (cfg Config) prepare() (*preparedConfig, error) {
	chainID, err := canonicalChainID(cfg.ChainID)
	if err != nil {
		return nil, fmt.Errorf("ChainID must be a nonnegative decimal or hexadecimal integer")
	}
	if cfg.StartupTimeout < 0 {
		return nil, fmt.Errorf("StartupTimeout cannot be negative")
	}
	if (cfg.Sink == nil) == (cfg.Postgres == nil) {
		return nil, fmt.Errorf("supply exactly one of Sink or Postgres")
	}
	if cfg.Sink != nil && nilDependency(cfg.Sink) {
		return nil, fmt.Errorf("Sink cannot be a typed nil")
	}
	var pg *PostgresConfig
	if cfg.Postgres != nil {
		copy := *cfg.Postgres
		if copy.Pool == nil {
			return nil, fmt.Errorf("Postgres.Pool is required and remains caller-owned")
		}
		if copy.Handler == nil {
			copy.Handler = postgres.HandlerFunc(func(context.Context, pgx.Tx, Event) error { return nil })
		} else if nilDependency(copy.Handler) {
			return nil, fmt.Errorf("Postgres.Handler cannot be a typed nil")
		}
		if v, ok := copy.Handler.(interface{ Validate() error }); ok {
			if err := v.Validate(); err != nil {
				return nil, fmt.Errorf("Postgres.Handler: %w", err)
			}
		}
		pg = &copy
	}
	opts := DefaultOptions()
	if cfg.Options != nil {
		opts = *cfg.Options
		if opts.StartBlock != 0 || opts.ConfirmationDepth != 0 || len(opts.Addresses) > 0 || len(opts.Topics) > 0 {
			return nil, fmt.Errorf("Options must not set StartBlock, ConfirmationDepth, Addresses or Topics; use Config and Contracts")
		}
		defaults := DefaultOptions()
		if opts.RangeSize == 0 {
			opts.RangeSize = defaults.RangeSize
		}
		if opts.FetcherConcurrency == 0 {
			opts.FetcherConcurrency = defaults.FetcherConcurrency
		}
	}
	if cfg.FromBlock > 0 {
		opts.StartBlock = cfg.FromBlock - 1
	}
	opts.ConfirmationDepth = cfg.ConfirmationDepth
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("Options: %w", err)
	}
	router, addresses, topics, err := compileContracts(cfg.Contracts)
	if err != nil {
		return nil, err
	}
	opts.Addresses, opts.Topics = addresses, [][]string{topics}
	r := cfg.RPC
	if r != nil {
		if nilDependency(r) {
			return nil, fmt.Errorf("RPC cannot be a typed nil")
		}
		if cfg.RPCURL != "" || cfg.RPCOptions != nil {
			return nil, fmt.Errorf("custom RPC cannot be combined with RPCURL or RPCOptions")
		}
	} else {
		options := HTTPRPCOptions{RateLimit: 20, BurstLimit: 5}
		if cfg.RPCOptions != nil {
			options = *cfg.RPCOptions
		}
		r, err = NewHTTPRPCWithOptions(cfg.RPCURL, options)
		if err != nil {
			return nil, err
		}
	}
	if !cfg.SkipChainIDCheck {
		if _, ok := r.(ChainIDRPC); !ok {
			return nil, fmt.Errorf("RPC must implement ChainIDRPC for verification; SkipChainIDCheck explicitly opts out")
		}
	}
	return &preparedConfig{chainID: chainID, rpc: r, opts: &opts, router: router, pg: pg}, nil
}

func canonicalChainID(text string) (string, error) {
	base := 10
	if strings.HasPrefix(text, "0x") {
		base, text = 16, text[2:]
	}
	if text == "" {
		return "", fmt.Errorf("empty chain ID")
	}
	for _, c := range text {
		if c >= '0' && c <= '9' {
			continue
		}
		if base == 16 && ((c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			continue
		}
		return "", fmt.Errorf("invalid chain ID")
	}
	n, ok := new(big.Int).SetString(text, base)
	if !ok {
		return "", fmt.Errorf("invalid chain ID")
	}
	return n.String(), nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	switch v := reflect.ValueOf(value); v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}

func compileContracts(contracts []Contract) (*decoder.DecoderRouter, []string, []string, error) {
	if len(contracts) == 0 {
		return nil, nil, nil, fmt.Errorf("at least one Contract is required")
	}
	router := decoder.NewDecoderRouter()
	addressSet, topicSet, routes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, contract := range contracts {
		address := strings.ToLower(contract.Address)
		if !strings.HasPrefix(address, "0x") || len(address) != 42 {
			return nil, nil, nil, fmt.Errorf("Contracts[%d].Address must be a 20-byte hexadecimal address with 0x prefix", i)
		}
		if _, err := hex.DecodeString(address[2:]); err != nil {
			return nil, nil, nil, fmt.Errorf("Contracts[%d].Address contains invalid hexadecimal characters", i)
		}
		var abi decoder.ABI
		if err := json.Unmarshal([]byte(contract.ABI), &abi); err != nil {
			return nil, nil, nil, fmt.Errorf("Contracts[%d].ABI: invalid ABI JSON: %w", i, err)
		}
		selected, err := selectEvents(abi, contract.Events)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Contracts[%d]: %w", i, err)
		}
		data, err := json.Marshal(selected)
		if err != nil {
			return nil, nil, nil, err
		}
		d := decoder.NewStandardDecoder()
		name := fmt.Sprintf("contract-%d", i)
		if err := d.RegisterABI(name, string(data)); err != nil {
			return nil, nil, nil, fmt.Errorf("Contracts[%d].ABI: %w", i, err)
		}
		var matchers []decoder.MatchFunc
		for _, event := range selected {
			topic := utils.FunctionSignatureToTopic(eventSignature(event))
			key := address + ":" + topic
			if routes[key] {
				return nil, nil, nil, fmt.Errorf("Contracts[%d] duplicates route for %s at %s", i, eventSignature(event), address)
			}
			routes[key], topicSet[topic] = true, true
			matchers = append(matchers, decoder.ByTopic0(topic))
		}
		router.Register(decoder.And(decoder.ByAddress(address), decoder.Or(matchers...)), name, d)
		addressSet[address] = true
	}
	addresses, topics := make([]string, 0, len(addressSet)), make([]string, 0, len(topicSet))
	for address := range addressSet {
		addresses = append(addresses, address)
	}
	for topic := range topicSet {
		topics = append(topics, topic)
	}
	slices.Sort(addresses)
	slices.Sort(topics)
	return router, addresses, topics, nil
}

func eventSignature(item decoder.ABIItem) string {
	inputs := make([]string, len(item.Inputs))
	for i, input := range item.Inputs {
		inputs[i] = input.Type
	}
	return item.Name + "(" + strings.Join(inputs, ",") + ")"
}

func selectEvents(abi decoder.ABI, selectors []string) (decoder.ABI, error) {
	var events decoder.ABI
	for _, item := range abi {
		if item.Type == "event" {
			events = append(events, item)
		}
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("ABI contains no events")
	}
	selected := events
	if len(selectors) > 0 {
		selected = nil
		for _, selector := range selectors {
			var matches decoder.ABI
			for _, event := range events {
				if event.Name == selector || eventSignature(event) == selector {
					matches = append(matches, event)
				}
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("event %q not found in ABI", selector)
			}
			if len(matches) > 1 {
				return nil, fmt.Errorf("event %q is ambiguous; select its canonical signature", selector)
			}
			selected = append(selected, matches[0])
		}
	}
	seen := map[string]bool{}
	for _, event := range selected {
		if event.Name == "" {
			return nil, fmt.Errorf("event name is required")
		}
		signature := eventSignature(event)
		if seen[signature] {
			return nil, fmt.Errorf("duplicate event selection %s", signature)
		}
		seen[signature] = true
		fields := map[string]bool{}
		for _, input := range event.Inputs {
			if input.Name == "" || fields[input.Name] {
				return nil, fmt.Errorf("event %s requires unique, nonempty input names", signature)
			}
			fields[input.Name] = true
		}
	}
	return selected, nil
}
