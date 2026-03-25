//go:build darwin && arm64

package jit

// FuseMultiplyAdd fuses MUL_FLOAT+ADD_FLOAT into FMADD and
// MUL_FLOAT+SUB_FLOAT into FMSUB when the MUL result has exactly one user.
//
// Patterns:
//   ADD_FLOAT(MUL_FLOAT(a,b), c) → FMADD(a, b, c)   = a*b + c
//   ADD_FLOAT(c, MUL_FLOAT(a,b)) → FMADD(a, b, c)   = a*b + c  (commutative)
//   SUB_FLOAT(c, MUL_FLOAT(a,b)) → FMSUB(a, b, c)   = c - a*b
//
// Note: SUB_FLOAT(MUL_FLOAT(a,b), c) = a*b - c is NOT fused because
// ARM64 FMSUB computes Ra - Rn*Rm, not Rn*Rm - Ra.
//
// The absorbed MUL instruction is left in the IR (preserving live ranges
// for register allocation) but marked in AbsorbedMuls so codegen skips it.
func FuseMultiplyAdd(f *SSAFunc) *SSAFunc {
	if f == nil {
		return nil
	}

	// Find LOOP marker.
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return f
	}

	// Count uses per ref in the loop body.
	uses := map[SSARef]int{}
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Arg1 >= 0 && inst.Arg1 != SSARefNone {
			uses[inst.Arg1]++
		}
		if inst.Arg2 >= 0 && inst.Arg2 != SSARefNone {
			uses[inst.Arg2]++
		}
	}

	f.AbsorbedMuls = map[SSARef]bool{}

	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		switch inst.Op {
		case SSA_ADD_FLOAT:
			// Check Arg1 = MUL_FLOAT with single use → FMADD(mul.a, mul.b, Arg2)
			if mulRef, ok := singleUseMul(f, inst.Arg1, uses); ok {
				mulInst := &f.Insts[mulRef]
				addend := inst.Arg2
				inst.Op = SSA_FMADD
				inst.Arg1 = mulInst.Arg1
				inst.Arg2 = mulInst.Arg2
				inst.AuxInt = int64(addend)
				f.AbsorbedMuls[mulRef] = true
				continue
			}
			// Check Arg2 = MUL_FLOAT with single use (commutative) → FMADD(mul.a, mul.b, Arg1)
			if mulRef, ok := singleUseMul(f, inst.Arg2, uses); ok {
				mulInst := &f.Insts[mulRef]
				addend := inst.Arg1
				inst.Op = SSA_FMADD
				inst.Arg1 = mulInst.Arg1
				inst.Arg2 = mulInst.Arg2
				inst.AuxInt = int64(addend)
				f.AbsorbedMuls[mulRef] = true
				continue
			}

		case SSA_SUB_FLOAT:
			// Only fuse SUB(c, MUL(a,b)) → FMSUB(a, b, c) = c - a*b
			// ARM64 FMSUB: Rd = Ra - Rn*Rm, so c is the accumulator (AuxInt).
			// Do NOT fuse SUB(MUL(a,b), c) because that's a*b - c, which
			// doesn't match the ARM64 FMSUB instruction semantics.
			if mulRef, ok := singleUseMul(f, inst.Arg2, uses); ok {
				mulInst := &f.Insts[mulRef]
				minuend := inst.Arg1 // c in (c - a*b)
				inst.Op = SSA_FMSUB
				inst.Arg1 = mulInst.Arg1
				inst.Arg2 = mulInst.Arg2
				inst.AuxInt = int64(minuend)
				f.AbsorbedMuls[mulRef] = true
				continue
			}
		}
	}

	return f
}

// singleUseMul checks whether ref points to a MUL_FLOAT instruction with
// exactly one use in the loop body. Returns the MUL's SSARef and true if fusible.
func singleUseMul(f *SSAFunc, ref SSARef, uses map[SSARef]int) (SSARef, bool) {
	if ref < 0 || ref == SSARefNone || int(ref) >= len(f.Insts) {
		return -1, false
	}
	inst := &f.Insts[ref]
	if inst.Op != SSA_MUL_FLOAT {
		return -1, false
	}
	if uses[ref] != 1 {
		return -1, false
	}
	return ref, true
}
