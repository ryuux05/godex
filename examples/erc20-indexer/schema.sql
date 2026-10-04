-- Custom tables for ERC20 processing
-- Startup initializes these tables; handler writes and rollback share the sink transaction.

CREATE TABLE IF NOT EXISTS erc20_transfer_stats (
    id BIGSERIAL PRIMARY KEY,
    chain_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    block_hash TEXT NOT NULL,
    log_index INT NOT NULL,
    contract_address TEXT NOT NULL,
    from_address TEXT NOT NULL,
    to_address TEXT NOT NULL,
    value TEXT NOT NULL,  -- Store as string to handle large numbers
    block_num BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS erc20_approvals (
    id BIGSERIAL PRIMARY KEY,
    chain_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    block_hash TEXT NOT NULL,
    log_index INT NOT NULL,
    contract_address TEXT NOT NULL,
    owner_address TEXT NOT NULL,
    spender_address TEXT NOT NULL,
    value TEXT NOT NULL,
    block_num BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS erc20_balances (
    chain_id TEXT NOT NULL,
    contract_address TEXT NOT NULL,
    holder_address TEXT NOT NULL,
    last_transfer_block BIGINT NOT NULL,
    PRIMARY KEY (chain_id, contract_address, holder_address)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_erc20_transfers_contract ON erc20_transfer_stats(contract_address);
CREATE INDEX IF NOT EXISTS idx_erc20_transfers_addresses ON erc20_transfer_stats(from_address, to_address);
CREATE INDEX IF NOT EXISTS idx_erc20_transfers_block ON erc20_transfer_stats(block_num);
CREATE INDEX IF NOT EXISTS idx_erc20_approvals_contract ON erc20_approvals(contract_address);
CREATE INDEX IF NOT EXISTS idx_erc20_approvals_owner ON erc20_approvals(owner_address);
CREATE INDEX IF NOT EXISTS idx_erc20_approvals_block ON erc20_approvals(block_num);


CREATE UNIQUE INDEX IF NOT EXISTS erc20_transfer_stats_chain_event_idx ON erc20_transfer_stats (chain_id,event_id);

CREATE UNIQUE INDEX IF NOT EXISTS erc20_approvals_chain_event_idx ON erc20_approvals (chain_id,event_id);
