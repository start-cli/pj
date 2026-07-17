package order

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestKeyBetweenFixtures(t *testing.T) {
	tests := []struct {
		name    string
		left    string
		right   string
		want    string
		wantErr error
	}{
		{name: "null null is integer zero", left: "", right: "", want: "a0"},
		{name: "append from a0", left: "a0", right: "", want: "a1"},
		{name: "prepend before a0", left: "", right: "a0", want: "Zz"},
		{name: "densify adjacent integers", left: "a0", right: "a1", want: "a0V"},
		{name: "densify adjacent fractions", left: "aV", right: "aW", want: "aVV"},
		{name: "open interval across integer boundary", left: "a9", right: "b00", want: "aA"},
		{name: "between negative and zero", left: "Zz", right: "a0", want: "ZzV"},
		{name: "integer overflow widens head", left: "az", right: "", want: "b00"},
		{name: "integer underflow widens head", left: "", right: "Z0", want: "Yzz"},
		{name: "densify under smallest integer", left: "", right: SmallestInteger + "V", want: SmallestInteger + "G"},
		{name: "equal keys have no between", left: "a5", right: "a5", wantErr: ErrEqualKeys},
		{name: "reversed bounds rejected", left: "a5", right: "a1", wantErr: ErrUnorderedBounds},
		{name: "floor exhaustion prepend", left: "", right: SmallestInteger, wantErr: ErrExhausted},
		{name: "invalid left rejected", left: "a", right: "", wantErr: ErrInvalidKey},
		{name: "invalid trailing-zero fraction rejected", left: "a1V0", right: "", wantErr: ErrInvalidKey},
		{name: "invalid head rejected", left: "09", right: "", wantErr: ErrInvalidKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := KeyBetween(tt.left, tt.right)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("KeyBetween(%q, %q) err = %v, want %v", tt.left, tt.right, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("KeyBetween(%q, %q) unexpected err: %v", tt.left, tt.right, err)
			}
			if got != tt.want {
				t.Fatalf("KeyBetween(%q, %q) = %q, want %q", tt.left, tt.right, got, tt.want)
			}
			if !Valid(got) {
				t.Fatalf("KeyBetween(%q, %q) = %q which fails Valid", tt.left, tt.right, got)
			}
			assertStrictlyBetween(t, tt.left, tt.right, got)
		})
	}
}

func TestRepeatedAppendStrictlyIncreasing(t *testing.T) {
	prev := ""
	widened := false
	for i := 0; i < 200; i++ {
		next, err := KeyBetween(prev, "")
		if err != nil {
			t.Fatalf("append step %d: %v", i, err)
		}
		if !Valid(next) {
			t.Fatalf("append step %d produced invalid key %q", i, next)
		}
		if prev != "" && prev >= next {
			t.Fatalf("append step %d not increasing: %q !< %q", i, prev, next)
		}
		if prev != "" && len(next) > len(prev) {
			widened = true
		}
		prev = next
	}
	if !widened {
		t.Fatal("expected the integer part to widen across 200 appends")
	}
}

func TestRepeatedPrependStrictlyDecreasing(t *testing.T) {
	prev := ""
	widened := false
	for i := 0; i < 200; i++ {
		next, err := KeyBetween("", prev)
		if err != nil {
			t.Fatalf("prepend step %d: %v", i, err)
		}
		if !Valid(next) {
			t.Fatalf("prepend step %d produced invalid key %q", i, next)
		}
		if prev != "" && next >= prev {
			t.Fatalf("prepend step %d not decreasing: %q !< %q", i, next, prev)
		}
		if prev != "" && len(next) > len(prev) {
			widened = true
		}
		prev = next
	}
	if !widened {
		t.Fatal("expected the negative integer head to widen across 200 prepends")
	}
}

func TestSameIntegerDensifyGrowsFraction(t *testing.T) {
	// Repeatedly insert just after "a0" and before its successor; the fraction
	// must grow, stay strictly between, and never end in the min digit '0'.
	left, right := "a0", "a1"
	prevLen := 0
	grew := false
	for i := 0; i < 20; i++ {
		mid, err := KeyBetween(left, right)
		if err != nil {
			t.Fatalf("densify step %d: %v", i, err)
		}
		if left >= mid || mid >= right {
			t.Fatalf("densify step %d not strictly between: %q < %q < %q", i, left, mid, right)
		}
		if strings.HasSuffix(mid, "0") {
			t.Fatalf("densify step %d ends in '0': %q", i, mid)
		}
		if !Valid(mid) {
			t.Fatalf("densify step %d invalid: %q", i, mid)
		}
		if len(mid) > prevLen {
			grew = true
		}
		prevLen = len(mid)
		right = mid // keep shrinking the gap against the same left
	}
	if !grew {
		t.Fatal("expected fraction length to grow while densifying a shrinking gap")
	}
}

