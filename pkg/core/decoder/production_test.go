package decoder

import (
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegerWidthsEnforced(t *testing.T) {
	for bits := 8; bits <= 256; bits += 8 {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
			_, err := decodeByType(fmt.Sprintf("%064x", max), fmt.Sprintf("uint%d", bits))
			require.NoError(t, err)
			if bits < 256 {
				_, err = decodeByType(fmt.Sprintf("%064x", new(big.Int).Add(max, big.NewInt(1))), fmt.Sprintf("uint%d", bits))
				assert.Error(t, err)
			}
			v, err := decodeByType(strings.Repeat("f", 64), fmt.Sprintf("int%d", bits))
			require.NoError(t, err)
			assert.Equal(t, big.NewInt(-1), v)
		})
	}
	for _, typ := range []string{"int0", "int7", "uint264", "uint-8", "tuple", "uint256[]"} {
		_, err := decodeByType(strings.Repeat("0", 64), typ)
		assert.Error(t, err)
	}
}

func TestIndexedDynamicValuesRemainHashes(t *testing.T) {
	for _, typ := range []string{"string", "bytes"} {
		d := NewStandardDecoder()
		require.NoError(t, d.RegisterABI("dynamic", fmt.Sprintf(`[{"type":"event","name":"Value","inputs":[{"name":"value","type":"%s","indexed":true}]}]`, typ)))
		e, err := d.Decode("dynamic", "1", types.Log{Topics: []string{utils.FunctionSignatureToTopic("Value(" + typ + ")"), "0x" + strings.Repeat("ab", 32)}, BlockNumber: "0x1", LogIndex: "0x0"})
		require.NoError(t, err)
		require.NotNil(t, e)
		assert.Len(t, e.Fields["value"], 32)
	}
}

func TestDecodeBatchReportsMissingContext(t *testing.T) {
	_, err := NewStandardDecoder().DecodeBatch(nil)
	assert.Error(t, err)
	_, err = NewDecoderRouter().DecodeBatch(nil)
	assert.Error(t, err)
}

func TestABIRegistrationConcurrentWithDecoding(t *testing.T) {
	d := NewStandardDecoder()
	require.NoError(t, d.RegisterABI("token", erc20Transfer_ABI))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = d.RegisterABI("token", erc20Transfer_ABI)
		}
	}()
	for i := 0; i < 100; i++ {
		_, err := d.Decode("token", "1", types.Log{Topics: []string{utils.FunctionSignatureToTopic("Transfer(address,address,uint256)"), "0x" + abiWord(1), "0x" + abiWord(2)}, Data: "0x" + abiWord(3), BlockNumber: "0x1", LogIndex: "0x0"})
		require.NoError(t, err)
	}
	wg.Wait()
}

func TestUnsupportedABIRegistrationIsAtomic(t *testing.T) {
	for _, unsupported := range []string{`{"type":"event","name":"Hidden","anonymous":true,"inputs":[]}`, `{"type":"event","name":"Array","inputs":[{"name":"xs","type":"uint256[]"}]}`, `{"type":"event","name":"Tuple","inputs":[{"name":"x","type":"tuple"}]}`} {
		d := NewStandardDecoder()
		assert.Error(t, d.RegisterABI("bad", `[{"type":"event","name":"Valid","inputs":[]},`+unsupported+`]`))
		_, err := d.Decode("bad", "1", types.Log{Topics: []string{utils.FunctionSignatureToTopic("Valid()")}})
		assert.ErrorContains(t, err, "not found")
	}
}
