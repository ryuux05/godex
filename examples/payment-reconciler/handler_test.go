package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryuux05/godex/adapters/sink/postgres"
	"github.com/ryuux05/godex/examples/payment-reconciler/bindings"
	"github.com/ryuux05/godex/internal/testutil"
	"github.com/ryuux05/godex/pkg/core/decoder"
	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tokenA   = "0x1111111111111111111111111111111111111111"
	tokenB   = "0x2222222222222222222222222222222222222222"
	depositA = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	depositB = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	payer    = "0xcccccccccccccccccccccccccccccccccccccccc"
	zero     = "0x0000000000000000000000000000000000000000"
)

func transferLog(block, index uint64, from, to string, amount *big.Int) godex.Log {
	return godex.Log{
		Address: tokenA,
		Topics: []string{transferTopic, "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(from, "0x"),
			"0x" + strings.Repeat("0", 24) + strings.TrimPrefix(to, "0x")},
		Data: fmt.Sprintf("0x%064x", amount), BlockNumber: fmt.Sprintf("0x%x", block),
		BlockHash: fmt.Sprintf("A-%d", block), TransactionHash: fmt.Sprintf("tx-%d", block), LogIndex: fmt.Sprintf("0x%x", index),
	}
}

func decodeTransfers(t *testing.T, chain string, logs ...godex.Log) []godex.Event {
	t.Helper()
	d := decoder.NewStandardDecoder()
	require.NoError(t, d.RegisterABI("payments", bindings.ERC20ABI))
	events := make([]godex.Event, 0, len(logs))
	for _, log := range logs {
		log.TransactionHash += "-chain-" + chain
		event, err := d.Decode("payments", chain, log)
		require.NoError(t, err)
		require.NotNil(t, event)
		events = append(events, *event)
	}
	return events
}

func setup(t *testing.T, threshold int) (*pgxpool.Pool, *postgres.PGSink) {
	t.Helper()
	pool := testutil.Postgres(t)
	sink, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: PaymentHandler{}, CopyThreshold: threshold})
	require.NoError(t, err)
	require.NoError(t, prepareApplication(context.Background(), pool))
	return pool, sink
}

func invoice(id, chain, token, address, amount string, from uint64) Invoice {
	return Invoice{ID: id, ChainID: chain, TokenAddress: token, DepositAddress: address, ExpectedAmount: amount, AcceptFromBlock: from}
}

func assertStatus(t *testing.T, pool *pgxpool.Pool, id, state, received, remaining, overpaid string, count int64) InvoiceStatus {
	t.Helper()
	s, err := readStatus(context.Background(), pool, id)
	require.NoError(t, err)
	assert.Equal(t, state, s.Status)
	assert.Equal(t, received, s.ReceivedAmount)
	assert.Equal(t, remaining, s.RemainingAmount)
	assert.Equal(t, overpaid, s.OverpaidAmount)
	assert.Equal(t, count, s.PaymentCount)
	return s
}

