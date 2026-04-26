// pass_range.go implements forward dataflow range analysis for integer SSA
// values. It computes [min, max] ranges over the IR, then marks every
// AddInt/SubInt/MulInt/DivIntExact/NegInt whose range provably fits in the signed int48
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
//   Phase A: Seed loop-counter ranges from FORLOOP structure and from
//     guarded while-style forward induction variables. When the loop guard
//     compares an induction-derived expression against an int bound, the
//     counter is capped by the guard plus one positive step.
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

func divExactRange(a, b intRange) intRange {
	if !a.known || !b.known {
		return topRange()
	}
	if b.min <= 0 && b.max >= 0 {
		return topRange()
	}
	qs := []int64{
		safeDivBound(a.min, b.min),
		safeDivBound(a.min, b.max),
		safeDivBound(a.max, b.min),
		safeDivBound(a.max, b.max),
	}
	lo, hi := qs[0], qs[0]
	for _, q := range qs[1:] {
		if q < lo {
			lo = q
		}
		if q > hi {
			hi = q
		}
	}
	return intRange{min: lo, max: hi, known: true}
}

func safeDivBound(a, b int64) int64 {
	if b == 0 {
		return math.MaxInt64
	}
	if a == math.MinInt64 && b == -1 {
		return math.MaxInt64
	}
	return a / b
}

// --- Main pass ---

// RangeAnalysisPass computes integer ranges across the IR and marks every
// AddInt/SubInt/MulInt/DivIntExact/NegInt whose range provably fits in signed int48.
// It also records ModInt facts whose operands make the native ARM64 remainder
// sequence equivalent to Lua modulo semantics.
func RangeAnalysisPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}

	ranges := make(map[int]intRange)

	// Phase A: seed loop counter ranges from FORLOOP/while-loop structure.
	seedLoopRanges(fn, ranges)
	seedGuardedForwardInductionRanges(fn, ranges)

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
			case OpAddInt, OpSubInt, OpMulInt, OpDivIntExact, OpNegInt:
				if r, ok := ranges[instr.ID]; ok && r.fitsInt48() {
					safe[instr.ID] = true
				}
			}
		}
	}
	markConvergingInductionSafe(fn, safe)
	fn.Int48Safe = safe
	fn.IntRanges = ranges
	populateIntModFacts(fn, ranges)
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

	case OpDivIntExact:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return divExactRange(argRange(instr.Args[0], ranges), argRange(instr.Args[1], ranges))

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

func populateIntModFacts(fn *Function, baseRanges map[int]intRange) {
	nonZeroDivisor := make(map[int]bool)
	noSignAdjust := make(map[int]bool)
	blockEntries := computeBlockEntryRanges(fn, baseRanges)

	for _, block := range fn.Blocks {
		env := cloneRangeMap(blockEntries[block.ID])
		for _, instr := range block.Instrs {
			if instr.Op == OpModInt && len(instr.Args) >= 2 {
				lhs := argRangeInEnv(instr.Args[0], env, baseRanges)
				rhs := argRangeInEnv(instr.Args[1], env, baseRanges)
				if rangeExcludesZero(rhs) {
					nonZeroDivisor[instr.ID] = true
				}
				if rangesHaveSameKnownModuloSign(lhs, rhs) {
					noSignAdjust[instr.ID] = true
				}
			}
			if instr.Type.isIntegerLike() {
				env[instr.ID] = computeRangeInEnv(instr, env, baseRanges)
			}
		}
	}

	fn.IntModNonZeroDivisor = nonZeroDivisor
	fn.IntModNoSignAdjust = noSignAdjust
}

