package distribconsistent

import (
	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
)

var _ consistent.Member = (*stringer)(nil)

type stringer string

func (s stringer) String() string {
	return string(s)
}

var _ consistent.Hasher = (*hasher)(nil)

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}
