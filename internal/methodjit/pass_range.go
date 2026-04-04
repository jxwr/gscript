// pass_range.go implements forward dataflow range analysis for integer SSA
// values. It computes [min, max] ranges over the IR, then marks every
// AddInt/SubInt/MulInt/NegInt whose range provably fits in the signed int48
// space. The emitter consults `fn.Int48Safe` to skip the 3-instruction
// SBFX+CMP+B.NE overflow check on provably safe arithmetic.
//
// Motivation: loop counters are already exempted at graph-build time (Aux2=1),
// but operations *derived* from loop counters (e.g. `i+j`, `(i+j)*(i+j+1)` in
// the inlined body of A(i,j) from spectral_norm) still carry overflow checks.
// A single overflow check after every arithmetic op accounts for a ~3.7x
// regression on spectral_norm. Eliminating provably-safe checks recovers
// most of this.
//
// Algorithm (three phases):
//   Phase A: Seed loop-counter ranges from FORLOOP structure. When the
//     initial value, limit and step are all concrete ints, the counter's
//     range is [min(start,limit), max(start,limit)].
//   Phase B: Forward propagation via fixed-point iteration (RPO, cap=5).
//     Constants seed their own range. AddInt/SubInt/MulInt/NegInt/ModInt
//     propagate using saturating arithmetic. Phi nodes join (min-of-mins,
//     max-of-maxes). All other ops are "top" (unbounded).
//   Phase C: Populate fn.Int48Safe with IDs whose range fits in int48.

package methodjit

import "math"

// Signed int48 limits. Any int64 value within [MinInt48, MaxInt48] is
// guaranteed to round-trip through SBFX(x, 0, 48).
const (
	MinInt48 int64 = -(1 << 47)
	MaxInt48 int64 = (1 << 47) - 1
)

// intRange represents a closed interval [min, max]. When known=false the
// range is "top" (unbounded) and the value's final range is MinInt64/MaxInt64
// conceptually. We still fill min/max in that case as sentinels so callers
// that forget to check `known` read safe values.
type intRange struct {
	min, max int64
	known    bool
}

func topRange() intRange {
	return intRange{min: math.MinInt64, max: math.MaxInt64, known: false}
}

func pointRange(v int64) intRange {
	return intRange{min: v, max: v, known: true}
}

func (r intRange) fitsInt48() bool {
	return r.known && r.min >= MinInt48 && r.max <= MaxInt48
}

// rangeEqual reports whether two ranges are exactly the same (including
// known-status), used to detect phi convergence.
func rangeEqual(a, b intRange) bool {
	if a.known != b.known {
		return false
	}
	if !a.known {
		return true
	}
	return a.min == b.min && a.max == b.max
}

// join returns the union of two ranges. If either is top the result is top.
func joinRange(a, b intRange) intRange {
	if !a.known || !b.known {
		return topRange()
	}
	out := intRange{known: true, min: a.min, max: a.max}
	if b.min < out.min {
		out.min = b.min
	}
	if b.max > out.max {
		out.max = b.max
	}
	return out
}

// --- Saturating arithmetic helpers ---

func satAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func satSub(a, b int64) int64 {
	// a - b: reduce to satAdd(a, -b), careful with b=MinInt64.
	if b == math.MinInt64 {
		// -(MinInt64) overflows. If a >= 0, result saturates to MaxInt64;
		// otherwise a - MinInt64 = a + |MinInt64|, which overflows iff a < 0,
		// but we're in the a < 0 branch only if a == MinInt64 (impossible since
		// a >= 0 was handled). Conservatively saturate.
		if a >= 0 {
			return math.MaxInt64
		}
		return math.MaxInt64
	}
	return satAdd(a, -b)
}

