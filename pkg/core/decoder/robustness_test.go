package decoder

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func abiWord(n uint64) string { return fmt.Sprintf("%064x", n) }

func dynamicPayload(payload string) string {
	return abiWord(32) + abiWord(uint64(len(payload)/2)) + payload
}

func TestDynamicDecode(t *testing.T) {
	for _, tc := range []struct{ name, data, want string }{
		{"empty", dynamicPayload(""), ""},
		{"ASCII", dynamicPayload("68656c6c6f"), "hello"},
		{"UTF8", dynamicPayload("e697a5e69cac"), "日本"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeString(tc.data, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			b, err := decodeDynamicBytes(tc.data, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(b))
		})
	}
	// The dynamic pointer is relative to the full payload, even when its head
	// follows a static argument.
	data := abiWord(7) + abiWord(64) + abiWord(2) + "6869"
	got, err := decodeString(data, 32)
	require.NoError(t, err)
	assert.Equal(t, "hi", got)
}

func TestDynamicDecodeRejectsMalformedData(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		offset     int
	}{
		{"empty", "", 0},
		{"short head", "00", 0},
		{"negative offset", dynamicPayload(""), -1},
		{"offset past data", dynamicPayload(""), 128},
		{"huge offset", dynamicPayload(""), int(^uint(0) >> 1)},
		{"invalid pointer", strings.Repeat("z", 64), 0},
		{"pointer outside data", abiWord(4096), 0},
		{"pointer overflows multiplication", abiWord(^uint64(0)), 0},
		{"missing length", abiWord(32), 0},
		{"invalid length", abiWord(32) + strings.Repeat("g", 64), 0},
		{"length outside data", abiWord(32) + abiWord(100) + "00", 0},
		{"length overflows multiplication", abiWord(32) + abiWord(^uint64(0)), 0},
		{"invalid payload hex", abiWord(32) + abiWord(1) + "zz", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() { _, err := decodeString(tc.data, tc.offset); assert.Error(t, err) })
			assert.NotPanics(t, func() { _, err := decodeDynamicBytes(tc.data, tc.offset); assert.Error(t, err) })
		})
	}
}

func TestDecodeMalformedLog(t *testing.T) {
	d := NewStandardDecoder()
	require.NoError(t, d.RegisterABI("transfer", erc20Transfer_ABI))
	require.NoError(t, d.RegisterABI("string", stringEvent_ABI))
	for _, topic := range []string{"", "0", "0x", "0xzz"} {
		t.Run("indexed topic "+topic, func(t *testing.T) {
			l := types.Log{Topics: []string{utils.FunctionSignatureToTopic("Transfer(address,address,uint256)"), topic, "0x" + abiWord(1)}, Data: "0x" + abiWord(1), BlockNumber: "0x1", LogIndex: "0x0"}
			assert.NotPanics(t, func() { ev, err := d.Decode("transfer", "1", l); assert.NoError(t, err); assert.Nil(t, ev) })
		})
	}
	for _, data := range []string{"", "0", "0x", "0x" + abiWord(^uint64(0))} {
		l := types.Log{Topics: []string{utils.FunctionSignatureToTopic("StringEvent(string)")}, Data: data}
		assert.NotPanics(t, func() { ev, err := d.Decode("string", "1", l); assert.NoError(t, err); assert.Nil(t, ev) })
	}
}

func TestDecodeStaticTypes(t *testing.T) {
	for _, tc := range []struct {
		kind, data string
		want       any
	}{
		{"uint64", abiWord(42), uint64(42)},
		{"uint256", abiWord(42), big.NewInt(42)},
		{"address", strings.Repeat("0", 24) + strings.Repeat("ab", 20), "0x" + strings.Repeat("ab", 20)},
		{"bool", abiWord(0), false},
		{"bool", abiWord(1), true},
		{"bytes32", strings.Repeat("ab", 32), strings.Repeat("\xab", 32)},
	} {
		t.Run(tc.kind+tc.data[60:], func(t *testing.T) {
			got, err := decodeByType(tc.data, tc.kind)
			require.NoError(t, err)
			if b, ok := got.([]byte); ok {
				got = string(b)
			}
			assert.Equal(t, tc.want, got)
		})
	}
	for _, tc := range []struct{ kind, data string }{
		{"uint64", strings.Repeat("f", 64)},
		{"uint256", strings.Repeat("g", 64)},
		{"uint256", "00"}, {"address", "00"}, {"address", strings.Repeat("z", 64)},
		{"int24", "00"},
		{"bool", "00"}, {"bool", abiWord(2)}, {"bool", strings.Repeat("f", 62) + "01"},
		{"bytes32", "00"}, {"bytes32", strings.Repeat("g", 64)},
		{"tuple", abiWord(0)},
	} {
		_, err := decodeByType(tc.data, tc.kind)
		assert.Error(t, err, tc.kind)
	}
	_, err := decodeByTypeWithOffset("", 0, "tuple")
	assert.Error(t, err)
}

