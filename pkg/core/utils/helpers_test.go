package utils

import (
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHexQuantities(t *testing.T) {
	for _, n := range []uint64{0, 1, 15, 16, 1 << 63, math.MaxUint64} {
		got, err := HexQtyToUint64(Uint64ToHexQty(n))
		require.NoError(t, err)
		assert.Equal(t, n, got)
	}
	for _, tc := range []struct {
		s    string
		want uint64
	}{{"0Xff", 255}, {"42", 42}, {"0x00", 0}} {
		got, err := HexQtyToUint64(tc.s)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	for _, s := range []string{"", "0x", "-1", "0xgg", "0x10000000000000000", "18446744073709551616"} {
		_, err := HexQtyToUint64(s)
		assert.Error(t, err, s)
	}
}

func TestTopicConversion(t *testing.T) {
	const topic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	assert.Equal(t, topic, FunctionSignatureToTopic("Transfer( address, address, uint256 )"))
	assert.Equal(t, "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470", hex.EncodeToString(Keccak256(nil)))
	input := [][]string{{"Transfer(address,address,uint256)", topic, topic[2:]}, nil}
	assert.Equal(t, [][]string{{topic, topic, topic}, {}}, ConvertToTopics(input))
	assert.Equal(t, "Transfer(address,address,uint256)", input[0][0], "conversion must preserve caller input")
	assert.Empty(t, ConvertToTopics(nil))
}

func TestFormattingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		n    uint64
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1.0K"}, {1500, "1.5K"}, {1000000, "1.0M"}, {2500000, "2.5M"}} {
		assert.Equal(t, tc.want, FormatNumber(tc.n))
	}
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{0, "0s"}, {59 * time.Second, "59s"}, {time.Minute, "1m"}, {59 * time.Minute, "59m"}, {time.Hour, "1h 0m"}, {90 * time.Minute, "1h 30m"}} {
		assert.Equal(t, tc.want, FormatDuration(tc.d))
	}
	assert.Equal(t, "abcdef", Normalize("0xAbCdEf"))
	assert.Equal(t, "abcdef", Normalize("AbCdEf"))
	assert.Empty(t, Normalize("0x"))
}

func FuzzHexQuantityRoundTrip(f *testing.F) {
	for _, n := range []uint64{0, 1, 255, math.MaxUint64} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n uint64) {
		got, err := HexQtyToUint64(Uint64ToHexQty(n))
		if err != nil || got != n {
			t.Fatalf("round trip %d: got %d, err %v", n, got, err)
		}
	})
}
