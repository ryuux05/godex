-- Uniswap Swap Events Table (Updated for V4)
CREATE TABLE IF NOT EXISTS uniswap_swaps (
    id BIGSERIAL PRIMARY KEY,
    chain_id VARCHAR(20) NOT NULL,
    contract_address VARCHAR(42) NOT NULL,
    tx_hash VARCHAR(66) NOT NULL,
    block_number BIGINT NOT NULL,
    timestamp BIGINT,
    block_timestamp TIMESTAMP, -- Human-readable timestamp
    
    -- Pool information
    pool_id VARCHAR(66), -- bytes32 as hex string
    fee_tier INTEGER, -- uint24 fee (e.g., 500 = 0.05%)
    
    -- Swap participants
    sender VARCHAR(42) NOT NULL,
    recipient VARCHAR(42) NOT NULL,
    version VARCHAR(10) NOT NULL, -- 'V4'
    
    -- Amounts (raw)
    amount0_raw TEXT NOT NULL, -- Original int128 value
    amount1_raw TEXT NOT NULL, -- Original int128 value
    
    -- Amounts (absolute values for volume)
    amount0_abs NUMERIC(78, 0) NOT NULL, -- Absolute value for volume calculation
    amount1_abs NUMERIC(78, 0) NOT NULL, -- Absolute value for volume calculation
    
    -- Swap direction (computed from amounts)
    direction VARCHAR(10), -- 'buy' or 'sell' (based on amount0/amount1 signs)
    
    -- V4 specific fields
    sqrt_price_x96 TEXT,
    price NUMERIC(78, 18), -- Computed price from sqrtPriceX96 (optional)
    liquidity TEXT,
    tick INTEGER, -- int24
    
    -- Legacy V2/V3 fields (kept for compatibility, NULL for V4)
    amount0_in TEXT,
    amount1_in TEXT,
    amount0_out TEXT,
    amount1_out TEXT,
    
    created_at TIMESTAMP DEFAULT NOW(),
    
    -- Unique constraint per chain
    UNIQUE(chain_id, tx_hash, contract_address, block_number)
);

-- Add indexes
CREATE INDEX IF NOT EXISTS idx_swaps_chain ON uniswap_swaps(chain_id);
CREATE INDEX IF NOT EXISTS idx_swaps_pool ON uniswap_swaps(pool_id);
CREATE INDEX IF NOT EXISTS idx_swaps_contract ON uniswap_swaps(contract_address);
CREATE INDEX IF NOT EXISTS idx_swaps_block ON uniswap_swaps(block_number);
CREATE INDEX IF NOT EXISTS idx_swaps_tx ON uniswap_swaps(tx_hash);
CREATE INDEX IF NOT EXISTS idx_swaps_sender ON uniswap_swaps(sender);
CREATE INDEX IF NOT EXISTS idx_swaps_timestamp ON uniswap_swaps(block_timestamp);
CREATE INDEX IF NOT EXISTS idx_swaps_direction ON uniswap_swaps(direction);

-- Pool Statistics Table
CREATE TABLE IF NOT EXISTS uniswap_pool_stats (
    chain_id VARCHAR(20) NOT NULL,
    contract_address VARCHAR(42) NOT NULL,
    last_swap_block BIGINT NOT NULL,
    swap_count BIGINT DEFAULT 0,
    total_volume0 NUMERIC DEFAULT 0,  -- Remove (78, 0) to allow unlimited precision
    total_volume1 NUMERIC DEFAULT 0,  -- Remove (78, 0) to allow unlimited precision
    updated_at TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (chain_id, contract_address)
);

-- Index for pool stats
CREATE INDEX IF NOT EXISTS idx_pool_stats_block ON uniswap_pool_stats(chain_id, last_swap_block);

-- Swap Connections Table - tracks related swaps across chains
CREATE TABLE IF NOT EXISTS swap_connections (
    id BIGSERIAL PRIMARY KEY,
    swap_id_1 BIGINT NOT NULL REFERENCES uniswap_swaps(id) ON DELETE CASCADE,
    swap_id_2 BIGINT NOT NULL REFERENCES uniswap_swaps(id) ON DELETE CASCADE,
    connection_type VARCHAR(50) NOT NULL, -- 'same_sender', 'same_recipient', 'cross_chain', 'time_window'
    time_diff_seconds BIGINT, -- Time difference between swaps
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(swap_id_1, swap_id_2)
);

-- Indexes for swap connections
CREATE INDEX IF NOT EXISTS idx_connections_swap1 ON swap_connections(swap_id_1);
CREATE INDEX IF NOT EXISTS idx_connections_swap2 ON swap_connections(swap_id_2);
CREATE INDEX IF NOT EXISTS idx_connections_type ON swap_connections(connection_type);

