package types

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var ErrFieldNotFound = errors.New("event field not found")

func (f EventFields) value(name string) (any, error) {
	v, exists := f[name]
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrFieldNotFound, name)
	}
	return v, nil
}

func fieldTypeError(name, expected string, actual any) error {
	return fmt.Errorf("field %q: expected %s, got %T", name, expected, actual)
}

// String reads a string field, including decoded addresses. It does not coerce
// other values or validate whether the string is an EVM address.
func (f EventFields) String(name string) (string, error) {
	v, err := f.value(name)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fieldTypeError(name, "string", v)
	}
	return s, nil
}

func (f EventFields) Bool(name string) (bool, error) {
	v, err := f.value(name)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fieldTypeError(name, "bool", v)
	}
	return b, nil
}

// BigInt reads exact native integers, big.Int, json.Number, or decimal strings.
// Floating-point values are rejected because their original precision is unknown.
// The returned integer is a copy and can be modified without changing the event.
func (f EventFields) BigInt(name string) (*big.Int, error) {
	v, err := f.value(name)
	if err != nil {
		return nil, err
	}
	n := new(big.Int)
	switch value := v.(type) {
	case *big.Int:
		if value != nil {
			return n.Set(value), nil
		}
	case big.Int:
		return n.Set(&value), nil
	case int:
		return n.SetInt64(int64(value)), nil
	case int8:
		return n.SetInt64(int64(value)), nil
	case int16:
		return n.SetInt64(int64(value)), nil
	case int32:
		return n.SetInt64(int64(value)), nil
	case int64:
		return n.SetInt64(value), nil
	case uint:
		return n.SetUint64(uint64(value)), nil
	case uint8:
		return n.SetUint64(uint64(value)), nil
	case uint16:
		return n.SetUint64(uint64(value)), nil
	case uint32:
		return n.SetUint64(uint64(value)), nil
	case uint64:
		return n.SetUint64(value), nil
	case json.Number:
		if _, ok := n.SetString(string(value), 10); ok {
			return n, nil
		}
	case string:
		if _, ok := n.SetString(value, 10); ok {
			return n, nil
		}
	}
	return nil, fieldTypeError(name, "exact integer", v)
}

func (f EventFields) Uint64(name string) (uint64, error) {
	n, err := f.BigInt(name)
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() {
		return 0, fmt.Errorf("field %q: integer is outside uint64 range", name)
	}
	return n.Uint64(), nil
}

func (f EventFields) Int64(name string) (int64, error) {
	n, err := f.BigInt(name)
	if err != nil {
		return 0, err
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("field %q: integer is outside int64 range", name)
	}
	return n.Int64(), nil
}

// Bytes reads bytes or a 0x-prefixed hex string and returns an independent copy.
func (f EventFields) Bytes(name string) ([]byte, error) {
	v, err := f.value(name)
	if err != nil {
		return nil, err
	}
	switch b := v.(type) {
	case []byte:
		return append([]byte(nil), b...), nil
	case [32]byte:
		return append([]byte(nil), b[:]...), nil
	case string:
		if strings.HasPrefix(b, "0x") {
			decoded, err := hex.DecodeString(b[2:])
			if err == nil {
				return decoded, nil
			}
		}
	}
	return nil, fieldTypeError(name, "bytes or 0x-prefixed hexadecimal string", v)
}
