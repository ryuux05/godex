package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/examples/internal/projections"
)

var _ postgres.RollbackHandler = (*UniswapHandler)(nil)

func prepareApplication(ctx context.Context, pool *pgxpool.Pool, h *UniswapHandler, rebuild bool) error {
	return projections.Prepare(ctx, pool, projections.Config{
		SQL:        applicationSchema,
		Tables:     []string{"swap_connections", "uniswap_pool_stats", "uniswap_pools", "uniswap_swaps", "uniswap_pool_initializations"},
		ProbeTable: "uniswap_swaps", RequiredColumn: "event_id",
		Handler: h, Rebuild: rebuild,
		IntegerFields: []string{"amount0", "amount1", "sqrtPriceX96", "liquidity", "tick", "fee", "tickSpacing"},
		BytesFields:   []string{"id"},
		EventTypes:    []string{"Swap", "Initialize"},
	})
}

func (h *UniswapHandler) Rollback(ctx context.Context, tx pgx.Tx, chainID string, fromBlock uint64) error {
	// Foreign keys remove connections involving deleted swaps, including links
	// to another chain. Surviving swaps and that other chain's aggregates remain.
	if _, err := tx.Exec(ctx, "DELETE FROM uniswap_swaps WHERE chain_id=$1 AND block_number >=$2", chainID, fromBlock); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM uniswap_pool_initializations WHERE chain_id=$1 AND block_number >=$2", chainID, fromBlock); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM uniswap_pool_stats WHERE chain_id=$1", chainID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO uniswap_pool_stats(chain_id,contract_address,last_swap_block,swap_count,total_volume0,total_volume1)
	 SELECT chain_id,contract_address,MAX(block_number),COUNT(*),SUM(amount0_abs),SUM(amount1_abs)
	 FROM uniswap_swaps WHERE chain_id=$1 GROUP BY chain_id,contract_address`, chainID); err != nil {
		return fmt.Errorf("rebuild swap statistics: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM uniswap_pools WHERE chain_id=$1", chainID); err != nil {
		return err
	}
	return rebuildPools(ctx, tx, chainID, nil)
}

func rebuildPools(ctx context.Context, tx pgx.Tx, chainID string, poolID *string) error {
	_, err := tx.Exec(ctx, `INSERT INTO uniswap_pools(pool_id,chain_id,token0_address,token1_address,fee_tier,tick_spacing,hooks_address,sqrt_price_x96,tick,first_seen_block,last_seen_block)
	 SELECT DISTINCT ON(pool_id) pool_id,chain_id,token0_address,token1_address,fee_tier,tick_spacing,hooks_address,sqrt_price_x96,tick,
	 MIN(block_number) OVER(PARTITION BY pool_id),MAX(block_number) OVER(PARTITION BY pool_id)
	 FROM uniswap_pool_initializations WHERE chain_id=$1 AND ($2::text IS NULL OR pool_id=$2)
	 ORDER BY pool_id,block_number DESC,log_index DESC,event_id DESC
	 ON CONFLICT(chain_id,pool_id) DO UPDATE SET token0_address=EXCLUDED.token0_address,token1_address=EXCLUDED.token1_address,
	 fee_tier=EXCLUDED.fee_tier,tick_spacing=EXCLUDED.tick_spacing,hooks_address=EXCLUDED.hooks_address,
	 sqrt_price_x96=EXCLUDED.sqrt_price_x96,tick=EXCLUDED.tick,first_seen_block=EXCLUDED.first_seen_block,
	 last_seen_block=EXCLUDED.last_seen_block,updated_at=NOW()`, chainID, poolID)
	if err != nil {
		return fmt.Errorf("rebuild pools: %w", err)
	}
	return nil
}
