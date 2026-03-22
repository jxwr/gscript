package jit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// Value memory layout constants for NaN-boxed 8-byte values.
// runtime.Value is now a uint64 (NaN-boxed). No sub-field offsets exist.
//
// NaN-boxing encoding:
//   Float64:  raw IEEE 754 bits (bits 50-62 NOT all 1)
//   Tagged:   bits 50-62 all 1 (qNaN), sign=1, bits 48-49 = type tag,
//             bits 0-47 = 48-bit payload.
//
//   Tag (top 16 bits): 0xFFFC=nil, 0xFFFD=bool, 0xFFFE=int, 0xFFFF=pointer
const (
	ValueSize = 8 // sizeof(runtime.Value) = sizeof(uint64)

	// NaN-boxing tags (top 16 bits of the 64-bit value)
	NB_TagMask     uint64 = 0xFFFF000000000000
	NB_PayloadMask uint64 = 0x0000FFFFFFFFFFFF
	NB_NanBits     uint64 = 0x7FFC000000000000 // bits 50-62

	NB_TagNil  uint64 = 0xFFFC000000000000
	NB_TagBool uint64 = 0xFFFD000000000000
	NB_TagInt  uint64 = 0xFFFE000000000000
	NB_TagPtr  uint64 = 0xFFFF000000000000

	NB_ValNil   uint64 = NB_TagNil
	NB_ValFalse uint64 = NB_TagBool     // payload = 0
	NB_ValTrue  uint64 = NB_TagBool | 1 // payload = 1

	// Tag values right-shifted by 48 (for LSR-based type checks)
	NB_TagNilShr48  = 0xFFFC
	NB_TagBoolShr48 = 0xFFFD
	NB_TagIntShr48  = 0xFFFE
	NB_TagPtrShr48  = 0xFFFF

	// Pointer sub-type bits (bits 44-47 of payload)
	NB_PtrSubShift   = 44
	NB_PtrSubMask    = uint64(0xF) << NB_PtrSubShift
	NB_PtrAddrMask   = (uint64(1) << NB_PtrSubShift) - 1
	NB_PtrSubTable   = uint64(0) << NB_PtrSubShift
	NB_PtrSubString  = uint64(1) << NB_PtrSubShift

	// Table struct offsets (must match runtime.Table layout)
	TableOffMu         = 0
	TableOffArray      = 8   // []Value slice header (ptr+len+cap = 24 bytes)
	TableOffImap       = 32  // map[int64]Value
	TableOffSkeys      = 40  // []string slice header (ptr+len+cap)
	TableOffSkeysLen   = 48  // skeys.len
	TableOffSvals      = 64  // []Value slice header
	TableOffMetatable  = 104 // *Table
	TableOffKeysDirty  = 136 // bool (1 byte) — must set true on append

	// Type-specialized array fields (added at end of Table struct)
	TableOffArrayKind  = 137 // ArrayKind (uint8)
	TableOffShapeID    = 140 // uint32 — shape identifier for field cache validation
	TableOffIntArray   = 144 // []int64 slice header (ptr+len+cap = 24 bytes)
	TableOffFloatArray = 168 // []float64 slice header (ptr+len+cap = 24 bytes)
	TableOffBoolArray  = 192 // []byte slice header (ptr+len+cap = 24 bytes)

	// Go string header: {ptr(8), len(8)} = 16 bytes
	StringSize = 16
)

// Legacy compatibility aliases for codegen transition.
// With NaN-boxing, there are no sub-field offsets. These all equal 0
// so that `reg*ValueSize + OffsetXxx` reduces to `reg*ValueSize`.
const (
	OffsetTyp     = 0
	OffsetData    = 0
	OffsetIval    = 0
	OffsetPtr     = 0
	OffsetPtrData = 0
)

// ArrayKind constants (must match runtime.ArrayKind values)
const (
	AKMixed = 0 // ArrayMixed
	AKInt   = 1 // ArrayInt
	AKFloat = 2 // ArrayFloat
	AKBool  = 3 // ArrayBool
)

