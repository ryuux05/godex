package main

import (
	"context"
	_ "embed"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/examples/payment-reconciler/bindings"
	"github.com/ryuux05/godex/pkg/godex"
)

//go:embed schema.sql
var applicationSchema string

type PaymentHandler struct{}

var _ postgres.Handler = PaymentHandler{}
var _ postgres.RollbackHandler = PaymentHandler{}

func prepareApplication(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, applicationSchema)
		return err
	})
}

func (PaymentHandler) Handle(ctx context.Context, tx pgx.Tx, event godex.Event) error {
	handlers := bindings.ERC20Handlers{OnTransfer: storePayment}
	return handlers.Handle(ctx, tx, event)
}

func storePayment(ctx context.Context, tx pgx.Tx, event godex.Event, transfer bindings.ERC20Transfer) error {
	_, err := tx.Exec(ctx, `INSERT INTO payment_transfers
		(chain_id,event_id,token_address,from_address,to_address,amount,block_num,log_index,tx_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		event.ChainId, event.Id, strings.ToLower(event.Address), string(transfer.From),
		string(transfer.To), transfer.Value.String(), event.BlockNumber, event.LogIndex, event.TransactionHash)
	return err
}

func (PaymentHandler) Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error {
	_, err := tx.Exec(ctx, `DELETE FROM payment_transfers WHERE chain_id=$1 AND block_num >= $2`, chainID, fromBlock)
	return err
}
