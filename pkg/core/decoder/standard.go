package decoder

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
)

type StandardDecoder struct {
	mu     sync.RWMutex
	events map[string]map[string]*types.EventDefinition
}

func NewStandardDecoder() *StandardDecoder {
	return &StandardDecoder{
		events: make(map[string]map[string]*types.EventDefinition),
	}
}

func (d *StandardDecoder) Decode(name string, chainId string, log types.Log) (*types.Event, error) {
	// If topic is empty skip it
	if len(log.Topics) == 0 {
		return nil, nil
	}

	// Get the ABI map by name
	d.mu.RLock()
	abi, exists := d.events[name]
	if !exists {
		d.mu.RUnlock()
		return nil, fmt.Errorf("ABI '%s' not found", name)
	}

	e, exist := abi[strings.ToLower(log.Topics[0])]
	d.mu.RUnlock()
	if !exist {
		return nil, nil
	}

	var topicNum = 1
	var dataOffset = 0
	field := make(map[string]interface{})

	for _, input := range e.Inputs {
		if input.Indexed == true {
			if topicNum >= len(log.Topics) {
				return nil, fmt.Errorf("malformed %s field %s (%s)", e.Name, input.Name, input.Type)
			}

			if !strings.HasPrefix(log.Topics[topicNum], "0x") {
				return nil, fmt.Errorf("malformed %s field %s (%s)", e.Name, input.Name, input.Type)
			}
			indexedType := input.Type
			if indexedType == "string" || indexedType == "bytes" {
				indexedType = "bytes32"
			}
			value, err := decodeByType(log.Topics[topicNum][2:], indexedType)
			if err != nil {
				return nil, fmt.Errorf("malformed %s field %s (%s): %w", e.Name, input.Name, input.Type, err)
			}
			field[input.Name] = value
			topicNum++
		} else {
			if !strings.HasPrefix(log.Data, "0x") {
				return nil, fmt.Errorf("malformed %s field %s (%s)", e.Name, input.Name, input.Type)
			}
			if input.Type != "string" && input.Type != "bytes" {

				start := dataOffset
				end := dataOffset + 32

				// Here we times 2 because each byte is represented by 2 character
				// Pass clean data without the 0x format
				hexStart := 2 + (start * 2)
				hexEnd := 2 + (end * 2)

				if hexEnd > len(log.Data) {
					return nil, fmt.Errorf("malformed %s field %s (%s)", e.Name, input.Name, input.Type)
				}

				value, err := decodeByType(log.Data[hexStart:hexEnd], input.Type)
				if err != nil {
					return nil, fmt.Errorf("malformed %s field %s (%s): %w", e.Name, input.Name, input.Type, err)
				}

				field[input.Name] = value
				dataOffset += 32
			} else {
				// Pass clean data without the 0x format
				// Offset is in byte
				value, err := decodeByTypeWithOffset(log.Data[2:], dataOffset, input.Type)
				if err != nil {
					return nil, fmt.Errorf("malformed %s field %s (%s): %w", e.Name, input.Name, input.Type, err)
				}

				field[input.Name] = value
				dataOffset += 32
			}
		}
	}

	blockNumber, err := utils.HexQtyToUint64(log.BlockNumber)
	if err != nil {
		return nil, err
	}
	logIndex, err := utils.HexQtyToUint64(log.LogIndex)
	if err != nil {
		return nil, err
	}

	id := fmt.Sprintf("%s:%s:%d", log.BlockHash, log.TransactionHash, logIndex)
	return &types.Event{
		Id:              id,
		ChainId:         chainId,
		BlockNumber:     blockNumber,
		BlockHash:       log.BlockHash,
		Address:         log.Address,
		TransactionHash: log.TransactionHash,
		LogIndex:        logIndex,
		EventType:       e.Name,
		Fields:          field,
	}, nil
}

