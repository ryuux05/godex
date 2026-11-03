CREATE TABLE IF NOT EXISTS payment_invoices (
	invoice_id TEXT PRIMARY KEY,
	chain_id TEXT NOT NULL,
	token_address TEXT NOT NULL,
	deposit_address TEXT NOT NULL,
	expected_amount NUMERIC(78, 0) NOT NULL CHECK (expected_amount > 0),
	accept_from_block BIGINT NOT NULL CHECK (accept_from_block > 0),
	UNIQUE (chain_id, token_address, deposit_address)
);

CREATE TABLE IF NOT EXISTS payment_transfers (
	chain_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	token_address TEXT NOT NULL,
	from_address TEXT NOT NULL,
	to_address TEXT NOT NULL,
	amount NUMERIC(78, 0) NOT NULL CHECK (amount >= 0),
	block_num BIGINT NOT NULL,
	log_index INT NOT NULL,
	tx_hash TEXT NOT NULL,
	PRIMARY KEY (chain_id, event_id)
);

CREATE INDEX IF NOT EXISTS payment_transfers_recipient
	ON payment_transfers (chain_id, token_address, to_address, block_num);
CREATE INDEX IF NOT EXISTS payment_transfers_rollback ON payment_transfers (chain_id, block_num);

-- Invoice terms survive reorgs. Only canonical, positive incoming transfers
-- count toward payment; mints and self-transfers are retained for inspection.
CREATE OR REPLACE VIEW payment_invoice_status AS
SELECT totals.*,
	GREATEST(expected_amount - received_amount, 0) AS remaining_amount,
	GREATEST(received_amount - expected_amount, 0) AS overpaid_amount,
	CASE
		WHEN received_amount = 0 THEN 'pending'
		WHEN received_amount < expected_amount THEN 'partially_paid'
		WHEN received_amount = expected_amount THEN 'paid'
		ELSE 'overpaid'
	END AS status
FROM (
	SELECT i.invoice_id, i.chain_id, i.token_address, i.deposit_address,
		i.expected_amount, i.accept_from_block,
		COALESCE(sum(p.amount), 0) AS received_amount,
		count(p.event_id) AS payment_count,
		max(p.block_num) AS last_payment_block
	FROM payment_invoices i
	LEFT JOIN payment_transfers p
		ON p.chain_id = i.chain_id AND p.token_address = i.token_address
		AND p.to_address = i.deposit_address AND p.block_num >= i.accept_from_block
		AND p.amount > 0 AND p.from_address <> p.to_address
		AND p.from_address <> '0x0000000000000000000000000000000000000000'
	GROUP BY i.invoice_id
) totals;
