package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ryuux05/godex/pkg/core/types"
)

type Handler interface {
	Handle(ctx context.Context, tx pgx.Tx, ev types.Event) error
}

// RollbackHandler reverses handler-owned data from fromBlock inclusively in the
// same transaction as internal event deletion and cursor recovery. Implement
// this whenever Handle writes derived data that must follow the canonical chain.
type RollbackHandler interface {
	Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error
}

// HandlerFunc adapts a function to Handler. Use it for event-only storage or
// effects requiring no reversal. Projection writes also need a RollbackHandler.
type HandlerFunc func(context.Context, pgx.Tx, types.Event) error

func (f HandlerFunc) Handle(ctx context.Context, tx pgx.Tx, ev types.Event) error {
	if f == nil {
		return fmt.Errorf("event handler function is required")
	}
	return f(ctx, tx, ev)
}

// HandlerFuncs adapts paired functions for transactional projection writes and
// inclusive rollback. Both functions are required; rollback never silently skips.
type HandlerFuncs struct {
	HandleEvent    HandlerFunc
	RollbackEvents func(context.Context, pgx.Tx, string, uint64) error
}

func (h HandlerFuncs) Validate() error {
	if h.HandleEvent == nil || h.RollbackEvents == nil {
		return fmt.Errorf("HandleEvent and RollbackEvents functions are required")
	}
	return nil
}

func (h HandlerFuncs) Handle(ctx context.Context, tx pgx.Tx, ev types.Event) error {
	if err := h.Validate(); err != nil {
		return err
	}
	return h.HandleEvent(ctx, tx, ev)
}

func (h HandlerFuncs) Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error {
	if err := h.Validate(); err != nil {
		return err
	}
	return h.RollbackEvents(ctx, tx, chainID, fromBlock)
}
