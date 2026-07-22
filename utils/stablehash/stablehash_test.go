package stablehash_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/stablehash"
	"github.com/stretchr/testify/require"
)

func TestSum256VectorsAndEquivalence(t *testing.T) {
	t.Parallel()

	// Prove boundary-forging pairs produce different digests.
	sum1 := stablehash.Sum256("a", "bc")
	sum2 := stablehash.Sum256("ab", "c")
	require.NotEqual(t, sum1, sum2)

	// Cover empty input vs one empty string.
	sumEmptyInput := stablehash.Sum256()
	sumEmptyString := stablehash.Sum256("")
	require.NotEqual(t, sumEmptyInput, sumEmptyString)
	require.Equal(t, sha256.Sum256(nil), sumEmptyInput)

	// Ordering matters.
	require.NotEqual(t, stablehash.Sum256("foo", "bar"), stablehash.Sum256("bar", "foo"))

	// Non-ASCII strings.
	sumUnicode := stablehash.Sum256("příliš", "žluťoučký")
	require.NotEqual(t, sumEmptyInput, sumUnicode)

	// Repeated values differ from single values.
	require.NotEqual(t, stablehash.Sum256("dup"), stablehash.Sum256("dup", "dup"))
}

// TestFormerFramingsEquivalence verifies that stablehash.Write and stablehash.Sum256
// produce the exact same byte sequence as the two former private implementations:
// writeLengthPrefixed (route.go) and writeField (registration identifiers).
func TestFormerFramingsEquivalence(t *testing.T) {
	t.Parallel()

	writeLengthPrefixed := func(h hash.Hash, value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}

	writeField := func(digest hash.Hash, value string) {
		var length [8]byte
		for i, shift := 0, 56; i < 8; i, shift = i+1, shift-8 {
			length[i] = byte(uint64(len(value)) >> shift)
		}
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}

	testCases := [][]string{
		{},
		{""},
		{"hello"},
		{"hello", "world"},
		{"platform", "registration", "1", "100", "test-machine", "10.0.0.1"},
		{"příliš", "žluťoučký", "kůň"},
	}

	for _, tc := range testCases {
		h1 := sha256.New()
		for _, v := range tc {
			writeLengthPrefixed(h1, v)
		}

		h2 := sha256.New()
		for _, v := range tc {
			writeField(h2, v)
		}

		sumExpected := stablehash.Sum256(tc...)
		require.Equal(t, hex.EncodeToString(h1.Sum(nil)), hex.EncodeToString(sumExpected[:]))
		require.Equal(t, hex.EncodeToString(h2.Sum(nil)), hex.EncodeToString(sumExpected[:]))
	}
}
