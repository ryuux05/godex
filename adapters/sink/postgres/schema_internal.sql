CREATE TABLE IF NOT EXISTS _schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chronicle_events (
  event_id      TEXT PRIMARY KEY,
  chain_id      TEXT NOT NULL,
  kind          TEXT NOT NULL,
  block_num     BIGINT NOT NULL,
  block_hash    TEXT NOT NULL,
  tx_hash       TEXT NOT NULL,
  log_index     INT NOT NULL,
  address       TEXT NOT NULL,
  ts            BIGINT NOT NULL,
  payload       JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS chronicle_events_chain_id_block_num_idx ON chronicle_events (chain_id, block_num);
CREATE INDEX IF NOT EXISTS chronicle_events_chain_id_kind_block_num_idx ON chronicle_events (chain_id, kind, block_num);
CREATE INDEX IF NOT EXISTS chronicle_events_chain_id_address_block_num_idx ON chronicle_events (chain_id, address, block_num);

CREATE TABLE IF NOT EXISTS chronicle_cursors (
  chain_id   TEXT NOT NULL,
  block_num  BIGINT NOT NULL,
  block_hash TEXT NOT NULL,
  PRIMARY KEY (chain_id)
);
