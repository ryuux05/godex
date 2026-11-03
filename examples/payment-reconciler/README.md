# Reconcile stablecoin checkout payments

Connect a checkout's off-chain invoices to incoming ERC20 transfers. Create an
invoice for an order, watch its deposit address, and read whether the customer
has paid nothing, part of the amount, the exact amount, or too much.

For example, a checkout expecting 25 tokens with six decimals records an amount
of `25000000`. Two transfers of `10000000` and `15000000` pay that invoice. A
later transfer of `2000000` appears as an overpayment. A reorg that removes a
payment reopens the invoice without deleting the order's payment terms.

This example uses the [ERC20 Transfer event](https://eips.ethereum.org/EIPS/eip-20).
Amounts are exact integers in the selected token's raw units. Choose a token
whose Transfer values represent the units credited to the recipient.

The payment callback receives a generated `bindings.ERC20Transfer`, with typed
addresses and an exact `*big.Int` value. Regenerate its binding after ABI changes:

```sh
go generate ./examples/payment-reconciler
```

See [typed handler generation](../../docs/codegen.md) to generate bindings for
another ABI. Generated files are committed, so running the example does not
require a generation step.

## Set up the checkout database

From the repository root, start PostgreSQL and initialize the invoice schema:

```sh
docker compose -f examples/payment-reconciler/docker-compose.yml up -d --wait
export DATABASE_URL='postgres://godex:godex-example@127.0.0.1:55442/payments?sslmode=disable'
go run ./examples/payment-reconciler init
```

Set your RPC endpoint, chain ID, and token contract. Replace the placeholders:

```sh
export RPC_URL='https://YOUR_RPC_ENDPOINT'
export CHAIN_ID='1'
export CONTRACT_ADDRESS='YOUR_STABLECOIN_CONTRACT_ADDRESS'
export FROM_BLOCK='YOUR_CHECKOUT_LAUNCH_BLOCK'
export CONFIRMATION_DEPTH='12'
```

Set `FROM_BLOCK` at or before the first invoice's acceptance block. The setting
is inclusive on a fresh database and does not rewind saved progress. Zero
resumes, or starts at block 1 without a cursor. Use a dedicated database or
schema for this watcher; Godex keeps one cursor per chain in that namespace.

## Register an order's invoice

Allocate a fresh deposit address from your wallet service. Create the invoice
before you give the customer that address:

```sh
go run ./examples/payment-reconciler invoice \
  -id order-1042 \
  -to YOUR_FRESH_DEPOSIT_ADDRESS \
  -amount 25000000
```

The command verifies the RPC chain and returns JSON containing the order ID,
token address, deposit address, expected amount, and `accept_from_block`. The
acceptance block is the current RPC head plus one. Earlier transfers cannot pay
the invoice. The amount above means 25 tokens only when that token has six
decimals; the command does not assume a decimal count or dollar price.

The same chain, token contract, and deposit address can belong to only one
invoice, including after it is paid. Retries with the same ID and payment terms
return the original invoice, including its original acceptance block. Changing
the amount, address, chain, or token under an existing ID fails.

Address allocation and key custody belong to your wallet service. This example
stores public addresses and observes transfers; it does not create keys, send
funds, issue refunds, or fulfill orders.

## Watch and read payment status

Start the watcher in one terminal:

```sh
go run ./examples/payment-reconciler watch
```

In another terminal with the same environment, inspect the order:

```sh
go run ./examples/payment-reconciler status -id order-1042
```

The response includes `expected_amount`, `received_amount`, `remaining_amount`,
`overpaid_amount`, `payment_count`, `last_payment_block`, and `status`. All amount
fields are decimal strings, so JSON consumers retain their integer precision.

For the invoice above, the statuses are:

| Received raw units | Status | Remaining raw units | Overpaid raw units |
| ---: | --- | ---: | ---: |
| 0 | `pending` | 25000000 | 0 |
| 10000000 | `partially_paid` | 15000000 | 0 |
| 25000000 | `paid` | 0 | 0 |
| 27000000 | `overpaid` | 0 | 2000000 |

New invoices are picked up while the watcher runs. Register each invoice before
funding its address. Existing transfers to unregistered addresses are not
backfilled by creating an invoice later.

Confirmation depth defaults to 12 blocks. Choose the depth for your network;
zero includes unconfirmed blocks. Status describes the currently indexed chain,
and a later reorg can change it. A fulfillment service must account for that
possibility when it acts on a paid invoice.

Press Ctrl+C to stop. Run `watch` again to resume the durable cursor. Running the
program without a subcommand also starts the watcher.

## Inspect payment evidence

Connect with `psql "$DATABASE_URL"` and read the reconciliation view:

```sql
SELECT invoice_id, expected_amount, received_amount, remaining_amount,
       overpaid_amount, status, payment_count, last_payment_block
FROM payment_invoice_status
WHERE invoice_id = 'order-1042';
```

Inspect incoming transfers for the invoice, including rows excluded from its
paid amount:

```sql
SELECT p.event_id, p.block_num, p.log_index, p.tx_hash,
       p.from_address, p.to_address, p.amount
FROM payment_transfers p
JOIN payment_invoices i
  ON p.chain_id = i.chain_id AND p.token_address = i.token_address
  AND p.to_address = i.deposit_address
WHERE i.invoice_id = 'order-1042'
ORDER BY p.block_num, p.log_index;
```

Only positive incoming transfers at or after `accept_from_block` count. Token
mints from the zero address and self-transfers do not count. Payments are scoped
by chain ID, token contract, and recipient address. Any sender may pay an invoice,
including an exchange withdrawal address. Outgoing sweeps do not reopen an
invoice because the view tracks gross receipts rather than the wallet balance.

## Follow indexing and reorg recovery

`recipientRPC` extends the HTTP adapter with an indexed recipient-topic filter.
It reloads invoice addresses for each range, requests at most 128 recipients per
RPC call, and merges batches in block/log order. An empty recipient set skips the
log request. A failed recipient batch fails the entire window rather than
committing a partial payment history. Adjust the batch size for your provider.

`PaymentHandler` stores transfers in the same PostgreSQL transaction as Godex
events and cursor advancement. Replayed event IDs do not add another payment.
Rollback removes transfers from the first orphaned block inclusively. The status
view then recomputes the paid amount from surviving history, while invoices stay
intact. No external fulfillment or refund runs inside that transaction.

The example watches one configured token per process. More tokens on the same
chain require one processor with all their routes and corresponding recipient
filters, or separate namespaces. It does not implement invoice expiration,
exchange rates, fee reconciliation, or a public checkout API.

## Verify the checkout flow

Use an isolated test database as described in [testing.md](../../docs/testing.md):

```sh
POSTGRES_TEST_DSN='postgres://USER:PASSWORD@HOST:PORT/TEST_DB?sslmode=disable' \
  go test -race -count=1 -v ./examples/payment-reconciler
```

Tests decode ERC20 logs and use the PostgreSQL sink under INSERT and COPY. They
cover split payments, overpayments, replay, multiple logs in one transaction,
wrong chains and tokens, acceptance blocks, ignored transfers, immutable invoice
retries, address reuse, large exact totals, failed transactions, and reorgs.
The local HTTP fixture runs invoice creation, status reads, live registration,
targeted fetching, cancellation, and restart without a blockchain endpoint or
funded wallet. Recipient-filter tests cover batching, ordering, malformed
responses, and failure after an earlier batch succeeded.
