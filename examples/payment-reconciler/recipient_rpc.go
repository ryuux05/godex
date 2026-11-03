package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/ryuux05/godex/pkg/godex"
)

const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// recipientRPC borrows the HTTP adapter, including its chain identity capability.
// Invoice addresses are refreshed for every range, including split retries.
type recipientRPC struct {
	*godex.HTTPRPC
	pool                  *pgxpool.Pool
	chainID, tokenAddress string
}

func (r *recipientRPC) GetLogs(ctx context.Context, filter godex.Filter) ([]godex.Log, error) {
	to, err := utils.HexQtyToUint64(filter.ToBlock)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT deposit_address FROM payment_invoices
		WHERE chain_id=$1 AND token_address=$2 AND accept_from_block<=$3 ORDER BY deposit_address`, r.chainID, r.tokenAddress, to)
	if err != nil {
		return nil, err
	}
	addresses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	type positioned struct {
		log          godex.Log
		block, index uint64
	}
	var collected []positioned
	const recipientsPerRequest = 128
	for offset := 0; offset < len(addresses); offset += recipientsPerRequest {
		end := min(offset+recipientsPerRequest, len(addresses))
		topics := make([]string, 0, end-offset)
		for _, address := range addresses[offset:end] {
			topics = append(topics, "0x"+strings.Repeat("0", 24)+address[2:])
		}
		request := filter
		request.Address = []string{r.tokenAddress}
		request.Topics = [][]string{{transferTopic}, nil, topics}
		logs, err := r.HTTPRPC.GetLogs(ctx, request)
		if err != nil {
			return nil, err
		}
		for _, log := range logs {
			block, err := utils.HexQtyToUint64(log.BlockNumber)
			if err != nil {
				return nil, fmt.Errorf("payment log block: %w", err)
			}
			index, err := utils.HexQtyToUint64(log.LogIndex)
			if err != nil {
				return nil, fmt.Errorf("payment log index: %w", err)
			}
			collected = append(collected, positioned{log, block, index})
		}
	}
	sort.Slice(collected, func(i, j int) bool {
		if collected[i].block != collected[j].block {
			return collected[i].block < collected[j].block
		}
		return collected[i].index < collected[j].index
	})
	logs := make([]godex.Log, 0, len(collected))
	for _, entry := range collected {
		logs = append(logs, entry.log)
	}
	return logs, nil
}