func computeBlockEntryRanges(fn *Function, baseRanges map[int]intRange) map[int]map[int]intRange {
	entries := make(map[int]map[int]intRange, len(fn.Blocks))
	if fn.Entry != nil {
		entries[fn.Entry.ID] = make(map[int]intRange)
	}

	const maxIter = 8
	for iter := 0; iter < maxIter; iter++ {
		changed := false
		for _, block := range fn.Blocks {
			env := cloneRangeMap(entries[block.ID])
			for _, instr := range block.Instrs {
				if instr.Type.isIntegerLike() {
					env[instr.ID] = computeRangeInEnv(instr, env, baseRanges)
				}
			}
			if len(block.Instrs) == 0 {
				continue
			}
			term := block.Instrs[len(block.Instrs)-1]
			if term.Op == OpBranch && len(term.Args) > 0 && len(block.Succs) >= 2 {
				trueEnv := cloneRangeMap(env)
				falseEnv := cloneRangeMap(env)
				refineBranchEnvs(term.Args[0], trueEnv, falseEnv)
				if mergeBlockEntry(entries, block.Succs[0], trueEnv) {
					changed = true
				}
				if mergeBlockEntry(entries, block.Succs[1], falseEnv) {
					changed = true
				}
				continue
			}
			for _, succ := range block.Succs {
				if mergeBlockEntry(entries, succ, env) {
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return entries
}

func computeRangeInEnv(instr *Instr, env, baseRanges map[int]intRange) intRange {
	switch instr.Op {
	case OpConstInt:
		return pointRange(instr.Aux)
	case OpAddInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return addRange(argRangeInEnv(instr.Args[0], env, baseRanges), argRangeInEnv(instr.Args[1], env, baseRanges))
	case OpSubInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return subRange(argRangeInEnv(instr.Args[0], env, baseRanges), argRangeInEnv(instr.Args[1], env, baseRanges))
	case OpMulInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return mulRange(argRangeInEnv(instr.Args[0], env, baseRanges), argRangeInEnv(instr.Args[1], env, baseRanges))
	case OpModInt:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return modRange(argRangeInEnv(instr.Args[1], env, baseRanges))
	case OpDivIntExact:
		if len(instr.Args) < 2 {
			return topRange()
		}
		return divExactRange(argRangeInEnv(instr.Args[0], env, baseRanges), argRangeInEnv(instr.Args[1], env, baseRanges))
	case OpNegInt:
		if len(instr.Args) < 1 {
			return topRange()
		}
		return negRange(argRangeInEnv(instr.Args[0], env, baseRanges))
	case OpPhi:
		if r, ok := baseRanges[instr.ID]; ok && r.known {
			return r
		}
		if len(instr.Args) == 0 {
			return topRange()
		}
		acc := argRangeInEnv(instr.Args[0], env, baseRanges)
		for i := 1; i < len(instr.Args); i++ {
			acc = joinRange(acc, argRangeInEnv(instr.Args[i], env, baseRanges))
			if !acc.known {
				break
			}
		}
		return acc
	case OpBoxInt, OpUnboxInt:
		if len(instr.Args) < 1 {
			return topRange()
		}
		return argRangeInEnv(instr.Args[0], env, baseRanges)
	}
	if r, ok := baseRanges[instr.ID]; ok {
		return r
	}
	return topRange()
}

func argRangeInEnv(v *Value, env, baseRanges map[int]intRange) intRange {
	if v == nil || v.Def == nil {
		return topRange()
	}
	if r, ok := env[v.ID]; ok && r.known {
		return r
	}
	if r, ok := baseRanges[v.ID]; ok {
		return r
	}
	return topRange()
}

func refineBranchEnvs(condValue *Value, trueEnv, falseEnv map[int]intRange) {
	if condValue == nil || condValue.Def == nil || len(condValue.Def.Args) < 2 {
		return
	}
	cond := condValue.Def
	switch cond.Op {
	case OpLtInt, OpLt:
		refineComparison(cond.Args[0], cond.Args[1], trueEnv, falseEnv, true)
	case OpLeInt, OpLe:
		refineComparison(cond.Args[0], cond.Args[1], trueEnv, falseEnv, false)
	}
}

func refineComparison(lhs, rhs *Value, trueEnv, falseEnv map[int]intRange, strict bool) {
	if c, ok := constIntFromValue(rhs); ok && lhs != nil {
		trueMax := c
		falseMin := c
		if strict {
			trueMax = satSub(c, 1)
		} else {
			falseMin = satAdd(c, 1)
		}
		constrainUpper(trueEnv, lhs.ID, trueMax)
		constrainLower(falseEnv, lhs.ID, falseMin)
		return
	}
	if c, ok := constIntFromValue(lhs); ok && rhs != nil {
		trueMin := c
		falseMax := c
		if strict {
			trueMin = satAdd(c, 1)
		} else {
			falseMax = satSub(c, 1)
		}
		constrainLower(trueEnv, rhs.ID, trueMin)
		constrainUpper(falseEnv, rhs.ID, falseMax)
	}
}

func constrainLower(env map[int]intRange, id int, min int64) {
	r := env[id]
	if !r.known {
		r = intRange{min: math.MinInt64, max: math.MaxInt64, known: true}
	}
	if min > r.min {
		r.min = min
	}
	env[id] = r
}

func constrainUpper(env map[int]intRange, id int, max int64) {
	r := env[id]
	if !r.known {
		r = intRange{min: math.MinInt64, max: math.MaxInt64, known: true}
	}
	if max < r.max {
		r.max = max
	}
	env[id] = r
}

func mergeBlockEntry(entries map[int]map[int]intRange, block *Block, incoming map[int]intRange) bool {
	if block == nil {
		return false
	}
	current, ok := entries[block.ID]
	if !ok {
		entries[block.ID] = cloneRangeMap(incoming)
		return len(incoming) > 0
	}
	changed := false
	for id, in := range incoming {
		if !in.known {
			continue
		}
		if old, ok := current[id]; ok && old.known {
			joined := joinRange(old, in)
			if !rangeEqual(old, joined) {
				current[id] = joined
				changed = true
			}
			continue
		}
		current[id] = in
		changed = true
	}
	return changed
}

func cloneRangeMap(in map[int]intRange) map[int]intRange {
	out := make(map[int]intRange, len(in))
	for id, r := range in {
		if r.known {
			out[id] = r
		}
	}
	return out
}

func rangeExcludesZero(r intRange) bool {
	return r.known && (r.max < 0 || r.min > 0)
}

func rangesHaveSameKnownModuloSign(lhs, rhs intRange) bool {
	if !lhs.known || !rhs.known {
		return false
	}
	return (lhs.min >= 0 && rhs.min > 0) || (lhs.max <= 0 && rhs.max < 0)
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

// --- Guarded forward induction ranges ---

// seedGuardedForwardInductionRanges recognizes while-style positive induction
// variables:
//
//	header:
//	  i = Phi(init, i + step)
//	  cond = f(i) <= bound
//	  Branch cond -> body, exit
//
// When init is non-negative, step is a positive constant, bound is an int48
// runtime value, and f(i) gives an upper bound for i on the true branch, every
// value carried back to the header is bounded by true-branch-max(i)+step. This
// covers trial-division loops like `i = 5; while i*i <= n { ...; i += 6 }`
// without naming the benchmark or the callee.
func seedGuardedForwardInductionRanges(fn *Function, ranges map[int]intRange) {
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return
	}

	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		cond := loopHeaderBranchCond(header)
		if cond == nil {
			continue
		}
		for _, phi := range header.Instrs {
			if phi.Op != OpPhi {
				break
			}
			if !phi.Type.isIntegerLike() {
				continue
			}
			ind, ok := analyzeForwardInduction(phi, li)
			if !ok || ind.init.min < 0 {
				continue
			}
			trueMax, ok := guardedUpperBound(cond, phi, ranges)
			if !ok {
				continue
			}
			backMax := satAdd(trueMax, ind.step)
			seeded := intRange{
				min:   ind.init.min,
				max:   max64(ind.init.max, backMax),
				known: true,
			}
			if !seeded.fitsInt48() {
				continue
			}
			ranges[phi.ID] = seeded
			ranges[ind.update.ID] = seeded
		}
	}
}

type forwardInduction struct {
	init   intRange
	step   int64
	update *Instr
}

func analyzeForwardInduction(phi *Instr, li *loopInfo) (forwardInduction, bool) {
	var out forwardInduction
	bodyBlocks := li.headerBlocks[phi.Block.ID]
	if bodyBlocks == nil {
		return out, false
	}

	for predIdx, arg := range phi.Args {
		if arg == nil || arg.Def == nil {
			continue
		}
		var fromLoop bool
		if predIdx < len(phi.Block.Preds) {
			fromLoop = bodyBlocks[phi.Block.Preds[predIdx].ID]
		} else if arg.Def.Block != nil {
			fromLoop = bodyBlocks[arg.Def.Block.ID]
		}
		if fromLoop {
			step, ok := forwardStepFromPhi(arg.Def, phi.ID)
			if !ok {
				continue
			}
			if out.update != nil || step <= 0 {
				return forwardInduction{}, false
			}
			out.step = step
			out.update = arg.Def
			continue
		}

		init := initialRangeFromValue(arg)
		if !init.known {
			return forwardInduction{}, false
		}
		if out.init.known {
			out.init = joinRange(out.init, init)
		} else {
			out.init = init
		}
	}

	if out.update == nil || !out.init.known {
		return forwardInduction{}, false
	}
	return out, true
}

func forwardStepFromPhi(instr *Instr, phiID int) (int64, bool) {
	if instr == nil {
		return 0, false
	}
	switch instr.Op {
	case OpAdd, OpAddInt:
		if len(instr.Args) < 2 {
			return 0, false
		}
		if instr.Args[0] != nil && instr.Args[0].ID == phiID {
			if c, ok := constIntFromValue(instr.Args[1]); ok {
				return c, true
			}
		}
		if instr.Args[1] != nil && instr.Args[1].ID == phiID {
			if c, ok := constIntFromValue(instr.Args[0]); ok {
				return c, true
			}
		}
	case OpSub, OpSubInt:
		if len(instr.Args) < 2 {
			return 0, false
		}
		if instr.Args[0] != nil && instr.Args[0].ID == phiID {
			if c, ok := constIntFromValue(instr.Args[1]); ok {
				return satNeg(c), true
			}
		}
	}
	return 0, false
}

func initialRangeFromValue(v *Value) intRange {
	if c, ok := constIntFromValue(v); ok {
		return pointRange(c)
	}
	return topRange()
}

func loopHeaderBranchCond(header *Block) *Instr {
	if header == nil || len(header.Instrs) == 0 {
		return nil
	}
	term := header.Instrs[len(header.Instrs)-1]
	if term.Op != OpBranch || len(term.Args) == 0 || term.Args[0] == nil {
		return nil
	}
	return term.Args[0].Def
}

func guardedUpperBound(cond *Instr, phi *Instr, ranges map[int]intRange) (int64, bool) {
	if cond == nil || len(cond.Args) < 2 {
		return 0, false
	}
	switch cond.Op {
	case OpLe, OpLeInt:
		return compareUpperBound(cond.Args[0], cond.Args[1], phi, ranges, false)
	case OpLt, OpLtInt:
		return compareUpperBound(cond.Args[0], cond.Args[1], phi, ranges, true)
	default:
		return 0, false
	}
}

func compareUpperBound(lhs, rhs *Value, phi *Instr, ranges map[int]intRange, strict bool) (int64, bool) {
	bound, ok := valueIntUpperBound(rhs, ranges)
	if !ok {
		return 0, false
	}
	if strict {
		bound = satSub(bound, 1)
	}
	return deriveUpperBoundFromExpr(lhs, phi.ID, bound)
}

func valueIntUpperBound(v *Value, ranges map[int]intRange) (int64, bool) {
	if v == nil || v.Def == nil {
		return 0, false
	}
	if r, ok := ranges[v.ID]; ok && r.known {
		return r.max, true
	}
	if c, ok := constIntFromValue(v); ok {
		return c, true
	}
	if isInt48RuntimeValue(v.Def) {
		return MaxInt48, true
	}
	return 0, false
}

func deriveUpperBoundFromExpr(v *Value, phiID int, bound int64) (int64, bool) {
	lin, ok := linearExprOfPhi(v, phiID)
	if ok && lin.scale > 0 {
		return floorDiv(satSub(bound, lin.offset), lin.scale), true
	}
	if square, ok := squareExprOfPhi(v, phiID); ok && square.scale > 0 {
		if bound < 0 {
			return 0, true
		}
		return floorDiv(satSub(isqrt64(bound), square.offset), square.scale), true
	}
	return 0, false
}

type phiLinearExpr struct {
	scale  int64
	offset int64
}

func linearExprOfPhi(v *Value, phiID int) (phiLinearExpr, bool) {
	if v == nil || v.Def == nil {
		return phiLinearExpr{}, false
	}
	if v.ID == phiID {
		return phiLinearExpr{scale: 1}, true
	}
	instr := v.Def
	switch instr.Op {
	case OpAdd, OpAddInt:
		if len(instr.Args) < 2 {
			return phiLinearExpr{}, false
		}
		if lin, ok := linearExprOfPhi(instr.Args[0], phiID); ok {
			if c, ok := constIntFromValue(instr.Args[1]); ok {
				lin.offset = satAdd(lin.offset, c)
				return lin, true
			}
		}
		if lin, ok := linearExprOfPhi(instr.Args[1], phiID); ok {
			if c, ok := constIntFromValue(instr.Args[0]); ok {
				lin.offset = satAdd(lin.offset, c)
				return lin, true
			}
		}
	case OpSub, OpSubInt:
		if len(instr.Args) < 2 {
			return phiLinearExpr{}, false
		}
		if lin, ok := linearExprOfPhi(instr.Args[0], phiID); ok {
			if c, ok := constIntFromValue(instr.Args[1]); ok {
				lin.offset = satSub(lin.offset, c)
				return lin, true
			}
		}
	}
	return phiLinearExpr{}, false
}

func squareExprOfPhi(v *Value, phiID int) (phiLinearExpr, bool) {
	if v == nil || v.Def == nil {
		return phiLinearExpr{}, false
	}
	instr := v.Def
	if instr.Op != OpMul && instr.Op != OpMulInt || len(instr.Args) < 2 {
		return phiLinearExpr{}, false
	}
	left, ok1 := linearExprOfPhi(instr.Args[0], phiID)
	right, ok2 := linearExprOfPhi(instr.Args[1], phiID)
	if !ok1 || !ok2 || left != right {
		return phiLinearExpr{}, false
	}
	return left, true
}

func isInt48RuntimeValue(instr *Instr) bool {
	if instr == nil || instr.Type != TypeInt {
		return false
	}
	switch instr.Op {
	case OpConstInt, OpGuardType, OpLoadSlot, OpUnboxInt:
		return true
	default:
		return false
	}
}

func isqrt64(v int64) int64 {
	if v <= 0 {
		return 0
	}
	x := int64(math.Sqrt(float64(v)))
	for x < math.MaxInt64 {
		next := x + 1
		if next > v/next {
			break
		}
		x++
	}
	for x > 0 && x > v/x {
		x--
	}
	return x
}

// markConvergingInductionSafe recognizes the common two-pointer loop:
//
//	header:
//	  lo = Phi(initLo, lo + 1)
//	  hi = Phi(initHi, hi - 1)
//	  lo < hi
//
// On the true branch, both operands are int48 values and the strict comparison
// proves lo <= MaxInt48-1 and hi >= MinInt48+1. Therefore lo+1 and hi-1 cannot
// leave the int48 payload range. This keeps swap/reverse loops in raw-int form
// without making a benchmark-specific assumption about arrays or table values.
func markConvergingInductionSafe(fn *Function, safe map[int]bool) {
	if fn == nil || safe == nil {
		return
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return
	}
	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		cond := loopHeaderBranchCond(header)
		if cond == nil || len(cond.Args) < 2 {
			continue
		}
		switch cond.Op {
		case OpLt, OpLtInt:
		default:
			continue
		}
		leftPhi := headerPhiValue(cond.Args[0], header)
		rightPhi := headerPhiValue(cond.Args[1], header)
		if leftPhi == nil || rightPhi == nil {
			continue
		}
		if !leftPhi.Type.isIntegerLike() || !rightPhi.Type.isIntegerLike() {
			continue
		}
		body := li.headerBlocks[header.ID]
		leftUpdate, ok := loopPhiBackedgeValue(leftPhi, body)
		if !ok || !isSelfAddConst(leftUpdate, leftPhi.ID, 1) {
			continue
		}
		rightUpdate, ok := loopPhiBackedgeValue(rightPhi, body)
		if !ok || !isSelfSubConst(rightUpdate, rightPhi.ID, 1) {
			continue
		}
		safe[leftUpdate.ID] = true
		safe[rightUpdate.ID] = true
	}
}

func headerPhiValue(v *Value, header *Block) *Instr {
	if v == nil || v.Def == nil || header == nil {
		return nil
	}
	if v.Def.Op != OpPhi || v.Def.Block != header {
		return nil
	}
	return v.Def
}

func loopPhiBackedgeValue(phi *Instr, body map[int]bool) (*Instr, bool) {
	if phi == nil || body == nil {
		return nil, false
	}
	var update *Instr
	for predIdx, arg := range phi.Args {
		if arg == nil || arg.Def == nil {
			continue
		}
		fromLoop := false
		if predIdx < len(phi.Block.Preds) {
			fromLoop = body[phi.Block.Preds[predIdx].ID]
		} else if arg.Def.Block != nil {
			fromLoop = body[arg.Def.Block.ID]
		}
		if !fromLoop {
			continue
		}
		if update != nil {
			return nil, false
		}
		update = arg.Def
	}
	return update, update != nil
}

func isSelfAddConst(instr *Instr, phiID int, c int64) bool {
	if instr == nil || instr.Op != OpAddInt || len(instr.Args) < 2 {
		return false
	}
	if instr.Args[0] != nil && instr.Args[0].ID == phiID {
		return valueIsConstInt(instr.Args[1], c)
	}
	if instr.Args[1] != nil && instr.Args[1].ID == phiID {
		return valueIsConstInt(instr.Args[0], c)
	}
	return false
}

func isSelfSubConst(instr *Instr, phiID int, c int64) bool {
	if instr == nil || instr.Op != OpSubInt || len(instr.Args) < 2 {
		return false
	}
	return instr.Args[0] != nil && instr.Args[0].ID == phiID && valueIsConstInt(instr.Args[1], c)
}

func valueIsConstInt(v *Value, want int64) bool {
	got, ok := constIntFromValue(v)
	return ok && got == want
}

func floorDiv(a, b int64) int64 {
	if b <= 0 {
		return 0
	}
	q := a / b
	r := a % b
	if r != 0 && ((r < 0) != (b < 0)) {
		q--
	}
	return q
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
