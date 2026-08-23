package acquisition

import (
	"errors"
	"math"
	"testing"
)

func TestSafeAdd(t *testing.T) {
	cases := []struct {
		name string
		a, b int64
		want int64
		err  error
	}{
		{"basic", 1, 2, 3, nil},
		{"max ok", math.MaxInt64, 0, math.MaxInt64, nil},
		{"overflow positive", math.MaxInt64, 1, 0, ErrOverflow},
		{"overflow negative", math.MinInt64, -1, 0, ErrOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SafeAdd(c.a, c.b)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if err == nil && got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestSafeMul(t *testing.T) {
	cases := []struct {
		name string
		a, b int64
		want int64
		err  error
	}{
		{"zero", 0, math.MaxInt64, 0, nil},
		{"max safe product", math.MaxInt64 / 7, 7, math.MaxInt64 / 7 * 7, nil},
		{"overflow positive", math.MaxInt64, 2, 0, ErrOverflow},
		{"overflow negative", math.MinInt64, 2, 0, ErrOverflow},
		{"overflow mixed", math.MinInt64, -1, 0, ErrOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SafeMul(c.a, c.b)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v, want %v", err, c.err)
			}
			if err == nil && got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestSafeDiv(t *testing.T) {
	if _, err := SafeDiv(10, 0); !errors.Is(err, ErrDivZero) {
		t.Fatalf("want ErrDivZero, got %v", err)
	}
	got, err := SafeDiv(7, 2)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestDivRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		num, den int64
		want     int64
	}{
		{5, 2, 3},   // 2.5 -> 3 (away from zero)
		{4, 2, 2},   // 2.0 -> 2
		{3, 2, 2},   // 1.5 -> 2
		{1, 2, 1},   // 0.5 -> 1
		{-5, 2, -3}, // -2.5 -> -3 (away from zero)
		{-1, 2, -1}, // -0.5 -> -1
		{-3, 2, -2}, // -1.5 -> -2
	}
	for _, c := range cases {
		got, err := DivRoundHalfAwayFromZero(c.num, c.den)
		if err != nil {
			t.Fatalf("num=%d den=%d unexpected err %v", c.num, c.den, err)
		}
		if got != c.want {
			t.Fatalf("num=%d den=%d got %d, want %d", c.num, c.den, got, c.want)
		}
	}
	if _, err := DivRoundHalfAwayFromZero(1, 0); !errors.Is(err, ErrDivZero) {
		t.Fatalf("want ErrDivZero, got %v", err)
	}
}

func TestScaleNonNegative(t *testing.T) {
	// 1000 * 3 / 2 = 1500 exactly.
	got, err := ScaleNonNegative(1000, 3, 2)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if got != 1500 {
		t.Fatalf("got %d, want 1500", got)
	}
	// Half-away-from-zero on the boundary: 1 * 1 / 2 = 0.5 -> 1.
	got, err = ScaleNonNegative(1, 1, 2)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
	// Negative input is rejected.
	if _, err := ScaleNonNegative(-1, 1, 2); !errors.Is(err, ErrNonNegative) {
		t.Fatalf("want ErrNonNegative, got %v", err)
	}
	// Overflow is propagated, not silently truncated.
	if _, err := ScaleNonNegative(math.MaxInt64, 2, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}
