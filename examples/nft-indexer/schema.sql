CREATE TABLE IF NOT EXISTS nft_transfers (
	chain_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	contract_address TEXT NOT NULL,
	token_id NUMERIC(78, 0) NOT NULL CHECK (token_id >= 0),
	from_address TEXT NOT NULL,
	to_address TEXT NOT NULL,
	block_num BIGINT NOT NULL,
	log_index INT NOT NULL,
	tx_hash TEXT NOT NULL,
	PRIMARY KEY (chain_id, event_id)
);

CREATE INDEX IF NOT EXISTS nft_transfers_latest
	ON nft_transfers (chain_id, contract_address, token_id, block_num DESC, log_index DESC);
CREATE INDEX IF NOT EXISTS nft_transfers_rollback ON nft_transfers (chain_id, block_num);

CREATE OR REPLACE VIEW nft_token_state AS
SELECT DISTINCT ON (chain_id, contract_address, token_id)
	chain_id, contract_address, token_id, to_address AS owner,
	block_num, log_index, tx_hash
FROM nft_transfers
ORDER BY chain_id, contract_address, token_id, block_num DESC, log_index DESC;

-- Filter burns after selecting the latest transfer, or an earlier owner would
-- incorrectly reappear when the token is burned.
CREATE OR REPLACE VIEW nft_owners AS
SELECT * FROM nft_token_state
WHERE owner <> '0x0000000000000000000000000000000000000000';

CREATE OR REPLACE VIEW nft_wallet_balances AS
SELECT chain_id, contract_address, owner, count(*) AS token_count
FROM nft_owners
GROUP BY chain_id, contract_address, owner;
