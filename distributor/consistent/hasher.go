package distribconsistent

import "github.com/cespare/xxhash"

type stringer string

func (s stringer) String() string {
	return string(s)
}

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}
