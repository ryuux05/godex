# Track NFT ownership

Index an ERC721 collection into PostgreSQL to look up current owners, list a
wallet's NFTs, rank holders, and inspect mint, transfer, and burn history.
The example preserves full uint256 token IDs, including zero, and restores
ownership and balances after a reorg.

The embedded ABI follows the [ERC721 Transfer event](https://eips.ethereum.org/EIPS/eip-721).
Unlike ERC20's Transfer event, ERC721 indexes `tokenId` as a topic.

## Run the indexer

From the repository root, start PostgreSQL:

```sh
docker compose -f examples/nft-indexer/docker-compose.yml up -d --wait
export DATABASE_URL='postgres://godex:godex-example@127.0.0.1:55441/nft?sslmode=disable'
```

Replace the placeholders with your RPC endpoint, collection address, and
deployment block:

```sh
export RPC_URL='https://YOUR_RPC_ENDPOINT'
export CHAIN_ID='1'
export CONTRACT_ADDRESS='YOUR_ERC721_COLLECTION_ADDRESS'
export FROM_BLOCK='YOUR_COLLECTION_DEPLOYMENT_BLOCK'
export CONFIRMATION_DEPTH='12'
go run ./examples/nft-indexer
```

The indexer creates its schema automatically. Press Ctrl+C to stop, then run the
same command to resume the saved cursor. `FROM_BLOCK` includes the specified
block on a fresh database and does not rewind stored progress. Zero resumes, or
starts at block 1 when there is no cursor. Confirmation depth defaults to 12
blocks; set it for your network. Zero indexes unconfirmed blocks.

Use a dedicated database or schema for each independently running indexer.
Godex keeps one cursor per chain, so separate collection indexers must not share
that cursor.

## Query ownership and inventories

Connect with `psql "$DATABASE_URL"` and set your query parameters. Addresses are
stored in lowercase and token IDs are exact decimal integers:

```sql
\set chain_id '1'
\set collection 'YOUR_LOWERCASE_COLLECTION_ADDRESS'
\set wallet 'YOUR_LOWERCASE_WALLET_ADDRESS'
\set token_id '42'

SELECT owner, block_num, tx_hash
FROM nft_owners
WHERE chain_id = :'chain_id' AND contract_address = :'collection'
  AND token_id = :'token_id'::numeric;
```

A burned token has no row in `nft_owners`. Its latest burn remains in
`nft_token_state` with the zero address as owner. A missing token in both views
has no indexed transfer history.

List the NFTs currently owned by a wallet:

```sql
SELECT token_id, block_num, tx_hash
FROM nft_owners
WHERE chain_id = :'chain_id' AND contract_address = :'collection'
  AND owner = :'wallet'
ORDER BY token_id;
```

Rank current holders by the number of live tokens:

```sql
SELECT owner, token_count
FROM nft_wallet_balances
WHERE chain_id = :'chain_id' AND contract_address = :'collection'
ORDER BY token_count DESC, owner
LIMIT 20;
```

Self-transfers do not increase a wallet's balance. Burns remove a token from
inventories, and a later mint of the same ID restores it under its new owner.

## Query transfer history

Inspect a token's mint, transfer, and burn events in chain order:

```sql
SELECT CASE
    WHEN from_address = '0x0000000000000000000000000000000000000000' THEN 'mint'
    WHEN to_address = '0x0000000000000000000000000000000000000000' THEN 'burn'
    ELSE 'transfer'
  END AS activity,
  from_address, to_address, block_num, log_index, tx_hash
FROM nft_transfers
WHERE chain_id = :'chain_id' AND contract_address = :'collection'
  AND token_id = :'token_id'::numeric
ORDER BY block_num, log_index;
```

Find the token's owner at the end of an indexed block:

```sql
\set at_block 'YOUR_INDEXED_BLOCK_NUMBER'

SELECT to_address AS owner, block_num, log_index
FROM nft_transfers
WHERE chain_id = :'chain_id' AND contract_address = :'collection'
  AND token_id = :'token_id'::numeric AND block_num <= :'at_block'::bigint
ORDER BY block_num DESC, log_index DESC
LIMIT 1;
```

The zero address in this historical query means the token was burned. No row
means no transfer was indexed for that token before the specified block.

## Follow persistence and recovery

`NFTHandler` writes transfer history in the same PostgreSQL transaction as
Godex event storage and cursor advancement. The sink skips replayed event IDs.
Multiple transfers of one token in the same transaction remain separate rows,
and log index determines the final owner within a block.

The ownership and balance views read surviving transfer history. Rollback deletes
history from the first orphaned block inclusively, so an orphaned transfer,
burn, or remint no longer affects current state. Other chains remain intact.
The views favor a small implementation; benchmark their query cost before
serving a large collection dashboard.

Start at deployment for the full emitted Transfer history. ERC721 permits
constructor allocations without Transfer events; those tokens require an
initial ownership snapshot. This example does not process ERC2309
`ConsecutiveTransfer`, ERC1155 events, approvals, or token metadata.

## Verify the example

Use an isolated test database as described in [testing.md](../../docs/testing.md):

```sh
POSTGRES_TEST_DSN='postgres://USER:PASSWORD@HOST:PORT/TEST_DB?sslmode=disable' \
  go test -race -count=1 ./examples/nft-indexer
```

Tests decode raw ERC721 logs and use the real PostgreSQL sink under INSERT and
COPY. They cover mint, transfer, self-transfer, burn, remint, maximum uint256
IDs, zero IDs, balances, historical ownership, replay, same-block ordering,
inclusive rollback, replacement history, chain and collection isolation, and
failed transactions. A local HTTP RPC fixture also runs the service through
indexing, cancellation, and resume without a blockchain endpoint.
