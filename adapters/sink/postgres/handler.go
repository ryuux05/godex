package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/ryuux05/godex/pkg/core/types"
)

type Handler interface {
	Handle(ctx context.Context, tx pgx.Tx, ev types.Event) error
}