// runtime.ValueType constants (must match runtime package).
// These are the small integer type codes used by the runtime, NOT the NaN-box tags.
// For NaN-box type checks in JIT codegen, use NB_TagXxxShr48 with LSR #48.
const (
	TypeNil      = 0
	TypeBool     = 1
	TypeInt      = 2
	TypeFloat    = 3
	TypeString   = 4
	TypeTable    = 5
	TypeFunction = 6
)

func init() {
	// Verify runtime.Value is 8 bytes (NaN-boxed uint64)
	var v runtime.Value
	if s := unsafe.Sizeof(v); s != ValueSize {
		panic("jit: Value size mismatch: expected 8, got " + itoa(int(s)))
	}

	// Verify Table layout
	var t runtime.Table
	t.SetConcurrent(false) // ensure mu is nil for offset checking
	_ = t

	if uint8(runtime.TypeNil) != TypeNil {
		panic("jit: TypeNil mismatch")
	}
	if uint8(runtime.TypeBool) != TypeBool {
		panic("jit: TypeBool mismatch")
	}
	if uint8(runtime.TypeInt) != TypeInt {
		panic("jit: TypeInt mismatch")
	}
	if uint8(runtime.TypeFloat) != TypeFloat {
		panic("jit: TypeFloat mismatch")
	}

	// Verify typed array field offsets
	akOff, iaOff, faOff, baOff := runtime.TableFieldOffsets()
	if akOff != TableOffArrayKind {
		panic("jit: Table.arrayKind offset mismatch: expected " + itoa(TableOffArrayKind) + ", got " + itoa(int(akOff)))
	}
	if iaOff != TableOffIntArray {
		panic("jit: Table.intArray offset mismatch: expected " + itoa(TableOffIntArray) + ", got " + itoa(int(iaOff)))
	}
	if faOff != TableOffFloatArray {
		panic("jit: Table.floatArray offset mismatch: expected " + itoa(TableOffFloatArray) + ", got " + itoa(int(faOff)))
	}
	if baOff != TableOffBoolArray {
		panic("jit: Table.boolArray offset mismatch: expected " + itoa(TableOffBoolArray) + ", got " + itoa(int(baOff)))
	}

	// Verify keysDirty offset
	kdOff := runtime.TableKeysDirtyOffset()
	if kdOff != TableOffKeysDirty {
		panic("jit: Table.keysDirty offset mismatch: expected " + itoa(TableOffKeysDirty) + ", got " + itoa(int(kdOff)))
	}

	// Verify shapeID offset
	shOff := runtime.TableShapeIDOffset()
	if shOff != TableOffShapeID {
		panic("jit: Table.shapeID offset mismatch: expected " + itoa(TableOffShapeID) + ", got " + itoa(int(shOff)))
	}

	// Verify ArrayKind constants
	if uint8(runtime.ArrayMixed) != AKMixed {
		panic("jit: ArrayMixed mismatch")
	}
	if uint8(runtime.ArrayInt) != AKInt {
		panic("jit: ArrayInt mismatch")
	}
	if uint8(runtime.ArrayFloat) != AKFloat {
		panic("jit: ArrayFloat mismatch")
	}
	if uint8(runtime.ArrayBool) != AKBool {
		panic("jit: ArrayBool mismatch")
	}

	// Verify NaN-boxing tag constants match runtime
	if NB_TagNil != uint64(runtime.TagNil) {
		panic("jit: NB_TagNil mismatch with runtime")
	}
	if NB_TagBool != uint64(runtime.TagBool) {
		panic("jit: NB_TagBool mismatch with runtime")
	}
	if NB_TagInt != uint64(runtime.TagInt) {
		panic("jit: NB_TagInt mismatch with runtime")
	}
	if NB_TagPtr != uint64(runtime.TagPtr) {
		panic("jit: NB_TagPtr mismatch with runtime")
	}
	if NB_PayloadMask != uint64(runtime.PayloadMask) {
		panic("jit: NB_PayloadMask mismatch with runtime")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ValueOffset(reg int) int {
	return reg * ValueSize
}

// EmitMulValueSize emits ARM64 instructions to compute rd = rn * ValueSize.
// Uses scratch register (must not alias rn) for the multiplication constant.
// For power-of-2 ValueSize, uses a single LSL. For ValueSize=24, uses
// ADD+LSL (rd = rn*3 << 3). For other sizes, uses MOVZ+MUL.
func EmitMulValueSize(asm *Assembler, rd, rn, scratch Reg) {
	switch ValueSize {
	case 8:
		asm.LSLimm(rd, rn, 3)
	case 16:
		asm.LSLimm(rd, rn, 4)
	case 24:
		// 24 = 3 * 8. Compute rn * 3, then shift left by 3.
		// ADD rd, rn, rn LSL #1   (rd = rn + rn*2 = rn*3)
		// LSL rd, rd, #3          (rd = rn*3 * 8 = rn*24)
		asm.ADDregLSL(rd, rn, rn, 1) // rd = rn * 3
		asm.LSLimm(rd, rd, 3)         // rd = rn * 24
	case 32:
		asm.LSLimm(rd, rn, 5)
	default:
		asm.LoadImm64(scratch, int64(ValueSize))
		asm.MUL(rd, rn, scratch)
	}
}

// ---------------------------------------------------------------------------
// NaN-boxing codegen helpers
// ---------------------------------------------------------------------------
// These emit ARM64 instruction sequences for common NaN-box operations.
// They use scratch registers X0-X9 and must not clobber registers the
// caller is still using.

// EmitLoadNBValue loads the full 8-byte NaN-boxed Value of VM register 'reg'
// from regRegs into ARM64 register dst.
func EmitLoadNBValue(asm *Assembler, dst Reg, base Reg, reg int) {
	off := reg * ValueSize
	if off <= 32760 {
		asm.LDR(dst, base, off)
	} else {
		asm.LoadImm64(dst, int64(off))
		asm.ADDreg(dst, base, dst)
		asm.LDR(dst, dst, 0)
	}
}

// EmitStoreNBValue stores a full 8-byte NaN-boxed Value from ARM64 register src
// into VM register 'reg' at base.
func EmitStoreNBValue(asm *Assembler, src Reg, base Reg, reg int) {
	off := reg * ValueSize
	if off <= 32760 {
		asm.STR(src, base, off)
	} else {
		asm.LoadImm64(X10, int64(off))
		asm.ADDreg(X10, base, X10)
		asm.STR(src, X10, 0)
	}
}

// EmitCheckTagShr48 checks if the NaN-boxed value in 'valReg' has tag == expected.
// Uses scratch1 for the shifted value, scratch2 for the constant.
// After this, condition flags are set for B.NE.
// Pattern: LSR scratch1, valReg, #48; LoadImm64 scratch2, tag>>48; CMP scratch1, scratch2
func EmitCheckTagShr48(asm *Assembler, valReg, scratch1, scratch2 Reg, tagShr48 uint16) {
	asm.LSRimm(scratch1, valReg, 48)
	asm.MOVimm16(scratch2, tagShr48) // 16-bit immediate fits in MOVZ
	asm.CMPreg(scratch1, scratch2)
}

// EmitUnboxInt extracts a 48-bit signed integer from a NaN-boxed value.
// Result is sign-extended to 64 bits in dst.
// Pattern: SBFX dst, src, #0, #48
func EmitUnboxInt(asm *Assembler, dst, src Reg) {
	asm.SBFX(dst, src, 0, 48)
}

// nb_i64 converts a uint64 NaN-boxing constant to int64 for LoadImm64.
func nb_i64(v uint64) int64 { return int64(v) }

// EmitBoxInt creates a NaN-boxed int value from a raw int in src, stores in dst.
// Uses scratch for the tag constant. Used by SSA/trace codegen which don't have
// a pinned tag register.
// Pattern: UBFX dst, src, #0, #48; LoadImm64 scratch, tagInt; ORR dst, dst, scratch
func EmitBoxInt(asm *Assembler, dst, src, scratch Reg) {
	asm.LoadImm64(scratch, nb_i64(NB_TagInt))
	asm.UBFX(dst, src, 0, 48) // clear top 16 bits in 1 instruction
	asm.ORRreg(dst, dst, scratch)
}

// EmitBoxIntFast creates a NaN-boxed int value using the pinned tag register (regTagInt/X24).
// Only 2 instructions: UBFX + ORR. Used by method JIT codegen where regTagInt is available.
func EmitBoxIntFast(asm *Assembler, dst, src, tagReg Reg) {
	asm.UBFX(dst, src, 0, 48) // clear top 16 bits in 1 instruction
	asm.ORRreg(dst, dst, tagReg)
}

// EmitBoxIntInPlace adds int tag to a value already masked to 48 bits.
// Requires tagReg to hold tagInt (either scratch loaded or regTagInt pinned).
func EmitBoxIntInPlace(asm *Assembler, dst, tagReg Reg) {
	asm.ORRreg(dst, dst, tagReg)
}

// EmitExtractPtr extracts the 44-bit pointer address from a NaN-boxed pointer value.
// Uses UBFX to extract bits 0-43 in a single instruction (was 4 insns with LoadImm64+AND).
func EmitExtractPtr(asm *Assembler, dst, src Reg) {
	asm.UBFX(dst, src, 0, 44)
}

// EmitBoxNil stores NaN-boxed nil (0xFFFC000000000000) into dst.
func EmitBoxNil(asm *Assembler, dst Reg) {
	asm.LoadImm64(dst, nb_i64(NB_ValNil))
}

// EmitBoxBool stores NaN-boxed bool into dst. boolReg should contain 0 or 1.
func EmitBoxBool(asm *Assembler, dst, boolReg, scratch Reg) {
	asm.LoadImm64(scratch, nb_i64(NB_TagBool))
	asm.ORRreg(dst, boolReg, scratch)
}

// EmitGuardType emits a NaN-boxing type guard. Loads the value at slot from base,
// checks that its type matches expectedType (a runtime.ValueType constant),
// and branches to failLabel on mismatch.
// Uses scratch1, scratch2. After return, the NaN-boxed value may be in scratch1 or X0.
func EmitGuardType(asm *Assembler, base Reg, slot int, expectedType int, failLabel string) {
	off := slot * ValueSize
	asm.LDR(X0, base, off) // load NaN-boxed value

	switch expectedType {
	case TypeInt: // 2
		asm.LSRimm(X1, X0, 48)
		asm.MOVimm16(X2, NB_TagIntShr48) // 0xFFFE
		asm.CMPreg(X1, X2)
		asm.BCond(CondNE, failLabel)
	case TypeFloat: // 3
		// Float: bits 50-62 NOT all set. Check: (val >> 50) != 0x3FFF
		asm.LSRimm(X1, X0, 50)
		asm.MOVimm16(X2, 0x3FFF) // 14 bits all set
		asm.CMPreg(X1, X2)
		asm.BCond(CondEQ, failLabel) // if all tag bits set = NOT float → fail
	case TypeBool: // 1
		asm.LSRimm(X1, X0, 48)
		asm.MOVimm16(X2, NB_TagBoolShr48) // 0xFFFD
		asm.CMPreg(X1, X2)
		asm.BCond(CondNE, failLabel)
	case TypeNil: // 0
		asm.LoadImm64(X1, nb_i64(NB_ValNil))
		asm.CMPreg(X0, X1)
		asm.BCond(CondNE, failLabel)
	case TypeTable: // 5
		EmitCheckIsTableFull(asm, X0, X1, X2, failLabel)
	case TypeString: // 4
		EmitCheckIsString(asm, X0, X1, X2, failLabel)
	default:
		// Unknown type → always fail
		asm.B(failLabel)
	}
}

// EmitGuardTypeRelaxedFloat emits a relaxed float guard: accepts any non-pointer type.
// Used for write-before-read float slots where the value may be garbage.
func EmitGuardTypeRelaxedFloat(asm *Assembler, base Reg, slot int, failLabel string) {
	off := slot * ValueSize
	asm.LDR(X0, base, off)
	// Accept if NOT a pointer (tag != 0xFFFF). Pointers would cause crashes if used as float.
	asm.LSRimm(X1, X0, 48)
	asm.MOVimm16(X2, NB_TagPtrShr48) // 0xFFFF
	asm.CMPreg(X1, X2)
	asm.BCond(CondEQ, failLabel) // pointer → fail
}

// EmitIsTagged checks if a NaN-boxed value is tagged (non-float).
// Float values have bits 50-62 NOT all set.
// Tagged values have bits 50-62 all set AND bit 63 set.
// Quick check: LSR #50, CMP #0x3FFF (bits 63:50 all set = tagged)
// After this, CondEQ = tagged, CondNE = float.
func EmitIsTagged(asm *Assembler, valReg, scratch Reg) {
	asm.LSRimm(scratch, valReg, 50)
	asm.CMPimm(scratch, 0x3FFF) // 14 bits all set = 0x3FFF
}

// EmitCheckIsTable checks if a NaN-boxed value is a table pointer.
// Uses scratch for intermediate. After this, CondEQ = is table, CondNE = not table.
// Pattern: LSR #48 → must be 0xFFFF (ptr tag), then check bits 44-47 = 0 (ptrSubTable)
func EmitCheckIsTable(asm *Assembler, valReg, scratch Reg) {
	// First check it's a pointer tag
	asm.LSRimm(scratch, valReg, 48)
	asm.CMPimm(scratch, NB_TagPtrShr48)
	// If not pointer, the NE flag is already set, caller checks with CondNE
	// But for table we also need to check sub-type = 0
	// We'll use a different approach: check the full top 20 bits
	// Table: top 16 bits = 0xFFFF, bits 44-47 = 0
	// So bits 44-63 = 0xFFFF0 shifted left. Let's check:
	// LSR #44 gives us bits 63:44 in the low 20 bits
	// For table pointer: that's 0xFFFF0 >> 0 = top16=FFFF, sub=0 → 0xFFFFF * ...
	// Actually simpler: check top 20 bits. Table = tagPtr | ptrSubTable = 0xFFFF000000000000
	// LSR #44 → 0xFFFFF (the F from ptr tag and 0 from sub-type gives 0xFFFF0)
	// Hmm, let me think again. 0xFFFF000000000000 >> 44 = 0xFFFF0.
	// That's > 12 bits, can't use CMPimm. So use a 2-step approach.
	// We already have LSR #48 in scratch. If that == 0xFFFF, check sub-type separately.
}

// EmitCheckIsTableFull is a full table check: tag=ptr AND sub=table.
// Branches to 'notTableLabel' if not a table. Uses scratch1 and scratch2.
func EmitCheckIsTableFull(asm *Assembler, valReg, scratch1, scratch2 Reg, notTableLabel string) {
	// Check top 16 bits == 0xFFFF (pointer tag)
	asm.LSRimm(scratch1, valReg, 48)
	asm.MOVimm16(scratch2, NB_TagPtrShr48) // 0xFFFF
	asm.CMPreg(scratch1, scratch2)
	asm.BCond(CondNE, notTableLabel)
	// Check pointer sub-type (bits 44-47) == 0 (ptrSubTable)
	asm.LSRimm(scratch1, valReg, uint8(NB_PtrSubShift))
	asm.LoadImm64(scratch2, 0xF)
	asm.ANDreg(scratch1, scratch1, scratch2)
	asm.CMPimm(scratch1, 0) // ptrSubTable = 0 (fits in 12 bits)
	asm.BCond(CondNE, notTableLabel)
}

// EmitCheckIsString checks if a NaN-boxed value is a string pointer.
// Branches to 'notStringLabel' if not. Uses scratch1 and scratch2.
func EmitCheckIsString(asm *Assembler, valReg, scratch1, scratch2 Reg, notStringLabel string) {
	asm.LSRimm(scratch1, valReg, 48)
	asm.MOVimm16(scratch2, NB_TagPtrShr48) // 0xFFFF
	asm.CMPreg(scratch1, scratch2)
	asm.BCond(CondNE, notStringLabel)
	asm.LSRimm(scratch1, valReg, uint8(NB_PtrSubShift))
	asm.LoadImm64(scratch2, 0xF)
	asm.ANDreg(scratch1, scratch1, scratch2)
	asm.CMPimm(scratch1, 1) // ptrSubString = 1 (fits in 12 bits)
	asm.BCond(CondNE, notStringLabel)
}