func satMul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	// Detect overflow via division. Guard MinInt64 / -1 case.
	if a == math.MinInt64 || b == math.MinInt64 {
		// Multiplying MinInt64 by anything != 0, ±1 overflows; by ±1 is ±MinInt64
		// which still overflows for -1. Just saturate by sign.
		if (a < 0) == (b < 0) {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	result := a * b
	// Overflow iff sign of result disagrees with expected, or division doesn't
	// recover. Using division is robust for any non-MinInt64 operands.
	if result/b != a {
		if (a < 0) == (b < 0) {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	return result
}

func satNeg(a int64) int64 {
	if a == math.MinInt64 {
		return math.MaxInt64
	}
	return -a
}

// --- Range arithmetic ---

func addRange(a, b intRange) intRange {
	if !a.known || !b.known {
		return topRange()
	}
	lo := satAdd(a.min, b.min)
	hi := satAdd(a.max, b.max)
	if lo == math.MinInt64 || hi == math.MaxInt64 {
		// Saturation hit — treat as top to be safe (we don't want to claim
		// a false-narrow range that happens to fit int48).
		if lo == math.MinInt64 && a.min+b.min != math.MinInt64 {
			return topRange()
		}
		if hi == math.MaxInt64 && a.max+b.max != math.MaxInt64 {
			return topRange()
		}
	}
	return intRange{min: lo, max: hi, known: true}
}

func subRange(a, b intRange) intRange {
	if !a.known || !b.known {
		return topRange()
	}
	lo := satSub(a.min, b.max)
	hi := satSub(a.max, b.min)
	return intRange{min: lo, max: hi, known: true}
}

func mulRange(a, b intRange) intRange {
	if !a.known || !b.known {
		return topRange()
	}
	p1 := satMul(a.min, b.min)
	p2 := satMul(a.min, b.max)
	p3 := satMul(a.max, b.min)
	p4 := satMul(a.max, b.max)
	lo := p1
	hi := p1
	for _, p := range []int64{p2, p3, p4} {
		if p < lo {
			lo = p
		}
		if p > hi {
			hi = p
		}
	}
	return intRange{min: lo, max: hi, known: true}
}

func negRange(a intRange) intRange {
	if !a.known {
		return topRange()
	}
	return intRange{min: satNeg(a.max), max: satNeg(a.min), known: true}
}

func modRange(b intRange) intRange {
	// a % b has |result| < |b|. If b is unbounded or straddles zero we can
	// still derive a conservative bound from the larger |b| extreme, provided
	// at least one bound is finite.
	if !b.known {
		return topRange()
	}
	bound := int64(0)
	absMin := b.min
	if absMin < 0 {
		absMin = satNeg(absMin)
	}
	absMax := b.max
	if absMax < 0 {
		absMax = satNeg(absMax)
	}
	if absMin > bound {
		bound = absMin
	}
	if absMax > bound {
		bound = absMax
	}
	if bound == 0 {
		return topRange() // divisor is exactly zero, runtime error anyway
	}
	bound--
	return intRange{min: -bound, max: bound, known: true}
}

// --- Main pass ---

// RangeAnalysisPass computes integer ranges across the IR and marks every
// AddInt/SubInt/MulInt/NegInt whose range provably fits in signed int48.
// Populates `fn.Int48Safe` with safe value IDs.
func RangeAnalysisPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}

	ranges := make(map[int]intRange)

	// Phase A: seed loop counter ranges from FORLOOP structure.
	seedLoopRanges(fn, ranges)

	// Phase B: fixed-point propagation (RPO, capped at 5 passes).
	const maxIter = 5
	for iter := 0; iter < maxIter; iter++ {
		changed := false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				newR := computeRange(instr, ranges)
				if !instr.Type.isIntegerLike() {
					continue
				}
				if old, ok := ranges[instr.ID]; ok {
					if !rangeEqual(old, newR) {
						ranges[instr.ID] = newR
						changed = true
					}
				} else {
					ranges[instr.ID] = newR
					if newR.known {
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}

	// Phase C: populate Int48Safe for int-arithmetic ops whose range fits.
	safe := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAddInt, OpSubInt, OpMulInt, OpNegInt:
				if r, ok := ranges[instr.ID]; ok && r.fitsInt48() {
					safe[instr.ID] = true
				}
			}
		}
	}
	fn.Int48Safe = safe
	return fn, nil
}

// isIntegerLike returns true for IR types whose runtime value is a raw int
// that the range analysis can track. We restrict to TypeInt only — other
// types carry NaN-boxed values that range analysis doesn't model.
func (t Type) isIntegerLike() bool {
	return t == TypeInt
}

// computeRange returns the inferred range of `instr`'s result value using the
// current `ranges` map. Unknown/unsupported ops produce top.
func computeRange(instr *Instr, ranges map[int]intRange) intRange {
	switch instr.Op {
	case OpConstInt:
		return pointRange(instr.Aux)

	case OpAddInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return addRange(argRange(instr.Args[0], ranges), argRange(instr.Args[1], ranges))

	case OpSubInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return subRange(argRange(instr.Args[0], ranges), argRange(instr.Args[1], ranges))

	case OpMulInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return mulRange(argRange(instr.Args[0], ranges), argRange(instr.Args[1], ranges))

	case OpModInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return modRange(argRange(instr.Args[1], ranges))

	case OpNegInt:
		if len(instr.Args) < 1 {
			return topRange()
		}
		return negRange(argRange(instr.Args[0], ranges))

	case OpPhi:
		if len(instr.Args) == 0 {
			return topRange()
		}
		// If this phi already has a seeded range (from Phase A), start from it
		// so the loop induction range isn't widened beyond the seeded interval.
		// Phase A seeds are derived from the loop's bounds; joining with the
		// back-edge value (which is in that same range by construction) keeps
		// the range stable.
		if seeded, ok := ranges[instr.ID]; ok && seeded.known {
			return seeded
		}
		acc := argRange(instr.Args[0], ranges)
		for i := 1; i < len(instr.Args); i++ {
			acc = joinRange(acc, argRange(instr.Args[i], ranges))
			if !acc.known {
				break
			}
		}
		return acc

	case OpBoxInt:
		if len(instr.Args) < 1 {
			return topRange()
		}
		return argRange(instr.Args[0], ranges)

	case OpUnboxInt:
		if len(instr.Args) < 1 {
			return topRange()
		}
		return argRange(instr.Args[0], ranges)
	}
	return topRange()
}

