package jit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// Value struct memory layout constants.
// These must match the runtime.Value struct layout exactly.
// Verified at init() time via unsafe.Offsetof.
//
// Compact 32-byte layout:
//   typ  ValueType  (offset 0, 1 byte + 7 padding)
//   data uint64     (offset 8, 8 bytes: int/float bits/bool)
//   ptr  any        (offset 16, 16 bytes: interface{} = type_ptr + data_ptr)
const (
	ValueSize   = 32 // sizeof(runtime.Value)
	OffsetTyp   = 0  // offset of .typ field (ValueType = uint8)
	OffsetData  = 8  // offset of .data field (uint64)
	OffsetPtr   = 16 // offset of .ptr field (any/interface: type_ptr + data_ptr = 16 bytes)

	// Legacy alias so existing codegen references compile without changes
	OffsetIval = OffsetData
)

// runtime.ValueType constants (must match runtime package).
const (
	TypeNil   = 0
	TypeBool  = 1
	TypeInt   = 2
	TypeFloat = 3
)

// valueLayoutAccessor is a copy of the runtime.Value layout for offset checking.
type valueLayoutAccessor struct {
	typ  uint8
	data uint64
	ptr  any
}

func init() {
	var v valueLayoutAccessor
	if s := unsafe.Sizeof(v); s != ValueSize {
		panic("jit: Value size mismatch: expected 32, got " + itoa(int(s)))
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
