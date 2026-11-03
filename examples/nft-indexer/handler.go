package main

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/pkg/godex"
)

//go:embed schema.sql
var applicationSchema string

type NFTHandler struct{}

var _ postgres.Handler = NFTHandler{}
var _ postgres.RollbackHandler = NFTHandler{}

func prepareApplication(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, applicationSchema)
		return err
	})
}

func (NFTHandler) Handle(ctx context.Context, tx pgx.Tx, event godex.Event) error {
	if event.EventType != "Transfer" {
		return fmt.Errorf("unsupported NFT event %q", event.EventType)
	}
	from, err := event.Fields.String("from")
	if err != nil {
		return err
	}
	to, err := event.Fields.String("to")
	if err != nil {
		return err
	}
	tokenID, err := event.Fields.BigInt("tokenId")
	if err != nil {
		return err
	}
	if tokenID.Sign() < 0 || tokenID.BitLen() > 256 {
		return fmt.Errorf("tokenId must fit uint256")
	}
	_, err = tx.Exec(ctx, `INSERT INTO nft_transfers
		(chain_id,event_id,contract_address,token_id,from_address,to_address,block_num,log_index,tx_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		event.ChainId, event.Id, strings.ToLower(event.Address), tokenID.String(),
		strings.ToLower(from), strings.ToLower(to), event.BlockNumber, event.LogIndex, event.TransactionHash)
	return err
}

func (NFTHandler) Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error {
	_, err := tx.Exec(ctx, `DELETE FROM nft_transfers WHERE chain_id=$1 AND block_num >= $2`, chainID, fromBlock)
	return err
}