// argRange resolves the range of an SSA value argument. Returns top if the
// value isn't in the map (e.g. function parameter, LoadSlot result).
func argRange(v *Value, ranges map[int]intRange) intRange {
	if v == nil || v.Def == nil {
		return topRange()
	}
	if r, ok := ranges[v.ID]; ok {
		return r
	}
	return topRange()
}

// --- Phase A: seed loop-counter ranges ---

// seedLoopRanges scans blocks for FORLOOP structure and seeds the induction
// phi's range based on the static loop bounds.
//
// Structure recognized:
//   - A loop header block has a Phi at the start.
//   - One of the phi's inputs is an OpAdd/OpAddInt with Aux2=1 (emitted by
//     FORLOOP back-edge), whose first arg is the phi itself.
//   - The block containing this back-edge add also contains an OpLe/OpLeInt
//     whose first arg is the back-edge add; its second arg is the limit.
//   - The other phi input is the loop-entry value: either an OpSub/OpSubInt
//     with Aux2=1 (emitted by FORPREP), or a direct ConstInt when ConstProp
//     has already folded the subtraction.
//
// When start/limit/step all resolve to concrete ints, we seed both the
// induction phi and the back-edge add with `[lo, hi]` expanded by |step|
// to cover the full trajectory including post-increment/exit values.
func seedLoopRanges(fn *Function, ranges map[int]intRange) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				break // phis live only at block entry
			}
			// Find the back-edge Add (Aux2=1, first arg = phi itself).
			var backAdd *Instr
			var initialArg *Value
			for _, arg := range instr.Args {
				if arg == nil || arg.Def == nil {
					continue
				}
				def := arg.Def
				if (def.Op == OpAdd || def.Op == OpAddInt) && def.Aux2 == 1 &&
					len(def.Args) >= 1 && def.Args[0] != nil && def.Args[0].ID == instr.ID {
					backAdd = def
					continue
				}
				initialArg = arg
			}
			if backAdd == nil || initialArg == nil {
				continue
			}

			// Resolve the step from the back-edge Add's Args[1].
			stepVal, stepOk := constIntFromValue(backAdd.Args[1])
			if !stepOk {
				continue
			}

			// Resolve the initial counter value from initialArg. Two forms:
			//   (a) OpSub/OpSubInt with Aux2=1: initialCounter = Args[0] - Args[1]
			//   (b) ConstInt (ConstProp folded the sub): initialCounter = Aux
			var initialCounter int64
			var initOk bool
			if initialArg.Def != nil {
				def := initialArg.Def
				switch def.Op {
				case OpSub, OpSubInt:
					if def.Aux2 == 1 && len(def.Args) >= 2 {
						s, ok1 := constIntFromValue(def.Args[0])
						k, ok2 := constIntFromValue(def.Args[1])
						if ok1 && ok2 {
							initialCounter = s - k
							initOk = true
						}
					}
				case OpConstInt:
					initialCounter = def.Aux
					initOk = true
				}
			}
			if !initOk {
				continue
			}

			// Find the limit: look for OpLe/OpLeInt in backAdd's block whose
			// first arg is backAdd and whose second arg is a ConstInt.
			var limitVal int64
			var limitOk bool
			if backAdd.Block != nil {
				for _, bi := range backAdd.Block.Instrs {
					if bi.Op != OpLe && bi.Op != OpLeInt {
						continue
					}
					if len(bi.Args) < 2 {
						continue
					}
					if bi.Args[0] == nil || bi.Args[0].ID != backAdd.ID {
						continue
					}
					if lv, lOk := constIntFromValue(bi.Args[1]); lOk {
						limitVal = lv
						limitOk = true
						break
					}
				}
			}
			if !limitOk {
				continue
			}

			// Bounding interval: [min(initialCounter, limit), max(...)].
			// Expand by |step| on both sides to cover post-increment extremes
			// and any guard slack.
			lo := initialCounter
			hi := limitVal
			if lo > hi {
				lo, hi = hi, lo
			}
			absStep := stepVal
			if absStep < 0 {
				absStep = -absStep
			}
			lo = satSub(lo, absStep)
			hi = satAdd(hi, absStep)

			seeded := intRange{min: lo, max: hi, known: true}
			ranges[instr.ID] = seeded
			ranges[backAdd.ID] = seeded
		}
	}
}

func constIntFromValue(v *Value) (int64, bool) {
	if v == nil || v.Def == nil {
		return 0, false
	}
	if v.Def.Op != OpConstInt {
		return 0, false
	}
	return v.Def.Aux, true
}
