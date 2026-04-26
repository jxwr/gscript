package jit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// Value memory layout constants for NaN-boxed 8-byte values.
// runtime.Value is now a uint64 (NaN-boxed). No sub-field offsets exist.
//
// NaN-boxing encoding:
//
//	Float64:  raw IEEE 754 bits (bits 50-62 NOT all 1)
//	Tagged:   bits 50-62 all 1 (qNaN), sign=1, bits 48-49 = type tag,
//	          bits 0-47 = 48-bit payload.
//
//	Tag (top 16 bits): 0xFFFC=nil, 0xFFFD=bool, 0xFFFE=int, 0xFFFF=pointer
const (
	ValueSize = 8 // sizeof(runtime.Value) = sizeof(uint64)

	// NaN-boxing tags (top 16 bits of the 64-bit value)
	NB_PayloadMask uint64 = 0x0000FFFFFFFFFFFF

	NB_TagNil  uint64 = 0xFFFC000000000000
	NB_TagBool uint64 = 0xFFFD000000000000
	NB_TagInt  uint64 = 0xFFFE000000000000
	NB_TagPtr  uint64 = 0xFFFF000000000000

	NB_ValNil   uint64 = NB_TagNil
	NB_ValFalse uint64 = NB_TagBool // payload = 0

	// Tag values right-shifted by 48 (for LSR-based type checks)
	NB_TagNilShr48  = 0xFFFC
	NB_TagBoolShr48 = 0xFFFD
	NB_TagIntShr48  = 0xFFFE
	NB_TagPtrShr48  = 0xFFFF

	// Pointer sub-type bits (bits 44-47 of payload)
	NB_PtrSubShift = 44

	// Table struct offsets (must match runtime.Table layout)
	TableOffArray     = 8   // []Value slice header (ptr+len+cap = 24 bytes)
	TableOffArrayLen  = 16  // array slice len field (8 bytes after data ptr)
	TableOffImap      = 32  // map[int64]Value
	TableOffSkeys     = 40  // []string slice header (ptr+len+cap)
	TableOffSkeysLen  = 48  // skeys.len
	TableOffSvals     = 64  // []Value slice header
	TableOffSvalsLen  = 72  // svals slice len field
	TableOffHash      = 96  // map[Value]Value
	TableOffMetatable = 104 // *Table
	TableOffKeysDirty = 136 // bool (1 byte) — must set true on append

	// FieldCacheEntry layout (for GETFIELD/SETFIELD inline caching)
	FieldCacheEntrySize        = 24 // sizeof(runtime.FieldCacheEntry)
	FieldCacheEntryOffFieldIdx = 0  // int (8 bytes)
	FieldCacheEntryOffShapeID  = 8  // uint32

	// Type-specialized array fields (added at end of Table struct)
	TableOffArrayKind     = 137 // ArrayKind (uint8)
	TableOffShapeID       = 140 // uint32 — shape identifier for field cache validation
	TableOffShape         = 144 // *Shape (pointer, 8 bytes) — TODO: verify with unsafe.Offsetof
	TableOffIntArray      = 144 // []int64 slice header (ptr+len+cap = 24 bytes)
	TableOffIntArrayLen   = 152 // intArray slice len field (8 bytes after data ptr)
	TableOffIntArrayCap   = 160 // intArray slice cap field
	TableOffFloatArray    = 168 // []float64 slice header (ptr+len+cap = 24 bytes)
	TableOffFloatArrayLen = 176 // floatArray slice len field (168 + 8)
	TableOffFloatArrayCap = 184 // floatArray slice cap field
	TableOffBoolArray     = 192 // []byte slice header (ptr+len+cap = 24 bytes)
	TableOffBoolArrayLen  = 200 // boolArray slice len field (192 + 8)
	TableOffBoolArrayCap  = 208 // boolArray slice cap field
	// R43 Phase 2 DenseMatrix descriptor fields.
	TableOffDMFlat   = 224 // unsafe.Pointer — flat backing head
	TableOffDMStride = 232 // int32 — row stride (columns)
)

// Legacy compatibility alias for codegen transition.
// With NaN-boxing, there are no sub-field offsets. This equals 0
// so that `reg*ValueSize + OffsetData` reduces to `reg*ValueSize`.
const OffsetData = 0

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

	// Verify FieldCacheEntry layout
	var fce runtime.FieldCacheEntry
	if sz := unsafe.Sizeof(fce); sz != FieldCacheEntrySize {
		panic("jit: FieldCacheEntry size mismatch: expected " + itoa(FieldCacheEntrySize) + ", got " + itoa(int(sz)))
	}
	if off := unsafe.Offsetof(fce.FieldIdx); off != FieldCacheEntryOffFieldIdx {
		panic("jit: FieldCacheEntry.FieldIdx offset mismatch")
	}
	if off := unsafe.Offsetof(fce.ShapeID); off != FieldCacheEntryOffShapeID {
		panic("jit: FieldCacheEntry.ShapeID offset mismatch: expected " + itoa(FieldCacheEntryOffShapeID) + ", got " + itoa(int(off)))
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
	imapOff, hashOff := runtime.TableMapOffsets()
	if imapOff != TableOffImap {
		panic("jit: Table.imap offset mismatch: expected " + itoa(TableOffImap) + ", got " + itoa(int(imapOff)))
	}
	if hashOff != TableOffHash {
		panic("jit: Table.hash offset mismatch: expected " + itoa(TableOffHash) + ", got " + itoa(int(hashOff)))
	}
	iaCapOff, faCapOff, baCapOff := runtime.TableTypedArrayCapOffsets()
	if iaCapOff != TableOffIntArrayCap {
		panic("jit: Table.intArray cap offset mismatch: expected " + itoa(TableOffIntArrayCap) + ", got " + itoa(int(iaCapOff)))
	}
	if faCapOff != TableOffFloatArrayCap {
		panic("jit: Table.floatArray cap offset mismatch: expected " + itoa(TableOffFloatArrayCap) + ", got " + itoa(int(faCapOff)))
	}
	if baCapOff != TableOffBoolArrayCap {
		panic("jit: Table.boolArray cap offset mismatch: expected " + itoa(TableOffBoolArrayCap) + ", got " + itoa(int(baCapOff)))
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

	// R43 Phase 2: verify DenseMatrix descriptor offsets.
	if off := runtime.TableDMFlatOffset(); off != TableOffDMFlat {
		panic("jit: Table.dmFlat offset mismatch: expected " + itoa(TableOffDMFlat) + ", got " + itoa(int(off)))
	}
	if off := runtime.TableDMStrideOffset(); off != TableOffDMStride {
		panic("jit: Table.dmStride offset mismatch: expected " + itoa(TableOffDMStride) + ", got " + itoa(int(off)))
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
		asm.LSLimm(rd, rd, 3)        // rd = rn * 24
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
// After this, CondEQ = tagged, CondNE = float. Clobbers scratch and X16.
func EmitIsTagged(asm *Assembler, valReg, scratch Reg) {
	asm.LSRimm(scratch, valReg, 50)
	asm.MOVimm16(X16, 0x3FFF) // 14 bits all set = 0x3FFF
	asm.CMPreg(scratch, X16)
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