func TestIncrementIntegerCeilingExhausts(t *testing.T) {
	maxInt := "z" + strings.Repeat("z", 26)
	if _, ok := incrementInteger(maxInt); ok {
		t.Fatal("incrementInteger at maximum positive integer should report exhaustion")
	}
	// KeyBetween append past the ceiling does not error; it grows the fraction.
	got, err := KeyBetween(maxInt, "")
	if err != nil {
		t.Fatalf("append past ceiling should grow fraction, got err: %v", err)
	}
	if got <= maxInt || !Valid(got) {
		t.Fatalf("append past ceiling = %q, not a valid greater key", got)
	}
}

func TestDecrementIntegerFloorExhausts(t *testing.T) {
	if _, ok := decrementInteger(SmallestInteger); ok {
		t.Fatal("decrementInteger at SmallestInteger should report exhaustion")
	}
}

func TestValid(t *testing.T) {
	valid := []string{"a0", "a1", "Z9", "b00", "Yzz", "a0V", SmallestInteger, "z" + strings.Repeat("z", 26)}
	for _, k := range valid {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false, want true", k)
		}
	}
	invalid := []string{
		"",     // empty
		"a",    // shorter than head length
		"a1V0", // fraction ends in '0'
		"09",   // digit head
		"0a",   // digit head
		"a!",   // non-alphabet char
		"a1 ",  // space is not in the alphabet
		"~0",   // head out of range
		"b0",   // head 'b' needs two digits
	}
	for _, k := range invalid {
		if Valid(k) {
			t.Errorf("Valid(%q) = true, want false", k)
		}
	}
}

func TestByteOrderEqualsRankOrderAndRoundTrip(t *testing.T) {
	produced, finalSorted := loadFixture(t)
	// Every emitted key round-trips through validation.
	for _, p := range produced {
		if !Valid(p.Key) {
			t.Fatalf("fixture key %q fails Valid", p.Key)
		}
	}
	// The design's insertion order must match byte-wise sort order.
	shuffled := append([]string(nil), finalSorted...)
	sort.Slice(shuffled, func(i, j int) bool { return shuffled[i] < shuffled[j] })
	for i := range finalSorted {
		if shuffled[i] != finalSorted[i] {
			t.Fatalf("byte-wise sort diverges from rank order at %d: %q vs %q", i, shuffled[i], finalSorted[i])
		}
	}
}

// TestCrossCheckAgainstReference replays the exact deterministic operation
// sequence the reference JS produced and asserts byte-identical keys, locking
// this port to the Rocicorp construction.
func TestCrossCheckAgainstReference(t *testing.T) {
	produced, finalSorted := loadFixture(t)

	x := int64(42)
	rnd := func() int64 {
		x = (x*1103515245 + 12345) % 2147483648
		return x
	}
	var keys []string
	for step, want := range produced {
		var op int64
		if len(keys) < 2 {
			if len(keys) == 0 {
				op = 0
			} else {
				op = rnd() % 2
			}
		} else {
			op = rnd() % 3
		}
		var got string
		var err error
		switch op {
		case 0:
			left := ""
			if len(keys) > 0 {
				left = keys[len(keys)-1]
			}
			got, err = KeyBetween(left, "")
			keys = append(keys, got)
		case 1:
			right := ""
			if len(keys) > 0 {
				right = keys[0]
			}
			got, err = KeyBetween("", right)
			keys = append([]string{got}, keys...)
		default:
			i := int(rnd() % int64(len(keys)-1))
			got, err = KeyBetween(keys[i], keys[i+1])
			keys = append(keys[:i+1], append([]string{got}, keys[i+1:]...)...)
		}
		if err != nil {
			t.Fatalf("step %d op %d: %v", step, op, err)
		}
		if int(op) != want.Op || got != want.Key {
			t.Fatalf("step %d: got op=%d key=%q, want op=%d key=%q", step, op, got, want.Op, want.Key)
		}
	}
	if len(keys) != len(finalSorted) {
		t.Fatalf("final key count %d, want %d", len(keys), len(finalSorted))
	}
	for i := range keys {
		if keys[i] != finalSorted[i] {
			t.Fatalf("final key %d = %q, want %q", i, keys[i], finalSorted[i])
		}
	}
}

func TestConstants(t *testing.T) {
	if IntegerZero != "a0" {
		t.Errorf("IntegerZero = %q, want a0", IntegerZero)
	}
	if SmallestInteger != "A"+strings.Repeat("0", 26) {
		t.Errorf("SmallestInteger = %q, want A followed by 26 zeros", SmallestInteger)
	}
}

func assertStrictlyBetween(t *testing.T, left, right, got string) {
	t.Helper()
	if left != "" && got <= left {
		t.Fatalf("result %q not strictly after left %q", got, left)
	}
	if right != "" && got >= right {
		t.Fatalf("result %q not strictly before right %q", got, right)
	}
}

type producedKey struct {
	Step int    `json:"step"`
	Op   int    `json:"op"`
	Key  string `json:"key"`
}

func loadFixture(t *testing.T) ([]producedKey, []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "order_fixture.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Produced    []producedKey `json:"produced"`
		FinalSorted []string      `json:"finalSorted"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return fixture.Produced, fixture.FinalSorted
}
