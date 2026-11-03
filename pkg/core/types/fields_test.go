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
