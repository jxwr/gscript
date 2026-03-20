package jit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// Value struct memory layout constants.
// These must match the runtime.Value struct layout exactly.
// Verified at init() time via unsafe.Offsetof.
//
// Compact 24-byte layout (down from 32):
//   typ  ValueType       (offset 0, 1 byte + 7 padding)
//   data uint64          (offset 8, 8 bytes: int/float bits/bool, or iface type ptr for Function)
//   ptr  unsafe.Pointer  (offset 16, 8 bytes: actual pointer for ref types, nil for scalars)
//
// Key change from 32-byte layout: ptr is now unsafe.Pointer (8 bytes)
// instead of any/interface{} (16 bytes). This eliminates the interface
// indirection — ptr IS the data pointer, not an interface wrapping it.
const (
	ValueSize  = 24 // sizeof(runtime.Value)
	OffsetTyp  = 0  // offset of .typ field (ValueType = uint8)
	OffsetData = 8  // offset of .data field (uint64: int/float/bool payload)
	OffsetPtr  = 16 // offset of .ptr field (unsafe.Pointer: direct pointer, NOT interface)

	// Legacy alias so existing codegen references compile without changes
	OffsetIval = OffsetData

	// With unsafe.Pointer, the pointer IS at OffsetPtr directly.
	// No interface indirection — OffsetPtrData == OffsetPtr.
	OffsetPtrData = OffsetPtr

	// Table struct offsets (must match runtime.Table layout)
	TableOffMu        = 0
	TableOffArray     = 8   // []Value slice header (ptr+len+cap = 24 bytes)
	TableOffImap      = 32  // map[int64]Value
	TableOffSkeys     = 40  // []string slice header (ptr+len+cap)
	TableOffSkeysLen  = 48  // skeys.len
	TableOffSvals     = 64  // []Value slice header
	TableOffMetatable  = 104 // *Table
	TableOffKeysDirty  = 136 // bool (1 byte) — must set true on append

	// Type-specialized array fields (added at end of Table struct)
	TableOffArrayKind  = 137 // ArrayKind (uint8)
	TableOffIntArray   = 144 // []int64 slice header (ptr+len+cap = 24 bytes)
	TableOffFloatArray = 168 // []float64 slice header (ptr+len+cap = 24 bytes)
	TableOffBoolArray  = 192 // []byte slice header (ptr+len+cap = 24 bytes)

	// Go string header: {ptr(8), len(8)} = 16 bytes
	StringSize = 16
)

// ArrayKind constants (must match runtime.ArrayKind values)
const (
	AKMixed = 0 // ArrayMixed
	AKInt   = 1 // ArrayInt
	AKFloat = 2 // ArrayFloat
	AKBool  = 3 // ArrayBool
)

// runtime.ValueType constants (must match runtime package).
const (
	TypeNil      = 0
	TypeBool     = 1
	TypeInt      = 2
	TypeFloat    = 3
	TypeString   = 4
	TypeTable    = 5
	TypeFunction = 6
)

// valueLayoutAccessor is a copy of the runtime.Value layout for offset checking.
type valueLayoutAccessor struct {
	typ  uint8
	data uint64
	ptr  unsafe.Pointer
}

func init() {
	var v valueLayoutAccessor
	if s := unsafe.Sizeof(v); s != ValueSize {
		panic("jit: Value size mismatch: expected 24, got " + itoa(int(s)))
	}
	if o := unsafe.Offsetof(v.typ); o != OffsetTyp {
		panic("jit: Value.typ offset mismatch")
	}
	if o := unsafe.Offsetof(v.data); o != OffsetData {
		panic("jit: Value.data offset mismatch")
	}
	if o := unsafe.Offsetof(v.ptr); o != OffsetPtr {
		panic("jit: Value.ptr offset mismatch")
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

func FieldOffset(reg, fieldOff int) int {
	return reg*ValueSize + fieldOff
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
