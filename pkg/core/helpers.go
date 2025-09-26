package core

import (
	"encoding/hex"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Helpers (hex quantity <-> uint64)
func Uint64ToHexQty(n uint64) string {
	if n == 0 {
		return "0x0"
	}
	return "0x" + strconv.FormatUint(n, 16)
}

func HexQtyToUint64(s string) (uint64, error) {
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		return strconv.ParseUint(s[2:], 16, 64)
	}
	return strconv.ParseUint(s, 10, 64)
}

// Keccak256 computes the Keccak256 hash of input data
func Keccak256(data []byte) []byte {
	hash := sha3.NewLegacyKeccak256()
	hash.Write(data)
	return hash.Sum(nil)
}

// FunctionSignatureTopic converts a funciton signature to its Keccak256
// Example: "Transfer(address,address,uint256)" -> "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
func FunctionSignatureToTopic(signature string) string {
	// Remove all whitespaces
	cleanSig := strings.ReplaceAll(signature, " ", "")

	// Hash the clean signature
	hash := Keccak256([]byte(cleanSig))

	return "0x" + hex.EncodeToString(hash)
}

func ConvertToTopics(signatures []string) []string {
	topics := make([]string, len(signatures))
	for i, signature := range signatures {
		// Check if the signature has been hashed to keccak256 and has the hex prefix
		if len(signature) == 66 && strings.HasPrefix(signature, "0x") {
			topics[i] = signature
			// Check if the signature has been hashed but didnt have the hex prefix
		} else if len(signature) == 64 && !strings.HasPrefix(signature, "0x") {
			topics[i] = "0x" + signature
		} else {
			topics[i] = FunctionSignatureToTopic(signature)
		}
		
	}
	
	return topics
}
