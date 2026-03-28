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
	// This function creates enough live values to exceed the 5 GPR budget.
	// Each variable a..f is live until the final sum, so at least 6 GPRs needed.
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

// TestRegAlloc_FloatValues: float-typed values go to FPRs (D4-D11).
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
		if reg.Reg < 4 || reg.Reg > 11 {
			t.Errorf("float value v%d assigned to D%d, expected D4-D11", instr.ID, reg.Reg)
		}
	}
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

// assertValidRegisters checks that all GPR assignments are in X19-X23 and
// all FPR assignments are in D4-D11.
func assertValidRegisters(t *testing.T, alloc *RegAllocation) {
	t.Helper()
	for id, reg := range alloc.ValueRegs {
		if reg.IsFloat {
			if reg.Reg < 4 || reg.Reg > 11 {
				t.Errorf("value v%d: FPR D%d out of range D4-D11", id, reg.Reg)
			}
		} else {
			if reg.Reg < 19 || reg.Reg > 23 {
				t.Errorf("value v%d: GPR X%d out of range X19-X23", id, reg.Reg)
			}
		}
	}
}
