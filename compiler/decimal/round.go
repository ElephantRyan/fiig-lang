package decimal

import (
	"fmt"
	"math/big"
)

// Rounding is ADR 0064 §4's prelude enum, in declaration order.
type Rounding int

const (
	HalfUp   Rounding = iota // ties away from zero
	HalfEven                 // ties to the even digit
	Up                       // away from zero
	Down                     // toward zero
	Ceiling                  // toward +infinity
	Floor                    // toward -infinity
)

// RoundingNames are the enum's variants, indexed by Rounding.
var RoundingNames = []string{"HalfUp", "HalfEven", "Up", "Down", "Ceiling", "Floor"}

// minPlaces bounds a negative places argument: every quotient of two
// decimals is below 10^69, so rounding to 10^70 or coarser already gives 0
// or a value over the cap, as any coarser place would.
const minPlaces = -2*Digits - 2

// roundQuo is num / den rounded once to an integer in mode. den > 0.
func roundQuo(num, den *big.Int, mode Rounding) *big.Int {
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	neg := num.Sign() < 0
	var away bool
	switch mode {
	case Up:
		away = true
	case Ceiling:
		away = !neg
	case Floor:
		away = neg
	case HalfUp, HalfEven:
		twice := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
		c := twice.Cmp(den)
		away = c > 0 || c == 0 && (mode == HalfUp || q.Bit(0) == 1)
	}
	if away {
		if neg {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return q
}

// Round is d rounded to places digits after the point in mode, at scale
// places (zero when places is negative, so -2 rounds to the nearest
// hundred). false means the result doesn't fit.
func (d Decimal) Round(places int, mode Rounding) (Decimal, bool) {
	if places >= d.scale {
		// Exact: only zeros are added, and New trims any beyond the cap.
		places = min(places, Digits)
		return New(new(big.Int).Mul(d.c(), pow10(places-d.scale)), places)
	}
	places = max(places, minPlaces)
	return New(roundQuo(d.c(), pow10(d.scale-places), mode), places)
}

// Quo is d / e computed exactly and rounded once, to places in mode (ADR
// 0064 §3): never to 34 digits first, which would round ties twice. e must
// not be zero; false means the quotient doesn't fit at places.
func Quo(d, e Decimal, places int, mode Rounding) (Decimal, bool) {
	num := new(big.Int).Mul(d.c(), pow10(e.scale))
	den := new(big.Int).Mul(e.c(), pow10(d.scale))
	if den.Sign() < 0 {
		num.Neg(num)
		den.Neg(den)
	}
	// Past 34 + digits(den) places, a quotient that isn't exact at 34 places
	// is more than 10^(34-places) from every 34-place value, so the verdict
	// and the value are those at that bound, and 10^places is never built.
	places = min(places, Digits+numDigits(den)+1)
	places = max(places, minPlaces)
	if places >= 0 {
		num.Mul(num, pow10(places))
	} else {
		den.Mul(den, pow10(-places))
	}
	return New(roundQuo(num, den, mode), places)
}

// Int64 is d rounded to a whole number in mode, as an int64; false when
// that is outside int64 (ADR 0064 §6's crossings).
func (d Decimal) Int64(mode Rounding) (int64, bool) {
	n := roundQuo(d.c(), pow10(d.scale), mode)
	return n.Int64(), n.IsInt64()
}

// MaxParts bounds Allocate's parts, so a mistaken count is a fault naming
// the problem rather than an attempt to build a list the process can't
// hold.
const MaxParts = 10_000_000

// Allocate splits amount into parts shares at places that add back to
// exactly amount (ADR 0064 §3a): each share is amount / parts rounded
// toward zero, and the remainder, a whole number of units in the last
// place, goes one unit at a time to the first shares. problem is "" or
// completes "math.allocate(...) ...": parts below 1, or an amount with
// more places than places.
func Allocate(amount Decimal, parts int64, places int) ([]Decimal, string) {
	if parts < 1 {
		return nil, "needs at least 1 part"
	}
	if parts > MaxParts {
		return nil, fmt.Sprintf("can split into at most %d parts", MaxParts)
	}
	if r, ok := amount.Round(places, Down); !ok || r.Cmp(amount) != 0 {
		return nil, "has an amount with more decimal places than places"
	}
	// Past 34 places a split fits only if it is exact at 34, and then it is
	// the same split; below -35 places the amount can only be zero.
	working := min(max(places, -Digits-1), Digits)
	units := new(big.Int).Set(amount.c())
	if working >= amount.scale {
		units.Mul(units, pow10(working-amount.scale))
	} else {
		units.Quo(units, pow10(amount.scale-working))
	}
	q, rem := new(big.Int).QuoRem(units, big.NewInt(parts), new(big.Int))
	if working < places && rem.Sign() != 0 {
		return nil, "does not fit in decimal (34 digits written in full)"
	}
	extra := rem.Int64() // |rem| < parts, so it fits
	step := big.NewInt(1)
	if extra < 0 {
		extra, step = -extra, big.NewInt(-1)
	}
	plus, _ := New(new(big.Int).Add(q, step), working)
	plain, _ := New(q, working)
	shares := make([]Decimal, parts)
	for i := range shares {
		shares[i] = plain
		if int64(i) < extra {
			shares[i] = plus
		}
	}
	return shares, ""
}
