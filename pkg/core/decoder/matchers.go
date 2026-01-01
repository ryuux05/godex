package decoder

import (
	"strings"

	"github.com/ryuux05/godex/pkg/core/types"
)

// ByTopicCount matches logs with exactly N topics
func ByTopicCount(count int) MatchFunc {
	return func(log types.Log) bool {
		return len(log.Topics) == count
	}
}

// ByAddress matches logs from specific contract address
func ByAddress(address string) MatchFunc {
	addr := strings.ToLower(address)
	return func(log types.Log) bool {
		return strings.ToLower(log.Address) == addr
	}
}

// ByAddresses matches logs from any of the addresses
func ByAddresses(addresses []string) MatchFunc {
	set := make(map[string]struct{}, len(addresses))
	for _, a := range addresses {
		set[strings.ToLower(a)] = struct{}{}
	}

	return func(log types.Log) bool {
		_, ok := set[strings.ToLower(log.Address)]
		return ok
	}
}

// ByTopic0 matches logs by event signature (first topic)
func ByTopic0(topic0 string) MatchFunc {
	 t := strings.ToLower(topic0)
	 return func(log types.Log) bool {
	 	if len(log.Topics) == 0 {
			return false
		}

		return strings.ToLower(log.Topics[0]) == t
	 }
}

// And combines multiple matchers ( all must be true )
func And(matchers ...MatchFunc) MatchFunc {
	return func(log types.Log) bool {
		for _, m := range matchers {
			if !m(log) {
				return false
			}
		}
		return true
	}
}

func Or(matchers ...MatchFunc) MatchFunc {
	return func(log types.Log) bool {
		for _, m := range matchers {
			if !m(log) {
				return  true
			}
		}
		return false
	}
}
