package types_test

import (
	"encoding/json"
	"math"
	"math/big"
	"testing"

	"github.com/ryuux05/godex/pkg/godex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExactIntegerAccessors(t *testing.T) {
	large := new(big.Int).Lsh(big.NewInt(1), 255)
	for _, value := range []any{large, *large, large.String(), json.Number(large.String())} {
		n, err := (godex.EventFields{"amount": value}).BigInt("amount")
		require.NoError(t, err)
		assert.Equal(t, large, n)
		n.SetInt64(0)
		assert.NotZero(t, large.Sign(), "returned integers must not alias the event")
	}
	for _, value := range []any{int(-1), int8(-1), int16(-1), int32(-1), int64(-1)} {
		n, err := (godex.EventFields{"amount": value}).Int64("amount")
		require.NoError(t, err)
		assert.Equal(t, int64(-1), n)
	}
	for _, value := range []any{uint(1), uint8(1), uint16(1), uint32(1), uint64(1)} {
		n, err := (godex.EventFields{"amount": value}).Uint64("amount")
		require.NoError(t, err)
		assert.Equal(t, uint64(1), n)
	}
	for _, value := range []any{float64(42), float32(42), true, (*big.Int)(nil), nil, "1.1", json.Number("1e20")} {
		_, err := (godex.EventFields{"amount": value}).BigInt("amount")
		assert.ErrorContains(t, err, "amount")
	}
}

func TestIntegerAccessorsRejectOverflow(t *testing.T) {
	f := godex.EventFields{"max": uint64(math.MaxUint64), "min": int64(math.MinInt64), "huge": new(big.Int).Lsh(big.NewInt(1), 64), "negative": int64(-1)}
	n, err := f.Uint64("max")
	require.NoError(t, err)
	assert.Equal(t, uint64(math.MaxUint64), n)
	signed, err := f.Int64("min")
	require.NoError(t, err)
	assert.Equal(t, int64(math.MinInt64), signed)
	_, err = f.Uint64("huge")
	assert.ErrorContains(t, err, "uint64 range")
	_, err = f.Uint64("negative")
	assert.ErrorContains(t, err, "uint64 range")
	_, err = f.Int64("max")
	assert.ErrorContains(t, err, "int64 range")
	f["underflow"] = new(big.Int).Sub(big.NewInt(math.MinInt64), big.NewInt(1))
	_, err = f.Int64("underflow")
	assert.ErrorContains(t, err, "int64 range")
}

func TestAddressAndBytes32Accessors(t *testing.T) {
	address, err := (godex.EventFields{"account": "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}).Address("account")
	require.NoError(t, err)
	assert.Equal(t, godex.Address("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), address)
	address, err = (godex.EventFields{"account": string(godex.ZeroAddress)}).Address("account")
	require.NoError(t, err)
	assert.Equal(t, godex.ZeroAddress, address)
	for _, value := range []any{nil, 7, "0xabc", "0xzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		_, err := (godex.EventFields{"account": value}).Address("account")
		assert.Error(t, err)
	}
	original := make([]byte, 32)
	original[0] = 7
	hash, err := (godex.EventFields{"hash": original}).Bytes32("hash")
	require.NoError(t, err)
	assert.Equal(t, byte(7), hash[0])
	hash[0] = 9
	assert.Equal(t, byte(7), original[0])
	for _, value := range []any{[]byte{}, make([]byte, 31), make([]byte, 33), "0x01", nil} {
		_, err := (godex.EventFields{"hash": value}).Bytes32("hash")
		assert.Error(t, err)
	}
	_, err = (godex.EventFields{}).Address("missing")
	assert.ErrorIs(t, err, godex.ErrFieldNotFound)
	_, err = (godex.EventFields{}).Bytes32("missing")
	assert.ErrorIs(t, err, godex.ErrFieldNotFound)
}

func TestFieldAccessorsAreStrictAndCopyBytes(t *testing.T) {
	original := []byte{1, 2}
	fixed := [32]byte{3}
	f := godex.EventFields{"text": "", "flag": false, "bytes": original, "fixed": fixed, "hex": "0x0102", "badhex": "0x0", "nil": nil}
	text, err := f.String("text")
	require.NoError(t, err)
	assert.Empty(t, text)
	flag, err := f.Bool("flag")
	require.NoError(t, err)
	assert.False(t, flag)
	for _, name := range []string{"bytes", "hex"} {
		b, err := f.Bytes(name)
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2}, b)
		b[0] = 99
		assert.Equal(t, byte(1), original[0])
	}
	b, err := f.Bytes("fixed")
	require.NoError(t, err)
	assert.Equal(t, byte(3), b[0])
	_, err = f.Bytes("badhex")
	assert.Error(t, err)
	_, err = f.String("flag")
	assert.Error(t, err)
	_, err = f.Bool("text")
	assert.Error(t, err)
	_, err = f.String("nil")
	assert.Error(t, err)
	_, err = f.String("missing")
	assert.ErrorIs(t, err, godex.ErrFieldNotFound)
	_, err = f.Bool("missing")
	assert.ErrorIs(t, err, godex.ErrFieldNotFound)
	_, err = f.Bytes("missing")
	assert.ErrorIs(t, err, godex.ErrFieldNotFound)
	_, err = f.BigInt("missing")
	assert.ErrorIs(t, err, godex.ErrFieldNotFound)
}
