package decoder

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/ryuux05/godex/pkg/core/types"
	"github.com/ryuux05/godex/pkg/core/utils"
)

type StandardDecoder struct {
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
	abi, exists := d.events[name]
	if !exists {
		return nil, fmt.Errorf("ABI '%s' not found", name)
	}


	e, exist := abi[strings.ToLower(log.Topics[0])]
	if !exist {
		return nil, nil
	}

	var topicNum = 1
	var dataOffset = 0
	field := make(map[string]interface{})

	for _, input := range e.Inputs {
		if input.Indexed == true {
			if topicNum >= len(log.Topics) {
                return nil, nil
            }

			value, err := decodeByType(log.Topics[topicNum][2:], input.Type)
			if err != nil {
				return nil, nil
			}
			field[input.Name] = value
			topicNum ++
		} else {
			if input.Type != "string" && input.Type != "bytes" {

				start := dataOffset
				end := dataOffset + 32
				
				// Here we times 2 because each byte is represented by 2 character
				// Pass clean data without the 0x format
				hexStart := 2 + (start * 2)
				hexEnd := 2 + (end * 2)
				
				if hexEnd > len(log.Data) {
					return nil, nil
				}
				
				value, err := decodeByType(log.Data[hexStart:hexEnd], input.Type)
				if err != nil {
					return nil, nil
				}
				
				field[input.Name] = value
				dataOffset += 32
			} else {
				// Pass clean data without the 0x format
				// Offset is in byte
				value, err := decodeByTypeWithOffset(log.Data[2:], dataOffset, input.Type)
				if err != nil {
					return nil, nil
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
		Id: id,
		ChainId: chainId,
		BlockNumber: blockNumber,
		BlockHash: log.BlockHash,
		Address: log.Address,
		TransactionHash: log.TransactionHash,
		LogIndex: logIndex,
		EventType: e.Name,
		Fields: field,
	}, nil
}

func (d *StandardDecoder) DecodeBatch(logs []types.Log) (*[]types.Event, error) {
	return nil, nil
}

func (d *StandardDecoder) RegisterABI(name, abiJson string) error {
	var abi ABI 
	err := json.Unmarshal([]byte(abiJson), &abi)
	if err != nil {
		return fmt.Errorf("invalid ABI JSON: %w", err)
	}

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
			Name: item.Name,
			Signature: signature,
			TopicHash: topicHash,
			Inputs: convertInputs(item.Inputs),
		}

		d.events[name][topicHash] = eventDefinition
	}

	return nil
}

func (d *StandardDecoder) RegisterABIFromFile(name string, filepath string) error{
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

func decodeByType(hex string, types string) (any, error){
	switch types {
	case "address":
		return decodeAddress(hex)
	case "uint256", "uint", "int256", "int":
		return decodeBigInt(hex)
	case "uint8", "uint16", "uint32", "uint64":
		return decodeUint(hex)
	case "bool":
		return decodeBool(hex)
	case "bytes32":
		return decodeBytes32(hex)
	default:
		// Handle arrays, tuples, or return error
		return nil, fmt.Errorf("unidentified data type")
	}
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

	// address data is the last 20 bytes
	addressHex := hexData[24:]

	return "0x" + addressHex, nil
}

func decodeBigInt(hexData string) (*big.Int, error) {
	if len(hexData) != 64 {
		return nil, fmt.Errorf("invalid big int hex length: expected 64, got %d", len(hexData))
	}

	value := new(big.Int)

	_, ok := value.SetString(hexData, 16) 
	if !ok {
        return nil, fmt.Errorf("failed to parse hex as big integer: %s", hexData)
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
	// Get the data offset pointer
	hexStart := (offset * 2)
    hexEnd := hexStart + 64
	offsetHex := data[hexStart:hexEnd]

	p, err := decodeUint(offsetHex)
	if err != nil {
		return "", err
	}

	// Get the data lenght
	dataStart := (p * 2)  // Convert byte offset to hex position
    lengthHex := data[dataStart:dataStart+64]
    
    l, err := decodeUint(lengthHex)
	if err != nil {
		return "", err
	}

	// Get the actual data
	bytesStart := dataStart + 64  // After length
    bytesEnd := bytesStart + (l * 2)  // l bytes = l*2 hex chars
    dataHex := data[bytesStart:bytesEnd]
    
    bytes, err := hex.DecodeString(dataHex)
	if err != nil {
        return "", fmt.Errorf("failed to decode hex: %w", err)
    }
	return string(bytes), nil
}

func decodeDynamicBytes(data string, offset int) ([]byte, error) {
	// Get the data offset pointer
	hexStart := (offset * 2)
    hexEnd := hexStart + 64
	offsetHex := data[hexStart:hexEnd]

	p, err := decodeUint(offsetHex)
	if err != nil {
		return nil, err
	}

	// Get the data lenght
	dataStart := (p * 2)  // Convert byte offset to hex position
    lengthHex := data[dataStart:dataStart+64]
    
    l, err := decodeUint(lengthHex)
	if err != nil {
		return nil, err
	}

	// Get the actual data
	bytesStart := dataStart + 64  // After length
    bytesEnd := bytesStart + (l * 2)  // l bytes = l*2 hex chars
    dataHex := data[bytesStart:bytesEnd]
    
    bytes, err := hex.DecodeString(dataHex)
	if err != nil {
        return nil, fmt.Errorf("failed to decode hex: %w", err)
    }
	return bytes, nil
}