func (d *StandardDecoder) DecodeBatch(logs []types.Log) (*[]types.Event, error) {
	return nil, fmt.Errorf("DecodeBatch requires ABI and chain context; use Decode")
}

func (d *StandardDecoder) RegisterABI(name, abiJson string) error {
	var abi ABI
	err := json.Unmarshal([]byte(abiJson), &abi)
	if err != nil {
		return fmt.Errorf("invalid ABI JSON: %w", err)
	}

	for _, item := range abi {
		if item.Type != "event" {
			continue
		}
		if item.Anonymous {
			return fmt.Errorf("anonymous event %s is unsupported", item.Name)
		}
		for _, input := range item.Inputs {
			if input.Type == "string" || input.Type == "bytes" {
				continue
			}
			if _, err := decodeByType(strings.Repeat("0", 64), input.Type); err != nil {
				return fmt.Errorf("event %s field %s: %w", item.Name, input.Name, err)
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.events[name] == nil {
		d.events[name] = make(map[string]*types.EventDefinition)
	}

	for _, item := range abi {
		if item.Type != "event" {
			continue
		}

		// Build the event signature from the abi
		signature := buildSignature(item)

		topicHash := utils.FunctionSignatureToTopic(signature)

		eventDefinition := &types.EventDefinition{
			Name:      item.Name,
			Signature: signature,
			TopicHash: topicHash,
			Inputs:    convertInputs(item.Inputs),
		}

		d.events[name][topicHash] = eventDefinition
	}

	return nil
}

func (d *StandardDecoder) RegisterABIFromFile(name string, filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	return d.RegisterABI(name, string(data))
}

func buildSignature(item ABIItem) string {
	var types []string
	for _, input := range item.Inputs {
		types = append(types, input.Type)
	}
	return fmt.Sprintf("%s(%s)", item.Name, strings.Join(types, ","))
}

func convertInputs(inputs []ABIInput) []types.EventInput {
	var result []types.EventInput
	for _, input := range inputs {
		result = append(result, types.EventInput{
			Name:    input.Name,
			Type:    input.Type,
			Indexed: input.Indexed,
		})
	}
	return result
}

func decodeByType(word string, typ string) (any, error) {
	switch typ {
	case "address":
		return decodeAddress(word)
	case "bool":
		return decodeBool(word)
	case "bytes32":
		return decodeBytes32(word)
	}
	signed := strings.HasPrefix(typ, "int")
	unsigned := strings.HasPrefix(typ, "uint")
	if signed || unsigned {
		prefix := "uint"
		if signed {
			prefix = "int"
		}
		width := strings.TrimPrefix(typ, prefix)
		bits := 256
		if width != "" {
			var err error
			bits, err = strconv.Atoi(width)
			if err != nil {
				return nil, fmt.Errorf("unsupported type %s", typ)
			}
		}
		if bits < 8 || bits > 256 || bits%8 != 0 {
			return nil, fmt.Errorf("invalid integer width %s", typ)
		}
		if signed {
			return decodeSignedBigInt(word, uint(bits))
		}
		value, err := decodeBigInt(word)
		if err != nil {
			return nil, err
		}
		if value.Sign() < 0 || value.BitLen() > bits {
			return nil, fmt.Errorf("value outside %s range", typ)
		}
		switch typ {
		case "uint8", "uint16", "uint32", "uint64":
			return value.Uint64(), nil
		}
		return value, nil
	}
	return nil, fmt.Errorf("unsupported ABI type %s", typ)
}

func decodeByTypeWithOffset(data string, offset int, types string) (any, error) {
	switch types {
	case "bytes":
		return decodeDynamicBytes(data, offset)
	case "string":
		return decodeString(data, offset)
	default:
		// Handle arrays, tuples, or return error
		return nil, fmt.Errorf("unidentified data type")
	}
}

func decodeAddress(hexData string) (string, error) {
	if len(hexData) != 64 {
		return "", fmt.Errorf("invalid address hex length: expected 64, got %d", len(hexData))
	}
	if _, err := hex.DecodeString(hexData); err != nil {
		return "", fmt.Errorf("invalid address hex: %w", err)
	}

	if strings.TrimLeft(hexData[:24], "0") != "" {
		return "", fmt.Errorf("invalid address padding")
	}
	// address data is the last 20 bytes
	addressHex := hexData[24:]

	return "0x" + addressHex, nil
}

func decodeBigInt(hexData string) (*big.Int, error) {
	if len(hexData) != 64 {
		return nil, fmt.Errorf("invalid big int hex length: expected 64, got %d", len(hexData))
	}

	if _, err := hex.DecodeString(hexData); err != nil {
		return nil, fmt.Errorf("invalid integer hex: %w", err)
	}
	value := new(big.Int)

	_, ok := value.SetString(hexData, 16)
	if !ok {
		return nil, fmt.Errorf("failed to parse hex as big integer: %s", hexData)
	}

	return value, nil
}

func decodeSignedBigInt(hexData string, bits uint) (*big.Int, error) {
	value, err := decodeBigInt(hexData)
	if err != nil {
		return nil, err
	}
	// Signed ABI integers occupy a sign-extended 256-bit word.
	if value.Bit(255) != 0 {
		value.Sub(value, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	limit := new(big.Int).Lsh(big.NewInt(1), bits-1)
	minimum := new(big.Int).Neg(new(big.Int).Set(limit))
	if value.Cmp(minimum) < 0 || value.Cmp(limit) >= 0 {
		return nil, fmt.Errorf("value outside int%d range", bits)
	}
	return value, nil
}

func decodeUint(hex string) (uint64, error) {
	v, err := decodeBigInt(hex)
	if err != nil {
		return 0, err
	}

	// Convert to uint64 (check overflow)
	if !v.IsUint64() {
		return 0, fmt.Errorf("value too large for uint64")
	}

	return v.Uint64(), nil
}
func decodeBool(hexData string) (bool, error) {
	if len(hexData) != 64 {
		return false, fmt.Errorf("invalid bool hex length: expected 64, got %d", len(hexData))
	}
	if strings.TrimLeft(hexData[:62], "0") != "" {
		return false, fmt.Errorf("invalid bool padding")
	}

	// Get last 2 characters (last byte)
	lastByte := hexData[len(hexData)-2:]

	switch lastByte {
	case "00":
		return false, nil
	case "01":
		return true, nil
	default:
		return false, fmt.Errorf("invalid bool value: %s (expected 00 or 01)", lastByte)
	}

}

func decodeBytes32(hexData string) ([]byte, error) {
	if len(hexData) != 64 {
		return nil, fmt.Errorf("invalid bytes32 hex length: expected 64, got %d", len(hexData))
	}

	// Decode hex string to bytes
	bytes, err := hex.DecodeString(hexData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}

	return bytes, nil
}

func decodeString(data string, offset int) (string, error) {
	b, err := decodeDynamicBytes(data, offset)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeDynamicBytes(data string, offset int) ([]byte, error) {
	// Check before multiplying offsets, so malicious uint64 values cannot wrap
	// into a valid slice index. All lengths below are in hex characters.
	if offset < 0 || len(data) < 64 || offset > (len(data)-64)/2 {
		return nil, fmt.Errorf("dynamic head offset out of bounds")
	}
	head := offset * 2
	pointer, err := decodeUint(data[head : head+64])
	if err != nil {
		return nil, err
	}
	if pointer > uint64((len(data)-64)/2) {
		return nil, fmt.Errorf("dynamic data pointer out of bounds")
	}
	start := int(pointer) * 2
	length, err := decodeUint(data[start : start+64])
	if err != nil {
		return nil, err
	}
	start += 64
	if length > uint64((len(data)-start)/2) {
		return nil, fmt.Errorf("dynamic data length out of bounds")
	}
	b, err := hex.DecodeString(data[start : start+int(length)*2])
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}
	return b, nil
}
