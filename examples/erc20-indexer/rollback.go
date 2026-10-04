package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/examples/internal/projections"
)

var _ postgres.RollbackHandler = (*ERC20Handler)(nil)

func prepareApplication(ctx context.Context, pool *pgxpool.Pool, h *ERC20Handler, rebuild bool) error {
	return projections.Prepare(ctx, pool, projections.Config{
		SQL:        applicationSchema,
		Tables:     []string{"erc20_balances", "erc20_approvals", "erc20_transfer_stats"},
		ProbeTable: "erc20_transfer_stats", RequiredColumn: "chain_id",
		Handler: h, Rebuild: rebuild,
		IntegerFields: []string{"value"},
		EventTypes:    []string{"Transfer", "Approval"},
	})
}

// Rollback removes orphaned history and restores holder recency from surviving
// transfers. erc20_balances tracks last-transfer heights, not token amounts.
func (h *ERC20Handler) Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error {
	for _, table := range []string{"erc20_transfer_stats", "erc20_approvals"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE chain_id=$1 AND block_num >=$2", chainID, fromBlock); err != nil {
			return fmt.Errorf("rollback %s: %w", table, err)
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM erc20_balances WHERE chain_id=$1", chainID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO erc20_balances(chain_id,contract_address,holder_address,last_transfer_block)
		SELECT $1,contract_address,holder,MAX(block_num) FROM (
		 SELECT contract_address,from_address AS holder,block_num FROM erc20_transfer_stats WHERE chain_id=$1
		 UNION ALL SELECT contract_address,to_address AS holder,block_num FROM erc20_transfer_stats WHERE chain_id=$1
		) transfers GROUP BY contract_address,holder`, chainID)
	if err != nil {
		return fmt.Errorf("rebuild holder recency: %w", err)
	}
	return nil
}