func TestPaymentLifecycleReplayReorgAndRestart(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			ctx := context.Background()
			original := invoice("order-1", "1", tokenA, depositA, "200", 2)
			_, err := registerInvoice(ctx, pool, original)
			require.NoError(t, err)
			assertStatus(t, pool, "order-1", "pending", "0", "200", "0", 0)
			store := func(logs ...godex.Log) {
				events := decodeTransfers(t, "1", logs...)
				for i := 0; i < 2; i++ {
					require.NoError(t, sink.Store(ctx, events))
				}
			}
			store(transferLog(1, 0, payer, depositA, big.NewInt(999)))
			assertStatus(t, pool, "order-1", "pending", "0", "200", "0", 0)
			store(transferLog(2, 0, payer, depositA, big.NewInt(75)))
			assertStatus(t, pool, "order-1", "partially_paid", "75", "125", "0", 1)
			store(transferLog(3, 0, payer, depositA, big.NewInt(50)), transferLog(3, 1, payer, depositA, big.NewInt(75)))
			s := assertStatus(t, pool, "order-1", "paid", "200", "0", "0", 3)
			require.NotNil(t, s.LastPaymentBlock)
			assert.Equal(t, uint64(3), *s.LastPaymentBlock)
			store(transferLog(4, 0, payer, depositA, big.NewInt(30)))
			assertStatus(t, pool, "order-1", "overpaid", "230", "0", "30", 4)
			require.NoError(t, sink.Rollback(ctx, "1", 4, "A-3"))
			assertStatus(t, pool, "order-1", "paid", "200", "0", "0", 3)
			require.NoError(t, sink.Rollback(ctx, "1", 3, "A-2"))
			assertStatus(t, pool, "order-1", "partially_paid", "75", "125", "0", 1)
			replacement := []godex.Log{transferLog(3, 0, payer, depositA, big.NewInt(100)), transferLog(4, 0, payer, depositA, big.NewInt(25))}
			for i := range replacement {
				replacement[i].BlockHash = fmt.Sprintf("B-%d", i+3)
			}
			events := decodeTransfers(t, "1", replacement...)
			require.NoError(t, sink.StoreWindow(ctx, "1", 7, "B-7", events))
			restarted, err := postgres.NewSink(postgres.SinkConfig{Pool: pool, Handler: PaymentHandler{}, CopyThreshold: threshold})
			require.NoError(t, err)
			require.NoError(t, prepareApplication(ctx, pool))
			require.NoError(t, restarted.StoreWindow(ctx, "1", 7, "B-7", events))
			assertStatus(t, pool, "order-1", "paid", "200", "0", "0", 3)
			height, hash, err := restarted.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(7), height)
			assert.Equal(t, "B-7", hash)
			require.NoError(t, restarted.Rollback(ctx, "1", 0, ""))
			s = assertStatus(t, pool, "order-1", "pending", "0", "200", "0", 0)
			assert.Equal(t, original, s.Invoice, "invoice terms survive rollback")
			assert.Nil(t, s.LastPaymentBlock)
		})
	}
}

func TestPaymentsMatchChainTokenRecipientAndRealIncomingValue(t *testing.T) {
	pool, sink := setup(t, 1)
	ctx := context.Background()
	for _, i := range []Invoice{
		invoice("main", "1", tokenA, depositA, "10", 2), invoice("other-token", "1", tokenB, depositA, "10", 1),
		invoice("other-chain", "2", tokenA, depositA, "10", 1), invoice("other-recipient", "1", tokenA, depositB, "10", 1),
	} {
		_, err := registerInvoice(ctx, pool, i)
		require.NoError(t, err)
	}
	events := decodeTransfers(t, "1", transferLog(1, 0, payer, depositA, big.NewInt(100)),
		transferLog(2, 0, depositA, depositA, big.NewInt(10)), transferLog(2, 1, zero, depositA, big.NewInt(10)),
		transferLog(2, 2, payer, depositA, big.NewInt(0)), transferLog(2, 3, payer, depositB, big.NewInt(7)),
		transferLog(2, 4, payer, depositA, big.NewInt(4)), transferLog(2, 5, depositA, payer, big.NewInt(9)))
	require.NoError(t, sink.Store(ctx, events))
	otherToken := transferLog(2, 6, payer, depositA, big.NewInt(10))
	otherToken.Address = tokenB
	require.NoError(t, sink.Store(ctx, decodeTransfers(t, "1", otherToken)))
	require.NoError(t, sink.Store(ctx, decodeTransfers(t, "2", transferLog(2, 0, payer, depositA, big.NewInt(10)))))
	assertStatus(t, pool, "main", "partially_paid", "4", "6", "0", 1)
	assertStatus(t, pool, "other-token", "paid", "10", "0", "0", 1)
	assertStatus(t, pool, "other-chain", "paid", "10", "0", "0", 1)
	assertStatus(t, pool, "other-recipient", "partially_paid", "7", "3", "0", 1)
	require.NoError(t, sink.Rollback(ctx, "1", 2, "A-1"))
	assertStatus(t, pool, "main", "pending", "0", "10", "0", 0)
	assertStatus(t, pool, "other-token", "pending", "0", "10", "0", 0)
	assertStatus(t, pool, "other-chain", "paid", "10", "0", "0", 1)
}