func TestDecodeSignedIntegers(t *testing.T) {
	for _, tc := range []struct {
		kind string
		bits uint
	}{{"int", 256}, {"int256", 256}, {"int128", 128}, {"int24", 24}} {
		t.Run(tc.kind, func(t *testing.T) {
			limit := new(big.Int).Lsh(big.NewInt(1), tc.bits-1)
			minimum := new(big.Int).Neg(new(big.Int).Set(limit))
			maximum := new(big.Int).Sub(new(big.Int).Set(limit), big.NewInt(1))
			for _, want := range []*big.Int{big.NewInt(-42), big.NewInt(-1), big.NewInt(0), big.NewInt(42), minimum, maximum} {
				wire := new(big.Int).Set(want)
				if wire.Sign() < 0 {
					wire.Add(wire, new(big.Int).Lsh(big.NewInt(1), 256))
				}
				got, err := decodeByType(fmt.Sprintf("%064x", wire), tc.kind)
				require.NoError(t, err)
				value, ok := got.(*big.Int)
				require.True(t, ok)
				assert.Zero(t, want.Cmp(value), "decoded value must equal %s", want)
			}
			if tc.bits < 256 {
				_, err := decodeByType(fmt.Sprintf("%064x", limit), tc.kind)
				assert.Error(t, err, "a value outside the signed type's range must be rejected")
			}
		})
	}
}

func TestRegisterABIFromFile(t *testing.T) {
	d := NewStandardDecoder()
	path := filepath.Join(t.TempDir(), "abi.json")
	require.NoError(t, os.WriteFile(path, []byte(erc20Transfer_ABI), 0600))
	require.NoError(t, d.RegisterABIFromFile("token", path))
	assert.Len(t, d.events["token"], 1)
	assert.Error(t, d.RegisterABIFromFile("missing", path+".missing"))
	require.NoError(t, os.WriteFile(path, []byte("{"), 0600))
	assert.ErrorContains(t, d.RegisterABIFromFile("bad", path), "invalid ABI JSON")
}

func TestDecodeDynamicEventMetadata(t *testing.T) {
	d := NewStandardDecoder()
	require.NoError(t, d.RegisterABI("bytes", `[{"type":"event","name":"Payload","inputs":[{"name":"value","type":"bytes"}]}]`))
	l := types.Log{Topics: []string{utils.FunctionSignatureToTopic("Payload(bytes)")}, Data: "0x" + dynamicPayload("aabb"), BlockNumber: "0x2a", BlockHash: "block", TransactionHash: "tx", LogIndex: "0x3"}
	ev, err := d.Decode("bytes", "10", l)
	require.NoError(t, err)
	require.NotNil(t, ev)
	assert.Equal(t, []byte{0xaa, 0xbb}, ev.Fields["value"])
	assert.Equal(t, "block:tx:3", ev.Id)
	assert.Equal(t, "10", ev.ChainId)
	assert.Equal(t, uint64(42), ev.BlockNumber)
	l.BlockNumber = "invalid"
	_, err = d.Decode("bytes", "10", l)
	assert.Error(t, err)
	l.BlockNumber = "0x2a"
	l.LogIndex = "invalid"
	_, err = d.Decode("bytes", "10", l)
	assert.Error(t, err)
}

func FuzzDecodeDynamicData(f *testing.F) {
	f.Add(dynamicPayload("6869"), 0)
	f.Add("", 0)
	f.Add(abiWord(^uint64(0)), 0)
	f.Add(dynamicPayload(""), -1)
	f.Fuzz(func(t *testing.T, data string, offset int) {
		_, _ = decodeString(data, offset)
		_, _ = decodeDynamicBytes(data, offset)
	})
}

func FuzzDecodeLog(f *testing.F) {
	d := NewStandardDecoder()
	if err := d.RegisterABI("token", erc20Transfer_ABI); err != nil {
		f.Fatal(err)
	}
	if err := d.RegisterABI("message", stringEvent_ABI); err != nil {
		f.Fatal(err)
	}
	f.Add("0x"+abiWord(1), "0x"+abiWord(1), "0x1", "0x0")
	f.Add("", "", "", "")
	f.Fuzz(func(t *testing.T, topic, data, block, index string) {
		l := types.Log{Topics: []string{utils.FunctionSignatureToTopic("Transfer(address,address,uint256)"), topic, topic}, Data: data, BlockNumber: block, LogIndex: index}
		_, _ = d.Decode("token", "1", l)
		l.Topics = []string{utils.FunctionSignatureToTopic("StringEvent(string)")}
		_, _ = d.Decode("message", "1", l)
	})
}
