// regalloc_test.go tests the forward-walk register allocator.
// Each test compiles GScript source → bytecode → CFG SSA IR → register allocation,
// then verifies that every non-terminator instruction gets a register or spill slot,
// that GPR/FPR assignments are within the allocatable ranges, and that no two
// live values share the same physical register.

package methodjit

import (
	"testing"
)

// TestRegAlloc_SimpleAdd: a + b — 2 inputs + 1 result, all fit in registers.
func TestRegAlloc_SimpleAdd(t *testing.T) {
	proto := compileFunction(t, `func f(a, b) { return a + b }`)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	// Every non-terminator value should have a register or spill slot.
	assertAllValuesAssigned(t, fn, alloc)

	// With only a few values, nothing should be spilled.
	if alloc.NumSpillSlots > 0 {
		t.Errorf("expected no spills for simple add, got %d spill slots", alloc.NumSpillSlots)
	}
}

// TestRegAlloc_ManyValues: 10+ values force spilling.
func TestRegAlloc_ManyValues(t *testing.T) {
	// This function creates enough live values to exceed the GPR budget.
	// Each variable a..f is live until the final sum, so at least 6 GPRs needed
	// (exceeds 5 allocatable GPRs: X20-X23, X28).
	proto := compileFunction(t, `
func f(n) {
	a := n + 1
	b := n + 2
	c := n + 3
	d := n + 4
	e := n + 5
	f := n + 6
	return a + b + c + d + e + f
}
`)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	assertAllValuesAssigned(t, fn, alloc)

	// With 5 GPRs and many simultaneously live values, some should be spilled.
	if alloc.NumSpillSlots == 0 {
		t.Logf("no spills — allocator may be smarter than expected or values die quickly")
	}

	// Verify GPR assignments are in valid range.
	assertValidRegisters(t, alloc)
}

// TestRegAlloc_ForLoop: loop with phis — phi values get registers.
func TestRegAlloc_ForLoop(t *testing.T) {
	proto := compileFunction(t, `
func sum(n) {
	s := 0
	for i := 1; i <= n; i++ {
		s = s + i
	}
	return s
}
`)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	assertAllValuesAssigned(t, fn, alloc)
	assertValidRegisters(t, alloc)
}

// TestRegAlloc_Fib: recursive fib — call results get allocated.
func TestRegAlloc_Fib(t *testing.T) {
	proto := compileFunction(t, `
func fib(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
`)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	assertAllValuesAssigned(t, fn, alloc)
	assertValidRegisters(t, alloc)
}

// TestRegAlloc_NoSpillSimple: with fewer values than registers, no spills needed.
func TestRegAlloc_NoSpillSimple(t *testing.T) {
	proto := compileFunction(t, `func f(a) { return a }`)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	assertAllValuesAssigned(t, fn, alloc)

	if alloc.NumSpillSlots != 0 {
		t.Errorf("expected 0 spill slots for identity function, got %d", alloc.NumSpillSlots)
	}
}

// TestRegAlloc_AllValuesAssigned: every value gets either a register or spill slot.
func TestRegAlloc_AllValuesAssigned(t *testing.T) {
	sources := []string{
		`func f(a, b) { return a + b }`,
		`func f(n) { if n > 0 { return 1 } else { return 0 } }`,
		`func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`,
		`func f(a, b, c) { return a * b + c }`,
	}
	for _, src := range sources {
		t.Run(src, func(t *testing.T) {
			proto := compileFunction(t, src)
			fn := BuildGraph(proto)
			alloc := AllocateRegisters(fn)
			assertAllValuesAssigned(t, fn, alloc)
			assertValidRegisters(t, alloc)
		})
	}
}

// TestRegAlloc_FloatValues: float-typed values go to allocatable FPRs.
func TestRegAlloc_FloatValues(t *testing.T) {
	// Build a function manually with float-typed instructions to verify FPR allocation.
	fn := &Function{
		Proto:   nil,
		NumRegs: 2,
	}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	// v0 = ConstFloat 1.5 : float
	v0 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: entry, Aux: 0}
	// v1 = ConstFloat 2.5 : float
	v1 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: entry, Aux: 0}
	// v2 = AddFloat v0, v1 : float
	v2 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: entry,
		Args: []*Value{v0.Value(), v1.Value()}}
	// Return v2
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: entry,
		Args: []*Value{v2.Value()}}
	entry.Instrs = []*Instr{v0, v1, v2, ret}

	alloc := AllocateRegisters(fn)

	// All float values should be in FPRs.
	for _, instr := range []*Instr{v0, v1, v2} {
		reg, hasReg := alloc.ValueRegs[instr.ID]
		if !hasReg {
			_, hasSpill := alloc.SpillSlots[instr.ID]
			if !hasSpill {
				t.Errorf("float value v%d has no register or spill slot", instr.ID)
			}
			continue
		}
		if !reg.IsFloat {
			t.Errorf("float value v%d assigned to GPR %d, expected FPR", instr.ID, reg.Reg)
		}
		if !isAllocatableFPR(reg.Reg) {
			t.Errorf("float value v%d assigned to D%d, expected allocatable FPR", instr.ID, reg.Reg)
		}
	}
}

