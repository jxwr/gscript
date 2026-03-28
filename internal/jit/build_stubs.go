//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// ────────────────────────────────────────────────────────────────────────────
// Build stubs: definitions needed by committed code that reference features
// still in development. These stubs provide enough to compile and run; they
// will be replaced by proper implementations as features land.
// ────────────────────────────────────────────────────────────────────────────

// SSA ops for shape-based field access (not yet implemented).
const (
	SSA_LOAD_TABLE_SHAPE SSAOp = 200 // placeholder: load table shape
	SSA_CHECK_SHAPE_ID   SSAOp = 201 // placeholder: guard shape ID
)

// TableOffShape is the offset of the *Shape pointer in the Table struct.
// It follows the shapeID (uint32 at offset 140, padded to 8-byte alignment).
const TableOffShape = 144 // *Shape (pointer, 8 bytes) — TODO: verify with unsafe.Offsetof

// Intrinsic IDs for math functions not yet in the main list.
const (
	IntrinsicAbs   = 20
	IntrinsicFloor = 21
	IntrinsicCeil  = 22
	IntrinsicMax   = 23
	IntrinsicMin   = 24
)

// ARM64 float instructions: stubs for assembler methods not yet implemented.
func (a *Assembler) FABSd(dst, src FReg)    { a.emit(0x1e60c000 | uint32(src)<<5 | uint32(dst)) }
func (a *Assembler) FRINTMd(dst, src FReg)  { a.emit(0x1e654000 | uint32(src)<<5 | uint32(dst)) }
func (a *Assembler) FRINTPd(dst, src FReg)  { a.emit(0x1e64c000 | uint32(src)<<5 | uint32(dst)) }
func (a *Assembler) FMAXNMd(dst, src1, src2 FReg) { a.emit(0x1e626800 | uint32(src2)<<16 | uint32(src1)<<5 | uint32(dst)) }
func (a *Assembler) FMINNMd(dst, src1, src2 FReg) { a.emit(0x1e627800 | uint32(src2)<<16 | uint32(src1)<<5 | uint32(dst)) }

// ARM64 shift/logic instructions
func (a *Assembler) LSLreg(dst, src, amount Reg) { a.emit(0x9ac02000 | uint32(amount)<<16 | uint32(src)<<5 | uint32(dst)) }
func (a *Assembler) LSRreg(dst, src, amount Reg) { a.emit(0x9ac02400 | uint32(amount)<<16 | uint32(src)<<5 | uint32(dst)) }
func (a *Assembler) ORNreg(dst, src1, src2 Reg)  { a.emit(0xaa200000 | uint32(src2)<<16 | uint32(src1)<<5 | uint32(dst)) }

// TraceContext iteration fields (appended after ExitFPR).
// These must be at the END of TraceContext to avoid breaking existing offsets.
// The ARM64 codegen uses these offsets for iteration counting.
var (
	TraceCtxOffIterCount = int(unsafe.Offsetof(TraceContext{}.IterationCount))
	TraceCtxOffMaxIter   = int(unsafe.Offsetof(TraceContext{}.MaxIterations))
)

func init() {
	// Sanity check that the new fields don't collide with existing offsets
	if TraceCtxOffIterCount <= TraceCtxOffExitFPR {
		panic("jit: TraceContext.IterationCount offset overlaps with ExitFPR")
	}
}

// DeoptMetadata holds guard-level type expectations for deoptimization.
type DeoptMetadata struct {
	Guards []*DeoptGuard
}

// NewDeoptMetadata creates an empty DeoptMetadata.
func NewDeoptMetadata() *DeoptMetadata {
	return &DeoptMetadata{}
}

// DeoptGuard holds the expected type for a single guard.
type DeoptGuard struct {
	Expected interface{} // typically runtime.ValueType
}

// SSAInst.String returns a human-readable representation.
func (inst SSAInst) String() string {
	name := ssaOpString(inst.Op)
	return fmt.Sprintf("%s slot=%d type=%d", name, inst.Slot, inst.Type)
}

// DumpSSA prints SSA IR to stdout.
func DumpSSA(f *SSAFunc) {
	for i, inst := range f.Insts {
		fmt.Printf("  %3d: %s\n", i, inst.String())
	}
}

// DumpRegAlloc prints register allocation to stdout.
func DumpRegAlloc(rm *RegMap) {
	if rm == nil {
		fmt.Println("  (no regmap)")
		return
	}
	fmt.Printf("  IntRegs: %v\n", rm.Int)
}

// DumpRegisters prints register values to stdout.
func DumpRegisters(regs []runtime.Value, slots []int) {
	for _, s := range slots {
		if s < len(regs) {
			fmt.Printf("  [%d]=0x%016x %s\n", s, regs[s].Raw(), formatValueByVal(regs[s]))
		}
	}
}

// formatValue formats a runtime.Value for diagnostic output.
func formatValue(v *runtime.Value) string {
	return formatValueByVal(*v)
}

// formatValueByVal formats a runtime.Value by value.
func formatValueByVal(v runtime.Value) string {
	switch v.Type() {
	case runtime.TypeInt:
		return fmt.Sprintf("int(%d)", v.Int())
	case runtime.TypeFloat:
		return fmt.Sprintf("float(%.6f)", v.Float())
	case runtime.TypeBool:
		return fmt.Sprintf("bool(%v)", v.Bool())
	case runtime.TypeNil:
		return "nil"
	default:
		return fmt.Sprintf("<%d>", v.Type())
	}
}
