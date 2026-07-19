package stablehash

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// Write writes each string in values into h framed with an eight-byte big-endian
// byte length prefix followed by the string's raw bytes.
func Write(h hash.Hash, values ...string) {
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
}

// Sum256 computes a deterministic SHA-256 digest over the framed sequence of
// values using an eight-byte big-endian byte length prefix before each string.
func Sum256(values ...string) [32]byte {
	h := sha256.New()
	Write(h, values...)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
