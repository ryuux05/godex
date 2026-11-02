# Track governance delegation

This example indexes a Votes-compatible token into PostgreSQL. Use it to find
an account's delegate, rank delegates by voting power, and inspect delegation
history. It handles redelegation, self-delegation, undelegation, and voting-power
changes caused by transfers.

The embedded ABI follows OpenZeppelin's
[IVotes events](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/v5.4.0/contracts/governance/utils/IVotes.sol).
It works with `ERC20Votes` and `ERC721Votes` contracts that emit these events.
This example tracks governance delegation. Staking and validator delegation
protocols need their own ABIs and handlers.

## Run the tracker

From the repository root, start PostgreSQL:

```sh
docker compose -f examples/delegation-tracker/docker-compose.yml up -d --wait
export DATABASE_URL='postgres://godex:godex-example@127.0.0.1:55440/delegation?sslmode=disable'
```

Set your RPC endpoint, token address, and token deployment block. Replace the
placeholder values before running these commands:

```sh
export RPC_URL='https://YOUR_RPC_ENDPOINT'
export CHAIN_ID='1'
export CONTRACT_ADDRESS='YOUR_VOTES_TOKEN_ADDRESS'
export FROM_BLOCK='YOUR_TOKEN_DEPLOYMENT_BLOCK'
export CONFIRMATION_DEPTH='12'
go run ./examples/delegation-tracker
```

The tracker creates its tables and views automatically. After it catches up,
`chronicle_cursors` contains the last indexed window. Press Ctrl+C to stop.
Run the same command to resume stored progress. `FROM_BLOCK` is inclusive on a
fresh database and does not rewind existing progress. A `FROM_BLOCK` of zero
resumes stored progress or starts at block 1. Confirmation depth defaults to 12
blocks; choose a value for your network. Zero indexes unconfirmed blocks.

Use a dedicated database or schema for each independently running tracker. The
processor stores one cursor per chain, so two trackers for different tokens on
the same chain must not share that cursor. Start at deployment for complete
state. A later starting block omits accounts and vote balances that have not
changed since that block.

## Query delegation

Connect to the database:

```sh
psql "$DATABASE_URL"
```

Set the token address in lowercase and the account to inspect:

```sql
\set chain_id '1'
\set token 'YOUR_LOWERCASE_TOKEN_ADDRESS'
\set account 'YOUR_LOWERCASE_ACCOUNT_ADDRESS'

SELECT delegator, delegate, block_num, tx_hash
FROM delegation_current
WHERE chain_id = :'chain_id' AND contract_address = :'token'
  AND delegator = :'account';
```

The zero address means the account explicitly undelegated. No row means no
delegation change was indexed for that account.

Rank delegates by voting power and count their current delegators:

```sql
SELECT p.delegate, p.votes, count(d.delegator) AS delegator_count
FROM delegation_voting_power p
LEFT JOIN delegation_current d
  ON d.chain_id = p.chain_id AND d.contract_address = p.contract_address
  AND d.delegate = p.delegate
WHERE p.chain_id = :'chain_id' AND p.contract_address = :'token'
  AND p.votes > 0
  AND p.delegate <> '0x0000000000000000000000000000000000000000'
GROUP BY p.delegate, p.votes
ORDER BY p.votes DESC, p.delegate
LIMIT 20;
```

Voting power comes from `DelegateVotesChanged.newVotes`. Delegator counts do not
measure voting power. Votes remain exact integers in the token's voting units;
the tracker does not convert them to decimal token amounts or percentages of
total supply.

Inspect an account's delegation history in chain order:

```sql
SELECT from_delegate, to_delegate, block_num, log_index, tx_hash
FROM delegation_changes
WHERE chain_id = :'chain_id' AND contract_address = :'token'
  AND delegator = :'account'
ORDER BY block_num, log_index;
```

Find a delegate's voting power at the end of an indexed block:

```sql
\set at_block 'YOUR_INDEXED_BLOCK_NUMBER'

SELECT new_votes AS votes, block_num, log_index
FROM delegation_vote_changes
WHERE chain_id = :'chain_id' AND contract_address = :'token'
  AND delegate = :'account' AND block_num <= :'at_block'::bigint
ORDER BY block_num DESC, log_index DESC
LIMIT 1;
```

This query uses chain block numbers, including for tokens whose governance clock
uses timestamps. No row means no balance change was indexed before that block.

## Follow reorg recovery

`DelegationHandler` writes history inside the same transaction as Godex event
storage and cursor advancement. Replayed event IDs do not invoke the handler
again. Each history row includes chain, contract, block, log index, and transaction.
Separate logs in one transaction remain separate changes.

`delegation_current` and `delegation_voting_power` are SQL views over the latest
surviving history. Rollback deletes history from the first orphaned block,
inclusively, in the sink's transaction. The views then expose the previous state.
Other chains remain intact. These views favor a small example implementation;
benchmark their query cost before using them for a large governance dashboard.

## Verify the example

Use an isolated test database as described in [testing.md](../../docs/testing.md):

```sh
POSTGRES_TEST_DSN='postgres://USER:PASSWORD@HOST:PORT/TEST_DB?sslmode=disable' \
  go test -race -count=1 ./examples/delegation-tracker
```

Tests decode ABI logs and use the real PostgreSQL sink under INSERT and COPY.
They cover voting balances beyond 64 bits, same-block ordering, replay,
redelegation, undelegation, self-delegation, transfers, rollback, replacement
history, chain and contract isolation, failed transactions, and restart. A local
HTTP RPC fixture also runs the actual service entry point through indexing,
cancellation, and resume without a blockchain endpoint.
