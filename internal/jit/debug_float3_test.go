//go:build darwin && arm64

package jit

import (
	"fmt"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestMicro_DebugFloat3(t *testing.T) {
	// Try WITHOUT UNBOX_FLOAT — just LOAD_SLOT + GUARD_TYPE for float slots
	f := buildMinimalTrace([]SSAInst{
		// Int slots 0-3 (standard for-loop)
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 0},
		{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: int64(TypeInt), Slot: 0},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Slot: 1},
		{Op: SSA_GUARD_TYPE, Arg1: 3, AuxInt: int64(TypeInt), Slot: 1},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
		{Op: SSA_GUARD_TYPE, Arg1: 6, AuxInt: int64(TypeInt), Slot: 2},
		{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
		{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
		{Op: SSA_GUARD_TYPE, Arg1: 9, AuxInt: int64(TypeInt), Slot: 3},
		// Float slot 4 (x) - load + guard but NO unbox
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 4},
		{Op: SSA_GUARD_TYPE, Arg1: 12, AuxInt: int64(TypeFloat), Slot: 4},
		// Float slot 5 (result) - load + guard but NO unbox
		{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},
		{Op: SSA_GUARD_TYPE, Arg1: 14, AuxInt: int64(TypeFloat), Slot: 5},
		// Loop
		{Op: SSA_LOOP}, // 16
		// result = result * x  (using LOAD_SLOT refs)
		// But we can't reference LOAD_SLOT refs for floats without UNBOX_FLOAT...
		// The resolveFloatRef won't find a register for refs 12 or 14.
		// It would fall back to loading from memory.
		// Actually we need refs that the float system can resolve.
		// Without UNBOX_FLOAT, the float register won't be loaded in pre-loop.
		// Wait, emitPreLoopLoads loads ANY allocated float slot even without SSA instructions.
		// So if slot 4 and 5 get slot-level float allocation, they'll be loaded.
		// But the MUL_FLOAT needs to reference something...
		// Let me use the LOAD_SLOT refs (12 and 14) - resolveFloatRef would check
		// FloatRefReg first (not allocated), then FloatReg for the slot (should be allocated).
		{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 14, Arg2: 12, Slot: 5}, // 17
		// FORLOOP
		{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},
		{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 4, AuxInt: -1},
		{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 18, Slot: 3},
	})

	regMap := AllocateRegisters(f)
	fmt.Println("--- Register allocation (no UNBOX_FLOAT) ---")
	if regMap.Float != nil {
		for slot, freg := range regMap.Float.slotToReg {
			fmt.Printf("  Float slot %d -> D%d\n", slot, freg)
		}
	}
	if regMap.FloatRef != nil {
		for ref, freg := range regMap.FloatRef.refToReg {
			fmt.Printf("  Float ref %d -> D%d (Op=%d slot=%d)\n", ref, freg, f.Insts[ref].Op, f.Insts[ref].Slot)
		}
	}

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)
	regs[1] = runtime.IntValue(3) // limit (4 iterations)
	regs[2] = runtime.IntValue(1)
	regs[3] = runtime.IntValue(0)
	regs[4] = runtime.FloatValue(2.0)
	regs[5] = runtime.FloatValue(1.0)

	_, _, guardFail := executeMicro(t, f, regs)
	if guardFail {
		t.Fatal("unexpected guard fail")
	}

	result := regs[5].Float()
	fmt.Printf("  result = %f\n", result)
}