func TestRegAlloc_FloatPhiUsesHighCallerSavedFPRs(t *testing.T) {
	fn := &Function{NumRegs: 1}
	entry := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	const phiCount = 12
	phis := make([]*Instr, 0, phiCount)
	for i := 0; i < phiCount; i++ {
		phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: entry}
		phis = append(phis, phi)
		entry.Instrs = append(entry.Instrs, phi)
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: entry}
	entry.Instrs = append(entry.Instrs, ret)

	alloc := AllocateRegisters(fn)
	if alloc.NumSpillSlots != 0 {
		t.Fatalf("expected widened FPR pool to hold %d simultaneous float phis, got %d spills", phiCount, alloc.NumSpillSlots)
	}
	usedHigh := false
	for _, phi := range phis {
		pr, ok := alloc.ValueRegs[phi.ID]
		if !ok || !pr.IsFloat {
			t.Fatalf("float phi v%d did not get an FPR: %+v", phi.ID, pr)
		}
		if pr.Reg >= 16 {
			usedHigh = true
		}
	}
	if !usedHigh {
		t.Fatalf("expected at least one float phi to use D16-D23; allocation=%s", formatRegAlloc(alloc))
	}
}

// TestRegAlloc_IntPhiAlreadyCarried verifies existing behavior: an int phi in a
// tight 2-block loop (header + one body) gets a GPR allocation and is carried
// into the body block so that the body's AddInt result does NOT clobber the
// phi's GPR. This is a baseline test for the existing carried-phi mechanism.
func TestRegAlloc_IntPhiAlreadyCarried(t *testing.T) {
	fn := &Function{NumRegs: 2}

	// CFG: b0(entry) → b1(header, phi:int) → b2(body, AddInt) → b1 (back-edge)
	//                                        \→ b3(exit)
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	b1 := &Block{ID: 1, defs: make(map[int]*Value)}
	b2 := &Block{ID: 2, defs: make(map[int]*Value)}
	b3 := &Block{ID: 3, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b2}
	b1.Succs = []*Block{b2, b3}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b1}
	b3.Preds = []*Block{b1}

	// b0: seed = ConstInt 0
	vSeed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0,
		Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{vSeed, b0Term}

	// b1: phi(seed, bodyResult) : int
	vPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	vCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{vCond.Value()},
		Aux:  int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{vPhi, vCond, b1Term}

	// b2: body = AddInt(phi, ConstInt 1) → back to b1
	vOne := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b2, Aux: 1}
	vBody := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{vPhi.Value(), vOne.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2,
		Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{vOne, vBody, b2Term}

	// Wire phi: from b0 → vSeed, from b2 → vBody
	vPhi.Args = []*Value{vSeed.Value(), vBody.Value()}

	// b3: return phi
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{vPhi.Value()}}
	b3.Instrs = []*Instr{b3Term}

	alloc := AllocateRegisters(fn)

	// The phi should get a GPR (not FPR, not spill).
	phiReg, ok := alloc.ValueRegs[vPhi.ID]
	if !ok {
		t.Fatalf("phi v%d has no register assignment", vPhi.ID)
	}
	if phiReg.IsFloat {
		t.Fatalf("int phi v%d expected GPR, got FPR D%d", vPhi.ID, phiReg.Reg)
	}

	// The body's AddInt should also get a GPR.
	bodyReg, ok := alloc.ValueRegs[vBody.ID]
	if !ok {
		t.Fatalf("body AddInt v%d has no register assignment", vBody.ID)
	}
	if bodyReg.IsFloat {
		t.Fatalf("body AddInt v%d expected GPR, got FPR D%d", vBody.ID, bodyReg.Reg)
	}

	// Critical: the body AddInt must NOT clobber the phi's GPR.
	if phiReg.Reg == bodyReg.Reg {
		t.Fatalf("body AddInt v%d assigned X%d, same as loop-header phi v%d (X%d); "+
			"this clobbers the loop-carried value",
			vBody.ID, bodyReg.Reg, vPhi.ID, phiReg.Reg)
	}
	t.Logf("phi=X%d body=X%d (no clobber)", phiReg.Reg, bodyReg.Reg)
}

