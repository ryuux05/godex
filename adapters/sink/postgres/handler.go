package postgres

import (
	"context"

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
