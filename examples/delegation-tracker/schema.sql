CREATE TABLE IF NOT EXISTS delegation_changes (
	chain_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	contract_address TEXT NOT NULL,
	delegator TEXT NOT NULL,
	from_delegate TEXT NOT NULL,
	to_delegate TEXT NOT NULL,
	block_num BIGINT NOT NULL,
	log_index INT NOT NULL,
	tx_hash TEXT NOT NULL,
	PRIMARY KEY (chain_id, event_id)
);

CREATE INDEX IF NOT EXISTS delegation_changes_latest
	ON delegation_changes (chain_id, contract_address, delegator, block_num DESC, log_index DESC);
CREATE INDEX IF NOT EXISTS delegation_changes_rollback
	ON delegation_changes (chain_id, block_num);

CREATE TABLE IF NOT EXISTS delegation_vote_changes (
	chain_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	contract_address TEXT NOT NULL,
	delegate TEXT NOT NULL,
	previous_votes NUMERIC(78, 0) NOT NULL CHECK (previous_votes >= 0),
	new_votes NUMERIC(78, 0) NOT NULL CHECK (new_votes >= 0),
	block_num BIGINT NOT NULL,
	log_index INT NOT NULL,
	tx_hash TEXT NOT NULL,
	PRIMARY KEY (chain_id, event_id)
);

CREATE INDEX IF NOT EXISTS delegation_vote_changes_latest
	ON delegation_vote_changes (chain_id, contract_address, delegate, block_num DESC, log_index DESC);
CREATE INDEX IF NOT EXISTS delegation_vote_changes_rollback
	ON delegation_vote_changes (chain_id, block_num);

-- Views read the latest surviving event, so deleting orphaned history also
-- restores current state. Log index resolves multiple changes in one block.
CREATE OR REPLACE VIEW delegation_current AS
SELECT DISTINCT ON (chain_id, contract_address, delegator)
	chain_id, contract_address, delegator, to_delegate AS delegate,
	block_num, log_index, tx_hash
FROM delegation_changes
ORDER BY chain_id, contract_address, delegator, block_num DESC, log_index DESC;

CREATE OR REPLACE VIEW delegation_voting_power AS
SELECT DISTINCT ON (chain_id, contract_address, delegate)
	chain_id, contract_address, delegate, new_votes AS votes,
	block_num, log_index, tx_hash
FROM delegation_vote_changes
ORDER BY chain_id, contract_address, delegate, block_num DESC, log_index DESC;