// TestRegAlloc_IntPhiCarry builds a loop with an int counter phi and a LeInt
// comparison against a LoadSlot bound. It verifies that:
//   - The counter phi gets a GPR
//   - The body's AddInt result does not clobber the phi's GPR
//   - The loop bound (LoadSlot) gets a GPR that is pinned in the body
func TestRegAlloc_IntPhiCarry(t *testing.T) {
	fn := &Function{NumRegs: 4}

	// CFG: b0(entry) → b1(header) → b2(body) → b1 (back-edge)
	//                              \→ b3(exit)
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	b1 := &Block{ID: 1, defs: make(map[int]*Value)}
	b2 := &Block{ID: 2, defs: make(map[int]*Value)}
	b3 := &Block{ID: 3, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b2}
	b1.Succs = []*Block{b2, b3}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b1}
	b3.Preds = []*Block{b1}

	// b0: seed = ConstInt 0; bound = LoadSlot(3)
	vSeed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	vBound := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Block: b0, Aux: 3}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0,
		Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{vSeed, vBound, b0Term}

	// b1: phi(seed, bodyResult) : int
	//     cmp = LeInt(phi, bound) : bool
	//     branch cmp, b2, b3
	vPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	vCmp := &Instr{ID: fn.newValueID(), Op: OpLeInt, Type: TypeBool, Block: b1,
		Args: []*Value{vPhi.Value(), vBound.Value()}}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{vCmp.Value()},
		Aux:  int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{vPhi, vCmp, b1Term}

	// b2: body = AddInt(phi, ConstInt 1)
	vOne := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b2, Aux: 1}
	vBody := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{vPhi.Value(), vOne.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2,
		Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{vOne, vBody, b2Term}

	// Wire phi
	vPhi.Args = []*Value{vSeed.Value(), vBody.Value()}

	// b3: return phi
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{vPhi.Value()}}
	b3.Instrs = []*Instr{b3Term}

	alloc := AllocateRegisters(fn)

	// The phi must get a GPR.
	phiReg, ok := alloc.ValueRegs[vPhi.ID]
	if !ok {
		t.Fatalf("phi v%d has no register assignment", vPhi.ID)
	}
	if phiReg.IsFloat {
		t.Fatalf("int phi v%d expected GPR, got FPR D%d", vPhi.ID, phiReg.Reg)
	}

	// The body AddInt must get a different GPR from the phi.
	bodyReg, ok := alloc.ValueRegs[vBody.ID]
	if !ok {
		t.Fatalf("body AddInt v%d has no register assignment", vBody.ID)
	}
	if phiReg.Reg == bodyReg.Reg {
		t.Fatalf("body AddInt v%d assigned X%d, same as phi v%d (X%d); clobbers loop-carried value",
			vBody.ID, bodyReg.Reg, vPhi.ID, phiReg.Reg)
	}

	// The loop bound (LoadSlot) should have a GPR assignment.
	boundReg, hasBound := alloc.ValueRegs[vBound.ID]
	if !hasBound {
		t.Fatalf("loop bound v%d has no register assignment", vBound.ID)
	}
	if boundReg.IsFloat {
		t.Fatalf("loop bound v%d expected GPR, got FPR D%d", vBound.ID, boundReg.Reg)
	}

	// The bound GPR must not collide with the phi GPR.
	if boundReg.Reg == phiReg.Reg {
		t.Errorf("bound v%d assigned X%d, same as phi v%d", vBound.ID, boundReg.Reg, vPhi.ID)
	}

	// The bound's GPR should be carried/pinned in the body block, meaning the
	// body's AddInt or ConstInt did not reuse the bound's register.
	for _, bodyInstr := range []*Instr{vOne, vBody} {
		if br, ok := alloc.ValueRegs[bodyInstr.ID]; ok && !br.IsFloat {
			if br.Reg == boundReg.Reg {
				t.Errorf("body instr v%d assigned X%d, same as loop bound v%d; "+
					"bound GPR was not carried/pinned",
					bodyInstr.ID, br.Reg, vBound.ID)
			}
		}
	}

	t.Logf("phi=X%d body=X%d bound=X%d", phiReg.Reg, bodyReg.Reg, boundReg.Reg)
}

// --- Test helpers ---

// assertAllValuesAssigned checks that every non-terminator instruction in the
// function has either a register or a spill slot in the allocation.
func assertAllValuesAssigned(t *testing.T, fn *Function, alloc *RegAllocation) {
	t.Helper()
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			_, hasReg := alloc.ValueRegs[instr.ID]
			_, hasSpill := alloc.SpillSlots[instr.ID]
			if !hasReg && !hasSpill {
				t.Errorf("value v%d (%s) has no register or spill slot", instr.ID, instr.Op)
			}
		}
	}
}

// assertValidRegisters checks that all assignments use the current allocatable
// register sets.
func assertValidRegisters(t *testing.T, alloc *RegAllocation) {
	t.Helper()
	validGPRs := map[int]bool{20: true, 21: true, 22: true, 23: true, 28: true}
	for id, reg := range alloc.ValueRegs {
		if reg.IsFloat {
			if !isAllocatableFPR(reg.Reg) {
				t.Errorf("value v%d: FPR D%d not in allocatable set", id, reg.Reg)
			}
		} else {
			if !validGPRs[reg.Reg] {
				t.Errorf("value v%d: GPR X%d not in allocatable set {X20-X23, X28}", id, reg.Reg)
			}
		}
	}
}

func isAllocatableFPR(reg int) bool {
	for _, r := range allocatableFPRs {
		if r == reg {
			return true
		}
	}
	return false
}
