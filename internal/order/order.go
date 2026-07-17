// Package order implements the order wire format: an integer-plus-fraction rank
// key (fractional indexing) whose byte-wise string comparison equals rank
// order. It ships generation (KeyBetween) and validation (Valid) only; re-space
// and doctor repair live elsewhere.
//
// The algorithm is a faithful port of the Rocicorp / Figma fractional-indexing
// construction (https://github.com/rocicorp/fractional-indexing), specialised
// to this design's fixed alphabets so it emits the exact on-disk format. The
// format is a frozen wire contract: the alphabet, head rule, and sort meaning
// must not change without a versioned migration of every stored key.
package order

import (
	"errors"
	"math"
	"strings"
)

// digitsAlphabet is the 62-character base for digit values (ascending =
// rank order = ASCII byte order).
const digitsAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// intDigitsAlphabet is the head alphabet (A-Z then a-z, i.e. digitsAlphabet
// without its ten numeric digits): its first half are the negative-length heads
// and its second half the positive-length heads.
const intDigitsAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// IntegerZero is the order key for an empty board — the result of
// KeyBetween("", "") and the value pj create writes to the first project.
const IntegerZero = "a0"

// SmallestInteger is the least legal integer part: the most-negative head
// followed by all-zero digits. It cannot be decremented further.
var SmallestInteger = string(intDigitsAlphabet[0]) + strings.Repeat(string(digitsAlphabet[0]), len(intDigitsAlphabet)/2)

// Sentinel errors returned by KeyBetween. Callers match these to distinguish a
// caller mistake (invalid bound, equal keys) from true rank-space exhaustion.
var (
	// ErrInvalidKey is returned when a non-empty bound fails the grammar.
	ErrInvalidKey = errors.New("order: invalid key")
	// ErrEqualKeys is returned when both bounds are the same key; equal keys
	// have no strict between and must be resolved by re-space, not invention.
	ErrEqualKeys = errors.New("order: equal keys have no key between them")
	// ErrUnorderedBounds is returned when left sorts after right. The contract
	// is left < right; a reversed pair is a caller-ordering bug, surfaced rather
	// than silently swapped.
	ErrUnorderedBounds = errors.New("order: left bound must sort before right bound")
	// ErrExhausted is returned at true floor/ceiling exhaustion of the integer
	// space — a theoretical limit, never reached in normal use.
	ErrExhausted = errors.New("order: rank space exhausted")
)

var (
	digitIndex    [256]int
	intDigitIndex [256]int
)

func init() {
	for i := range digitIndex {
		digitIndex[i] = -1
		intDigitIndex[i] = -1
	}
	for i := 0; i < len(digitsAlphabet); i++ {
		digitIndex[digitsAlphabet[i]] = i
	}
	for i := 0; i < len(intDigitsAlphabet); i++ {
		intDigitIndex[intDigitsAlphabet[i]] = i
	}
}

// Valid reports whether key satisfies the closed order grammar: non-empty;
// every character in the base-62 alphabet; a valid head (A-Z or a-z) whose
// length marker does not exceed the key length; and a fractional part that does
// not end in the minimum digit '0'. SmallestInteger satisfies the grammar
// (it is a legal stored key); KeyBetween separately refuses it as a bound.
func Valid(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		if digitIndex[key[i]] < 0 {
			return false
		}
	}
	intLen, ok := integerLength(key[0])
	if !ok || intLen > len(key) {
		return false
	}
	frac := key[intLen:]
	if len(frac) > 0 && frac[len(frac)-1] == digitsAlphabet[0] {
		return false
	}
	return true
}

// KeyBetween returns an order key that sorts strictly between left and right.
// An empty string denotes an open bound: KeyBetween("", "") is the first key
// (IntegerZero), KeyBetween(left, "") appends after left, and
// KeyBetween("", right) prepends before right. Non-empty bounds must be valid
// keys and must satisfy left < right. Equal keys return ErrEqualKeys, a
// reversed pair returns ErrUnorderedBounds, and true rank-space exhaustion
// returns ErrExhausted.
func KeyBetween(left, right string) (string, error) {
	a, b := left, right
	if a != "" {
		if err := validateBound(a); err != nil {
			return "", err
		}
	}
	if b != "" {
		if err := validateBound(b); err != nil {
			return "", err
		}
	}
	if a != "" && b != "" {
		if a == b {
			return "", ErrEqualKeys
		}
		if a > b {
			return "", ErrUnorderedBounds
		}
	}

	switch {
	case a == "" && b == "":
		return IntegerZero, nil

	case a == "":
		ib := integerPart(b)
		fb := b[len(ib):]
		if ib == SmallestInteger {
			return ib + midpoint("", fb), nil
		}
		if ib < b {
			return ib, nil
		}
		res, ok := decrementInteger(ib)
		if !ok {
			return "", ErrExhausted
		}
		return res, nil

	case b == "":
		ia := integerPart(a)
		fa := a[len(ia):]
		inc, ok := incrementInteger(ia)
		if !ok {
			return ia + midpoint(fa, ""), nil
		}
		return inc, nil

	default:
		ia := integerPart(a)
		fa := a[len(ia):]
		ib := integerPart(b)
		fb := b[len(ib):]
		if ia == ib {
			return ia + midpoint(fa, fb), nil
		}
		inc, ok := incrementInteger(ia)
		if !ok {
			return "", ErrExhausted
		}
		if inc < b {
			return inc, nil
		}
		return ia + midpoint(fa, ""), nil
	}
}

