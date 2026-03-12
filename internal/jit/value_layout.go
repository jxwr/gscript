package jit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

// Value struct memory layout constants.
// These must match the runtime.Value struct layout exactly.
// Verified at init() time via unsafe.Offsetof.
const (
	ValueSize    = 56 // sizeof(runtime.Value)
	OffsetTyp    = 0  // offset of .typ field (ValueType = uint8)
	OffsetIval   = 8  // offset of .ival field (int64)
	OffsetFval   = 16 // offset of .fval field (float64)
	OffsetSval   = 24 // offset of .sval field (string header: ptr + len = 16 bytes)
	OffsetPtr    = 40 // offset of .ptr field (any/interface: type_ptr + data_ptr = 16 bytes)
)

// runtime.ValueType constants (must match runtime package).
const (
	TypeNil   = 0
	TypeBool  = 1
	TypeInt   = 2
	TypeFloat = 3
)

// valueLayoutAccessor is a copy of the runtime.Value layout for offset checking.
// This struct must be kept in sync with runtime.Value.
type valueLayoutAccessor struct {
	typ  uint8
	ival int64
	fval float64
	sval string
	ptr  any
}

func init() {
	var v valueLayoutAccessor
	// Verify that our layout constants match the actual struct layout.
	if s := unsafe.Sizeof(v); s != ValueSize {
		panic("jit: Value size mismatch: expected 56, got " + itoa(int(s)))
	}
	if o := unsafe.Offsetof(v.typ); o != OffsetTyp {
		panic("jit: Value.typ offset mismatch")
	}
	if o := unsafe.Offsetof(v.ival); o != OffsetIval {
		panic("jit: Value.ival offset mismatch")
	}
	if o := unsafe.Offsetof(v.fval); o != OffsetFval {
		panic("jit: Value.fval offset mismatch")
	}
	if o := unsafe.Offsetof(v.sval); o != OffsetSval {
		panic("jit: Value.sval offset mismatch")
	}
	if o := unsafe.Offsetof(v.ptr); o != OffsetPtr {
		panic("jit: Value.ptr offset mismatch")
	}

	// Also verify the runtime ValueType constants.
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

// itoa is a simple int to string conversion for panic messages (avoids fmt import).
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

// ValueOffset returns the byte offset of VM register i in the register file.
// regs[i] is at base + i * ValueSize.
func ValueOffset(reg int) int {
	return reg * ValueSize
}

// FieldOffset returns the byte offset of a field within VM register i.
func FieldOffset(reg, fieldOff int) int {
	return reg*ValueSize + fieldOff
}
