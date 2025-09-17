package core

import "strconv"

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