// validateBound accepts a key usable as a KeyBetween bound. It is stricter than
// Valid: SmallestInteger passes the grammar but cannot serve as a bound (there
// is nothing strictly before it), so it is reported as exhaustion.
func validateBound(key string) error {
	if !Valid(key) {
		return ErrInvalidKey
	}
	if key == SmallestInteger {
		return ErrExhausted
	}
	return nil
}

// integerLength returns the length of the integer part (head plus its digits)
// for the given head, or ok=false if head is not a legal head character.
func integerLength(head byte) (int, bool) {
	i := intDigitIndex[head]
	if i < 0 {
		return 0, false
	}
	half := len(intDigitsAlphabet) / 2
	if i < half {
		return half - i + 1, true
	}
	return i - half + 2, true
}

// integerPart returns the leading integer part of a key already known to be
// valid.
func integerPart(key string) string {
	intLen, _ := integerLength(key[0])
	return key[:intLen]
}

// midpoint returns a fractional string strictly between a and b (with b empty
// meaning an open upper bound). Preconditions — a < b when b is non-empty, and
// neither ends in '0' — are guaranteed by KeyBetween and preserved across the
// recursion.
func midpoint(a, b string) string {
	zero := digitsAlphabet[0]
	if b != "" {
		n := 0
		for {
			var ac byte
			if n < len(a) {
				ac = a[n]
			} else {
				ac = zero
			}
			if n < len(b) && ac == b[n] {
				n++
				continue
			}
			break
		}
		if n > 0 {
			return b[:n] + midpoint(sliceFrom(a, n), b[n:])
		}
	}

	digitA := 0
	if len(a) > 0 {
		digitA = digitIndex[a[0]]
	}
	digitB := len(digitsAlphabet)
	if b != "" {
		digitB = digitIndex[b[0]]
	}
	if digitB-digitA > 1 {
		mid := int(math.Round(0.5 * float64(digitA+digitB)))
		return string(digitsAlphabet[mid])
	}
	if b != "" && len(b) > 1 {
		return b[:1]
	}
	return string(digitsAlphabet[digitA]) + midpoint(sliceFrom(a, 1), "")
}

// incrementInteger returns the next integer part after x, widening the head on
// overflow. ok is false only when x is already the maximum positive integer.
func incrementInteger(x string) (string, bool) {
	head := x[0]
	zero := digitsAlphabet[0]
	var trailing string
	for i := len(x) - 1; i >= 1; i-- {
		d := digitIndex[x[i]] + 1
		if d == len(digitsAlphabet) {
			trailing = string(zero) + trailing
			continue
		}
		return string(head) + x[1:i] + string(digitsAlphabet[d]) + trailing, true
	}
	headIdx := intDigitIndex[head]
	if headIdx == len(intDigitsAlphabet)-1 {
		return "", false
	}
	return widenHead(intDigitsAlphabet[headIdx+1], head, trailing, zero), true
}

// decrementInteger returns the previous integer part before x, widening the
// head on underflow. ok is false only when x is already SmallestInteger.
func decrementInteger(x string) (string, bool) {
	head := x[0]
	last := digitsAlphabet[len(digitsAlphabet)-1]
	var trailing string
	for i := len(x) - 1; i >= 1; i-- {
		d := digitIndex[x[i]] - 1
		if d < 0 {
			trailing = string(last) + trailing
			continue
		}
		return string(head) + x[1:i] + string(digitsAlphabet[d]) + trailing, true
	}
	headIdx := intDigitIndex[head]
	if headIdx == 0 {
		return "", false
	}
	return widenHead(intDigitsAlphabet[headIdx-1], head, trailing, last), true
}

// widenHead assembles a carried/borrowed integer part under a new head,
// growing or shrinking the digit run to match the new head's integer length.
// pad is '0' on increment overflow and the max digit on decrement underflow.
func widenHead(newHead, oldHead byte, trailing string, pad byte) string {
	newLen, _ := integerLength(newHead)
	oldLen, _ := integerLength(oldHead)
	switch delta := newLen - oldLen; {
	case delta > 0:
		return string(newHead) + trailing + string(pad)
	case delta < 0:
		return string(newHead) + trailing[1:]
	default:
		return string(newHead) + trailing
	}
}

func sliceFrom(s string, n int) string {
	if n >= len(s) {
		return ""
	}
	return s[n:]
}
