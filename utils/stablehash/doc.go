// Package stablehash computes deterministic digests for ordered sequences of strings.
//
// The digest is computed using SHA-256 over an eight-byte big-endian length-prefixed
// framing of each string. This framing prevents boundary-forging attacks: two
// different sequences of strings will never produce identical byte streams, even if
// substrings are moved between adjacent fields (e.g., ["ab", "c"] vs ["a", "bc"]).
//
// Framing algorithm invariants:
// - The length prefix is exactly eight bytes, encoded in big-endian byte order.
// - The prefix measures the length of the string in bytes, not UTF-8 runes or characters.
// - Input sequence order is preserved: Sum256("a", "b") differs from Sum256("b", "a").
// - No values (Sum256()) differs from one empty string (Sum256("")): no values writes
//   zero bytes to the hasher, whereas one empty string writes an eight-byte zero length prefix.
package stablehash