func TestInvoiceRetriesAndAddressReuse(t *testing.T) {
	pool, _ := setup(t, 1)
	ctx := context.Background()
	first := invoice("order-1", "1", tokenA, depositA, "100", 10)
	stored, err := registerInvoice(ctx, pool, first)
	require.NoError(t, err)
	assert.Equal(t, first, stored)
	retry := first
	retry.AcceptFromBlock = 20
	stored, err = registerInvoice(ctx, pool, retry)
	require.NoError(t, err)
	assert.Equal(t, first, stored, "retries preserve the original acceptance block")
	changed := first
	changed.ExpectedAmount = "99"
	_, err = registerInvoice(ctx, pool, changed)
	assert.Error(t, err)
	reused := first
	reused.ID = "order-2"
	_, err = registerInvoice(ctx, pool, reused)
	assert.Error(t, err, "the same chain/token deposit address cannot pay two invoices")
	assertStatus(t, pool, "order-1", "pending", "0", "100", "0", 0)
}

func TestPaymentAggregationPreservesFullPrecision(t *testing.T) {
	pool, sink := setup(t, 1)
	ctx := context.Background()
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	_, err := registerInvoice(ctx, pool, invoice("large", "1", tokenA, depositA, maximum.String(), 1))
	require.NoError(t, err)
	var logs []godex.Log
	for i := uint64(0); i < 10; i++ {
		logs = append(logs, transferLog(1, i, payer, depositA, maximum))
	}
	require.NoError(t, sink.Store(ctx, decodeTransfers(t, "1", logs...)))
	total := new(big.Int).Mul(maximum, big.NewInt(10))
	excess := new(big.Int).Mul(maximum, big.NewInt(9))
	assertStatus(t, pool, "large", "overpaid", total.String(), "0", excess.String(), 10)
}

func TestPaymentFailuresAreAtomic(t *testing.T) {
	for _, threshold := range []int{1, 100} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			pool, sink := setup(t, threshold)
			ctx := context.Background()
			_, err := registerInvoice(ctx, pool, invoice("order", "1", tokenA, depositA, "10", 1))
			require.NoError(t, err)
			for _, tc := range []struct {
				name, field string
				value       any
			}{
				{"missing sender", "from", nil}, {"missing recipient", "to", nil}, {"wrong recipient type", "to", 3},
				{"malformed amount", "value", "invalid"}, {"negative amount", "value", big.NewInt(-1)},
				{"overflow amount", "value", new(big.Int).Lsh(big.NewInt(1), 256)}, {"unsupported event", "", nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					events := decodeTransfers(t, "1", transferLog(1, 0, payer, depositA, big.NewInt(5)), transferLog(1, 1, payer, depositA, big.NewInt(5)))
					if tc.field == "" {
						events[1].EventType = "Approval"
					} else if tc.value == nil {
						delete(events[1].Fields, tc.field)
					} else {
						events[1].Fields[tc.field] = tc.value
					}
					assert.Error(t, sink.Store(ctx, events))
					assertStatus(t, pool, "order", "pending", "0", "10", "0", 0)
					var count int
					require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM chronicle_events").Scan(&count))
					assert.Zero(t, count)
				})
			}
			require.NoError(t, sink.Store(ctx, decodeTransfers(t, "1", transferLog(1, 0, payer, depositA, big.NewInt(10)))))
			_, err = pool.Exec(ctx, "ALTER TABLE chronicle_cursors ADD CHECK(block_num>0)")
			require.NoError(t, err)
			assert.Error(t, sink.Rollback(ctx, "1", 1, ""))
			assertStatus(t, pool, "order", "paid", "10", "0", "0", 1)
			height, hash, err := sink.LoadCursor(ctx, "1")
			require.NoError(t, err)
			assert.Equal(t, uint64(1), height)
			assert.Equal(t, "A-1", hash)
		})
	}
}
