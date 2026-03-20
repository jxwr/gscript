//go:build darwin && arm64

package jit

// FuseMultiplyAdd detects MUL+ADD and MUL+SUB patterns in the SSA IR
// and replaces them with fused multiply-add/subtract instructions.
//
// Pattern 1 (FMADD): c + a*b → SSA_FMADD(a, b, c)
//
//	ref1 = SSA_MUL_FLOAT(a, b)
//	ref2 = SSA_ADD_FLOAT(ref1, c)  or  SSA_ADD_FLOAT(c, ref1)
//	→ ref2 = SSA_FMADD(a, b, c)   // c + a*b
//
// Pattern 2 (FMSUB): c - a*b → SSA_FMSUB(a, b, c)
//
//	ref1 = SSA_MUL_FLOAT(a, b)
//	ref2 = SSA_SUB_FLOAT(c, ref1)   // c - ref1
//	→ ref2 = SSA_FMSUB(a, b, c)     // c - a*b
//
// Constraints:
//   - The MUL result must have exactly one use (the ADD/SUB).
//     If used elsewhere, we can't eliminate it.
//   - Only applies within the loop body (after SSA_LOOP).
//
// ARM64 semantics:
//
//	FMADD Dd, Dn, Dm, Da = Da + Dn * Dm
//	FMSUB Dd, Dn, Dm, Da = Da - Dn * Dm
//
// SSA encoding:
//
//	SSA_FMADD: Arg1=Dn (mul op1), Arg2=Dm (mul op2), AuxInt=Da ref (addend)
//	SSA_FMSUB: Arg1=Dn (mul op1), Arg2=Dm (mul op2), AuxInt=Da ref (addend)
func FuseMultiplyAdd(f *SSAFunc) *SSAFunc {
	if f == nil || len(f.Insts) == 0 {
		return f
	}

	// Find LOOP marker
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

	// Initialize AbsorbedMuls map
	if f.AbsorbedMuls == nil {
		f.AbsorbedMuls = make(map[SSARef]bool)
	}

	// Build use counts for refs in the loop body.
	// We only fuse if the MUL has exactly 1 use.
	useCount := make(map[SSARef]int)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		if inst.Arg1 >= 0 {
			useCount[inst.Arg1]++
		}
		if inst.Arg2 >= 0 {
			useCount[inst.Arg2]++
		}
	}

	// Scan for fuseable patterns
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		switch inst.Op {
		case SSA_ADD_FLOAT:
			// Check if Arg1 is a single-use MUL_FLOAT
			if tryFuseAdd(f, i, inst.Arg1, inst.Arg2, useCount) {
				continue
			}
			// Check if Arg2 is a single-use MUL_FLOAT (ADD is commutative)
			tryFuseAdd(f, i, inst.Arg2, inst.Arg1, useCount)

		case SSA_SUB_FLOAT:
			// c - a*b: Arg1=c, Arg2=ref1 (MUL result)
			// Only fuse when Arg2 is the MUL (c - mul_result)
			tryFuseSub(f, i, useCount)
		}
	}

	return f
}

// tryFuseAdd checks if mulRef is a single-use MUL_FLOAT and fuses it
// with the ADD_FLOAT at addIdx. addendRef is the other operand.
// Returns true if fusion was performed.
func tryFuseAdd(f *SSAFunc, addIdx int, mulRef, addendRef SSARef, useCount map[SSARef]int) bool {
	if mulRef < 0 || int(mulRef) >= len(f.Insts) {
		return false
	}
	mulInst := &f.Insts[mulRef]
	if mulInst.Op != SSA_MUL_FLOAT {
		return false
	}
	if useCount[mulRef] != 1 {
		return false
	}

	// Fuse: ADD(MUL(a,b), c) → FMADD(a, b, c)
	addInst := &f.Insts[addIdx]
	addInst.Op = SSA_FMADD
	addInst.Arg1 = mulInst.Arg1 // Dn (mul operand 1)
	addInst.Arg2 = mulInst.Arg2 // Dm (mul operand 2)
	addInst.AuxInt = int64(addendRef) // Da (addend, as SSA ref)

	// Mark the MUL as absorbed — it stays as MUL_FLOAT (preserving regalloc
	// live ranges and slot mapping) but codegen will skip emitting it.
	f.AbsorbedMuls[mulRef] = true

	return true
}

// tryFuseSub checks if Arg2 of the SUB_FLOAT at subIdx is a single-use
// MUL_FLOAT. If so, fuses into FMSUB.
func tryFuseSub(f *SSAFunc, subIdx int, useCount map[SSARef]int) bool {
	subInst := &f.Insts[subIdx]
	mulRef := subInst.Arg2
	if mulRef < 0 || int(mulRef) >= len(f.Insts) {
		return false
	}
	mulInst := &f.Insts[mulRef]
	if mulInst.Op != SSA_MUL_FLOAT {
		return false
	}
	if useCount[mulRef] != 1 {
		return false
	}

	// Fuse: SUB(c, MUL(a,b)) → FMSUB(a, b, c)
	addendRef := subInst.Arg1 // c (the value we're subtracting from)
	subInst.Op = SSA_FMSUB
	subInst.Arg1 = mulInst.Arg1 // Dn (mul operand 1)
	subInst.Arg2 = mulInst.Arg2 // Dm (mul operand 2)
	subInst.AuxInt = int64(addendRef) // Da (minuend, as SSA ref)

	// Mark the MUL as absorbed — it stays as MUL_FLOAT (preserving regalloc
	// live ranges and slot mapping) but codegen will skip emitting it.
	f.AbsorbedMuls[mulRef] = true

	return true
}
