//go:build darwin && arm64

package jit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ────────────────────────────────────────────────────────────────────────────
// Legacy method JIT stubs (for old test files that reference removed code)
// ────────────────────────────────────────────────────────────────────────────

// JITContext is the legacy method JIT context (replaced by TraceContext).
type JITContext struct {
	Regs      uintptr
	Constants uintptr
	ExitCode  int64
	ExitPC    int64
	RetBase   int64
	RetCount  int64
}

// compiledFunc represents a compiled function (legacy).
type compiledFunc struct {
	Code *CodeBlock
}

// Codegen is the legacy method JIT code generator.
type Codegen struct {
	asm     *Assembler
	proto   *vm.FuncProto
	globals map[string]runtime.Value
}

// compile is a stub that returns an error.
func (cg *Codegen) compile() (*compiledFunc, error) {
	return nil, fmt.Errorf("legacy method JIT codegen removed; use trace JIT instead")
}

// ────────────────────────────────────────────────────────────────────────────
// Legacy liveness analysis stubs (replaced by snapshot-based deoptimization)
// ────────────────────────────────────────────────────────────────────────────

// Liveness holds the result of liveness analysis on an SSA function.
type Liveness struct {
	WrittenSlots map[int]bool
	SlotTypes    map[int]SSAType
}

// NeedsStoreBack returns true if the given slot was written in the loop body.
func (l *Liveness) NeedsStoreBack(slot int) bool {
	return l.WrittenSlots[slot]
}

// AnalyzeLiveness performs liveness analysis on an SSA function.
// This is the legacy store-back analysis: it finds which slots are written
// after the LOOP marker and need to be stored back to VM memory on exit.
func AnalyzeLiveness(f *SSAFunc) *Liveness {
	li := &Liveness{
		WrittenSlots: make(map[int]bool),
		SlotTypes:    make(map[int]SSAType),
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
		return li
	}

	// Scan instructions after LOOP for written slots
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		// Stop at SIDE_EXIT (unreachable after)
		if inst.Op == SSA_SIDE_EXIT {
			break
		}

		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}

		// Skip guard/comparison/store ops (they don't produce values for VM slots)
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA:
			continue
		case SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT, SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
			continue
		case SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY:
			continue
		case SSA_NOP, SSA_SNAPSHOT, SSA_LOOP:
			continue
		case SSA_LOAD_SLOT:
			continue // reads, not writes
		}

		li.WrittenSlots[slot] = true
		li.SlotTypes[slot] = inst.Type
	}

	return li
}

// ────────────────────────────────────────────────────────────────────────────
// Legacy use-def analysis stubs
// ────────────────────────────────────────────────────────────────────────────

// SSA_CALL_SELF is a legacy opcode for self-recursive calls (now just SSA_CALL).
const SSA_CALL_SELF = SSA_CALL

// UseDef holds use-def chains for an SSA function.
type UseDef struct {
	Users    map[SSARef][]SSARef   // ref → list of refs that use it
	SlotDefs map[int][]SSARef      // slot → list of refs that define it
}

// UserCount returns the number of instructions that use the given ref.
func (ud *UseDef) UserCount(ref SSARef) int {
	return len(ud.Users[ref])
}

// HasUsers returns true if any instruction uses the given ref.
func (ud *UseDef) HasUsers(ref SSARef) bool {
	return len(ud.Users[ref]) > 0
}

// IsDeadCode returns true if the given instruction has no users and is not side-effecting.
func (ud *UseDef) IsDeadCode(ref SSARef, f *SSAFunc) bool {
	if ud.HasUsers(ref) {
		return false
	}
	if int(ref) >= len(f.Insts) {
		return true
	}
	inst := &f.Insts[ref]
	switch inst.Op {
	case SSA_GUARD_TYPE, SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA,
		SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY,
		SSA_SIDE_EXIT, SSA_CALL, SSA_CALL_INNER_TRACE,
		SSA_LOOP, SSA_SNAPSHOT:
		return false // side-effecting
	}
	return true
}

// BuildUseDef builds use-def chains for an SSA function.
func BuildUseDef(f *SSAFunc) *UseDef {
	ud := &UseDef{
		Users:    make(map[SSARef][]SSARef),
		SlotDefs: make(map[int][]SSARef),
	}

	for i := range f.Insts {
		inst := &f.Insts[i]
		ref := SSARef(i)

		// Track slot definitions
		if inst.Slot >= 0 {
			slot := int(inst.Slot)
			ud.SlotDefs[slot] = append(ud.SlotDefs[slot], ref)
		}

		// Track uses via Arg1
		if opUsesArg1(inst.Op) && inst.Arg1 >= 0 && inst.Arg1 != SSARefNone {
			ud.Users[inst.Arg1] = append(ud.Users[inst.Arg1], ref)
		}

		// Track uses via Arg2
		if opUsesArg2(inst.Op) && inst.Arg2 >= 0 && inst.Arg2 != SSARefNone {
			ud.Users[inst.Arg2] = append(ud.Users[inst.Arg2], ref)
		}

		// Special: STORE_ARRAY uses its AuxInt as a value ref
		if inst.Op == SSA_STORE_ARRAY && inst.AuxInt >= 0 {
			valRef := SSARef(inst.AuxInt)
			if valRef != SSARefNone && int(valRef) < len(f.Insts) {
				ud.Users[valRef] = append(ud.Users[valRef], ref)
			}
		}

		// Special: FMADD/FMSUB use AuxInt as a value ref (the addend/minuend)
		if (inst.Op == SSA_FMADD || inst.Op == SSA_FMSUB) && inst.AuxInt >= 0 {
			valRef := SSARef(inst.AuxInt)
			if valRef != SSARefNone && int(valRef) < len(f.Insts) {
				ud.Users[valRef] = append(ud.Users[valRef], ref)
			}
		}
	}

	return ud
}

// opUsesArg1 returns true if the SSA op reads Arg1 as an SSA ref.
func opUsesArg1(op SSAOp) bool {
	switch op {
	case SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
		SSA_LOAD_SLOT, SSA_LOOP, SSA_NOP, SSA_SNAPSHOT:
		return false
	}
	return true
}

// opUsesArg2 returns true if the SSA op reads Arg2 as an SSA ref.
func opUsesArg2(op SSAOp) bool {
	switch op {
	case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_DIV_INT,
		SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
		SSA_FMADD, SSA_FMSUB,
		SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
		SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
		SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_STORE_FIELD:
		return true
	}
	return false
}

// ssaIsCompilable returns true if the SSA function can be compiled.
// This combines ssaIsIntegerOnly and SSAIsUseful checks.
func ssaIsCompilable(f *SSAFunc) bool {
	return ssaIsIntegerOnly(f) && SSAIsUseful(f)
}
