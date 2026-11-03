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

type DelegationHandler struct{}

var _ postgres.Handler = DelegationHandler{}
var _ postgres.RollbackHandler = DelegationHandler{}

func prepareApplication(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, applicationSchema)
		return err
	})
}

func (DelegationHandler) Handle(ctx context.Context, tx pgx.Tx, event godex.Event) error {
	switch event.EventType {
	case "DelegateChanged":
		delegator, err := event.Fields.String("delegator")
		if err != nil {
			return err
		}
		from, err := event.Fields.String("fromDelegate")
		if err != nil {
			return err
		}
		to, err := event.Fields.String("toDelegate")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO delegation_changes
			(chain_id,event_id,contract_address,delegator,from_delegate,to_delegate,block_num,log_index,tx_hash)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			event.ChainId, event.Id, strings.ToLower(event.Address), strings.ToLower(delegator),
			strings.ToLower(from), strings.ToLower(to), event.BlockNumber, event.LogIndex, event.TransactionHash)
		return err
	case "DelegateVotesChanged":
		delegate, err := event.Fields.String("delegate")
		if err != nil {
			return err
		}
		previous, err := event.Fields.BigInt("previousVotes")
		if err != nil {
			return err
		}
		votes, err := event.Fields.BigInt("newVotes")
		if err != nil {
			return err
		}
		if previous.Sign() < 0 || previous.BitLen() > 256 || votes.Sign() < 0 || votes.BitLen() > 256 {
			return fmt.Errorf("vote counts must fit uint256")
		}
		// newVotes is an absolute balance. Transfers can change voting power
		// without a DelegateChanged event; counting delegators cannot derive it.
		_, err = tx.Exec(ctx, `INSERT INTO delegation_vote_changes
			(chain_id,event_id,contract_address,delegate,previous_votes,new_votes,block_num,log_index,tx_hash)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			event.ChainId, event.Id, strings.ToLower(event.Address), strings.ToLower(delegate),
			previous.String(), votes.String(), event.BlockNumber, event.LogIndex, event.TransactionHash)
		return err
	default:
		return fmt.Errorf("unsupported delegation event %q", event.EventType)
	}
}

func (DelegationHandler) Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM delegation_changes WHERE chain_id=$1 AND block_num >= $2`, chainID, fromBlock); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM delegation_vote_changes WHERE chain_id=$1 AND block_num >= $2`, chainID, fromBlock)
	return err
}
