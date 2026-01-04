-- Custom tables for ERC20 processing
-- These tables are created by the handler within the same transaction as event storage

CREATE TABLE IF NOT EXISTS erc20_transfer_stats (
    id BIGSERIAL PRIMARY KEY,
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
    contract_address TEXT NOT NULL,
    owner_address TEXT NOT NULL,
    spender_address TEXT NOT NULL,
    value TEXT NOT NULL,
    block_num BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS erc20_balances (
    contract_address TEXT NOT NULL,
    holder_address TEXT NOT NULL,
    last_transfer_block BIGINT NOT NULL,
    PRIMARY KEY (contract_address, holder_address)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_erc20_transfers_contract ON erc20_transfer_stats(contract_address);
CREATE INDEX IF NOT EXISTS idx_erc20_transfers_addresses ON erc20_transfer_stats(from_address, to_address);
CREATE INDEX IF NOT EXISTS idx_erc20_transfers_block ON erc20_transfer_stats(block_num);
CREATE INDEX IF NOT EXISTS idx_erc20_approvals_contract ON erc20_approvals(contract_address);
CREATE INDEX IF NOT EXISTS idx_erc20_approvals_owner ON erc20_approvals(owner_address);
CREATE INDEX IF NOT EXISTS idx_erc20_approvals_block ON erc20_approvals(block_num);

