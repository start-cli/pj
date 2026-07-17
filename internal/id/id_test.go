package id

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestIsScopeName(t *testing.T) {
	valid := []string{"wc", "webctl", "ilili", "a", "abcdefghijkl", "api", "x1", "0a"}
	for _, s := range valid {
		if !IsScopeName(s) {
			t.Errorf("IsScopeName(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "abcdefghijklm", "WC", "web-control", "web_ctl", "wc ", "wc.", "wc-", "über"}
	for _, s := range invalid {
		if IsScopeName(s) {
			t.Errorf("IsScopeName(%q) = true, want false", s)
		}
	}
}

func TestIsShortID(t *testing.T) {
	valid := []string{"ab2c", "ab2c9", "wxyz", "a234", "abcdefgh"}
	for _, s := range valid {
		if !IsShortID(s) {
			t.Errorf("IsShortID(%q) = false, want true", s)
		}
	}
	// The illegal cases a loose ^[a-z0-9]{4,8}$ would wrongly accept.
	invalid := []string{
		"",          // empty
		"ab2",       // too short
		"abcdefghi", // too long
		"10il",      // digit-leading and dropped chars i, l
		"0000",      // all dropped digits, digit-leading
		"2abc",      // digit-leading
		"abio",      // contains dropped letters i and o
		"ab1c",      // contains dropped digit 1
		"ab0c",      // contains dropped digit 0
		"able",      // contains dropped letter l
		"AB2C",      // uppercase
		"ab-c",      // hyphen
	}
	for _, s := range invalid {
		if IsShortID(s) {
			t.Errorf("IsShortID(%q) = true, want false", s)
		}
	}
}

func TestIsFullProjectID(t *testing.T) {
	valid := []string{"wc-ab2c", "wc-ab2c9", "api-m9k3", "x-wxyz", "webctl-abcdefgh"}
	for _, s := range valid {
		if !IsFullProjectID(s) {
			t.Errorf("IsFullProjectID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",           // empty
		"wc",         // no separator
		"api-10il",   // short-id digit-leading + dropped chars
		"wc-0000",    // short-id all dropped digits
		"wc-9k3m",    // short-id digit-leading (illegal despite design examples)
		"api-3m9k",   // short-id digit-leading
		"wc-ab2",     // short-id too short
		"WC-ab2c",    // uppercase scope
		"wc--ab2c",   // remainder contains '-'
		"wc-ab-2c",   // two separators
		"web_c-ab2c", // scope has underscore
		"-ab2c",      // empty scope
		"wc-",        // empty short-id
	}
	for _, s := range invalid {
		if IsFullProjectID(s) {
			t.Errorf("IsFullProjectID(%q) = true, want false", s)
		}
	}
}

func TestMintShape(t *testing.T) {
	// A high-entropy source so rejection sampling rarely re-draws; every mint
	// must be a legal length-4 short-id.
	for i := 0; i < 2000; i++ {
		got, err := Mint(rand.Reader)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if len(got) != ShortIDMin {
			t.Fatalf("Mint() = %q, want length %d", got, ShortIDMin)
		}
		if !IsShortID(got) {
			t.Fatalf("Mint() = %q, not a legal short-id", got)
		}
		if strings.IndexByte(LetterAlphabet, got[0]) < 0 {
			t.Fatalf("Mint() = %q, first char not a letter", got)
		}
	}
}

func TestMintDeterministicWithFixedSource(t *testing.T) {
	src := bytes.Repeat([]byte{0x00}, 16)
	a, err := Mint(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	b, err := Mint(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if a != b {
		t.Fatalf("Mint not deterministic for identical source: %q vs %q", a, b)
	}
	// All-zero bytes: first char = letter[0], each coin=0 (letter class),
	// each value = letter[0], so "aaaa".
	if a != "aaaa" {
		t.Fatalf("Mint(all-zero) = %q, want aaaa", a)
	}
}

func TestMintExhaustedSource(t *testing.T) {
	_, err := Mint(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("Mint with an empty source should error")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Mint empty source err = %v, want io.EOF", err)
	}
}

func TestExtendGrowth(t *testing.T) {
	// Normal case: first free single-char extension over the ordered alphabet.
	got, err := Extend("ab2c", occupiedSet())
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if got != "ab2ca" {
		t.Fatalf("Extend(ab2c, {}) = %q, want ab2ca (first alphabet char)", got)
	}
	if !IsShortID(got) {
		t.Fatalf("Extend produced non-short-id %q", got)
	}
}

func TestExtendSkipsOccupied(t *testing.T) {
	occ := occupiedSet("ab2ca", "ab2cb", "ab2cc")
	got, err := Extend("ab2c", occ)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if got != "ab2cd" {
		t.Fatalf("Extend skipping abc = %q, want ab2cd", got)
	}
}

func TestExtendDeterministic(t *testing.T) {
	occ := occupiedSet("ab2ca", "ab2cb")
	first, _ := Extend("ab2c", occ)
	second, _ := Extend("ab2c", occ)
	if first != second {
		t.Fatalf("Extend not deterministic: %q vs %q", first, second)
	}
}

func TestExtendGrowsLengthWhenBlocked(t *testing.T) {
	// Block every single-char extension so repair must grow to length 6.
	occ := occupiedSet()
	for i := 0; i < len(ShortIDAlphabet); i++ {
		occ["ab2c"+string(ShortIDAlphabet[i])] = struct{}{}
	}
	got, err := Extend("ab2c", occ)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if len(got) != 6 || got != "ab2caa" {
		t.Fatalf("Extend with all length-5 blocked = %q, want ab2caa", got)
	}
}

func TestExtendCapExhaustion(t *testing.T) {
	// A length-8 prefix has no room to grow (cap is ShortIDMax = 8).
	if _, err := Extend("abcdefgh", occupiedSet()); err == nil {
		t.Fatal("Extend on a max-length prefix should hard-fail")
	}
}

func TestExtendRejectsBadPrefix(t *testing.T) {
	if _, err := Extend("10il", occupiedSet()); err == nil {
		t.Fatal("Extend should reject a prefix that is not a legal short-id")
	}
}

func occupiedSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, s := range ids {
		m[s] = struct{}{}
	}
	return m
}
