package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Invoice struct {
	ID              string `json:"invoice_id"`
	ChainID         string `json:"chain_id"`
	TokenAddress    string `json:"token_address"`
	DepositAddress  string `json:"deposit_address"`
	ExpectedAmount  string `json:"expected_amount"`
	AcceptFromBlock uint64 `json:"accept_from_block"`
}

type InvoiceStatus struct {
	Invoice
	ReceivedAmount   string  `json:"received_amount"`
	RemainingAmount  string  `json:"remaining_amount"`
	OverpaidAmount   string  `json:"overpaid_amount"`
	Status           string  `json:"status"`
	PaymentCount     int64   `json:"payment_count"`
	LastPaymentBlock *uint64 `json:"last_payment_block"`
}

// registerInvoice preserves the first acceptance block when a checkout retries
// the same invoice. Reusing an ID with different terms or an address fails.
func registerInvoice(ctx context.Context, pool *pgxpool.Pool, invoice Invoice) (Invoice, error) {
	err := pool.QueryRow(ctx, `INSERT INTO payment_invoices
		(invoice_id,chain_id,token_address,deposit_address,expected_amount,accept_from_block)
		VALUES($1,$2,$3,$4,$5::numeric,$6)
		ON CONFLICT (invoice_id) DO UPDATE SET invoice_id=EXCLUDED.invoice_id
		WHERE payment_invoices.chain_id=EXCLUDED.chain_id
		AND payment_invoices.token_address=EXCLUDED.token_address
		AND payment_invoices.deposit_address=EXCLUDED.deposit_address
		AND payment_invoices.expected_amount=EXCLUDED.expected_amount
		RETURNING accept_from_block`, invoice.ID, invoice.ChainID, invoice.TokenAddress,
		invoice.DepositAddress, invoice.ExpectedAmount, invoice.AcceptFromBlock).Scan(&invoice.AcceptFromBlock)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, fmt.Errorf("invoice %q already exists with different payment terms", invoice.ID)
	}
	if err != nil {
		return Invoice{}, fmt.Errorf("register invoice: %w", err)
	}
	return invoice, nil
}

func readStatus(ctx context.Context, pool *pgxpool.Pool, id string) (InvoiceStatus, error) {
	var status InvoiceStatus
	err := pool.QueryRow(ctx, `SELECT invoice_id,chain_id,token_address,deposit_address,
		expected_amount::text,accept_from_block,received_amount::text,remaining_amount::text,
		overpaid_amount::text,status,payment_count,last_payment_block
		FROM payment_invoice_status WHERE invoice_id=$1`, id).Scan(
		&status.ID, &status.ChainID, &status.TokenAddress, &status.DepositAddress,
		&status.ExpectedAmount, &status.AcceptFromBlock, &status.ReceivedAmount,
		&status.RemainingAmount, &status.OverpaidAmount, &status.Status, &status.PaymentCount, &status.LastPaymentBlock)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoiceStatus{}, fmt.Errorf("invoice %q not found", id)
	}
	return status, err
}

func parseAddress(text string) (string, error) {
	text = strings.ToLower(text)
	if !strings.HasPrefix(text, "0x") || len(text) != 42 {
		return "", fmt.Errorf("expected a nonzero 20-byte address with 0x prefix")
	}
	b, err := hex.DecodeString(text[2:])
	if err != nil || new(big.Int).SetBytes(b).Sign() == 0 {
		return "", fmt.Errorf("expected a nonzero 20-byte hexadecimal address")
	}
	return text, nil
}

func parseAmount(text string) (string, error) {
	for _, c := range text {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("amount must be a positive uint256 integer in raw token units")
		}
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok || n.Sign() <= 0 || n.BitLen() > 256 {
		return "", fmt.Errorf("amount must be a positive uint256 integer in raw token units")
	}
	return n.String(), nil
}

func canonicalChainID(text string) (string, error) {
	base := 10
	if strings.HasPrefix(text, "0x") {
		base, text = 16, text[2:]
	}
	for _, c := range text {
		if c >= '0' && c <= '9' || base == 16 && c >= 'a' && c <= 'f' || base == 16 && c >= 'A' && c <= 'F' {
			continue
		}
		return "", fmt.Errorf("chain ID must be a nonnegative decimal or hexadecimal integer")
	}
	n, ok := new(big.Int).SetString(text, base)
	if !ok {
		return "", fmt.Errorf("chain ID must be a nonnegative decimal or hexadecimal integer")
	}
	return n.String(), nil
}
