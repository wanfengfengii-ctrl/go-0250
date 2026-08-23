package acquisition

import (
	"errors"
	"math"
)

// Sentinel errors returned by the safe arithmetic functions. All arithmetic
// failures are returned without any derived conclusion being written.
var (
	ErrOverflow    = errors.New("acquisition: integer overflow")
	ErrDivZero     = errors.New("acquisition: division by zero")
	ErrNonNegative = errors.New("acquisition: input must be non-negative")
)

// SafeAdd returns a+b or an overflow error. It detects overflow for both the
// positive and negative directions.
func SafeAdd(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// SafeSub returns a-b or an overflow error.
func SafeSub(a, b int64) (int64, error) {
	if (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b) {
		return 0, ErrOverflow
	}
	return a - b, nil
}

// SafeMul returns a*b or an overflow error. The product is checked against
// MaxInt64/factor (and MinInt64) before executing the multiplication.
func SafeMul(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	if a > 0 {
		if b > 0 {
			if a > math.MaxInt64/b {
				return 0, ErrOverflow
			}
		} else {
			if b < math.MinInt64/a {
				return 0, ErrOverflow
			}
		}
	} else {
		if b > 0 {
			if a < math.MinInt64/b {
				return 0, ErrOverflow
			}
		} else {
			if a < math.MaxInt64/b {
				return 0, ErrOverflow
			}
		}
	}
	return a * b, nil
}

// SafeDiv returns a/b with exact integer division. A zero denominator is
// rejected. (Go integer division already truncates toward zero; the rejection
// is the safety guarantee required by the domain.)
func SafeDiv(a, b int64) (int64, error) {
	if b == 0 {
		return 0, ErrDivZero
	}
	return a / b, nil
}

// ScaleNonNegative computes (value*num)/den using safe multiplication and
// division, with half-away-from-zero rounding, and requires value, num and den
// to be non-negative. It is the ratio-conversion primitive used for integer
// pressure/flow/displacement conversions.
func ScaleNonNegative(value, num, den int64) (int64, error) {
	if value < 0 || num < 0 || den < 0 {
		return 0, ErrNonNegative
	}
	if den == 0 {
		return 0, ErrDivZero
	}
	numProduct, err := SafeMul(value, num)
	if err != nil {
		return 0, err
	}
	return DivRoundHalfAwayFromZero(numProduct, den)
}

// DivRoundHalfAwayFromZero returns num/den rounded half away from zero: a
// remainder whose doubled magnitude is at least the magnitude of the
// denominator rounds the quotient one step away from zero. den must be
// non-zero.
func DivRoundHalfAwayFromZero(num, den int64) (int64, error) {
	if den == 0 {
		return 0, ErrDivZero
	}
	q := num / den
	r := num % den

	absR := r
	if absR < 0 {
		absR = -absR
	}
	absDen := den
	if absDen < 0 {
		absDen = -absDen
	}

	// Half-away-from-zero means "round up in magnitude when the doubled
	// remainder is at least the denominator". Since |r| < |den|, comparing
	// |r| against |den|-|r| avoids any intermediate overflow.
	if absR >= absDen-absR {
		if num >= 0 {
			q++
		} else {
			q--
		}
	}
	return q, nil
}
