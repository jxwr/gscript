// Package nanbox implements NaN-boxed 8-byte Values for GScript Season 2.
//
// Every GScript value (float64, int48, bool, nil, pointer) is packed into a
// single uint64 using the quiet-NaN payload space of IEEE 754 doubles.
//
// Encoding layout:
//
//	Float64: any IEEE 754 bit pattern that is NOT a quiet NaN with bit 50 set.
//	Tagged:  bits 50-62 all 1 (qNaN), bit 63=1, bits 48-49 = type tag,
//	         bits 0-47 = 48-bit payload.
//
//	tag 00 = nil      (payload 0)
//	tag 01 = bool     (payload 0=false, 1=true)
//	tag 10 = int48    (48-bit two's complement)
//	tag 11 = pointer  (48-bit address)
package nanbox

import (
	"fmt"
	"math"
	"unsafe"
)

// Value is a NaN-boxed 8-byte value.
type Value uint64

// -------------------------------------------------------------------------
// Constants
// -------------------------------------------------------------------------

const (
	// nanBits: bits 50-62 all set = quiet NaN marker with our discriminator.
	// Any uint64 with these bits set is a tagged (non-float) value.
	nanBits uint64 = 0x7FFC000000000000

	// tagBase: sign bit (63) + nanBits.  All tagged values have sign=1.
	tagBase uint64 = 0xFFFC000000000000

	// Type tags (sign=1 + nanBits + 2-bit tag in bits 48-49).
	tagNil  uint64 = 0xFFFC000000000000 // sign=1, tag=00
	tagBool uint64 = 0xFFFD000000000000 // sign=1, tag=01
	tagInt  uint64 = 0xFFFE000000000000 // sign=1, tag=10
	tagPtr  uint64 = 0xFFFF000000000000 // sign=1, tag=11

	// Masks.
	tagMask     uint64 = 0xFFFF000000000000 // top 16 bits
	payloadMask uint64 = 0x0000FFFFFFFFFFFF // bottom 48 bits

	// Pre-built special values.
	valNil   uint64 = tagNil
	valFalse uint64 = tagBool          // payload = 0
	valTrue  uint64 = tagBool | 1      // payload = 1

	// Canonical NaN (Go/IEEE 754 standard quiet NaN). Bit 50 is 0, so it
	// does NOT collide with our tagged space (which requires bit 50 = 1).
	canonicalNaN uint64 = 0x7FF8000000000000

	// Int48 range limits.
	maxInt48 int64 = (1 << 47) - 1  //  140_737_488_355_327
	minInt48 int64 = -(1 << 47)     // -140_737_488_355_328
)

// -------------------------------------------------------------------------
// Pre-built Value constants
// -------------------------------------------------------------------------

var (
	// Nil is the singleton nil Value.
	Nil = Value(valNil)
	// True is the boolean true Value.
	True = Value(valTrue)
	// False is the boolean false Value.
	False = Value(valFalse)
)

// -------------------------------------------------------------------------
// Constructors
// -------------------------------------------------------------------------

// FromFloat64 packs a float64 into a Value.
// NaN inputs are canonicalized so they never collide with the tag space.
func FromFloat64(f float64) Value {
	bits := math.Float64bits(f)
	// If the bit pattern has bits 50-62 all set, it falls inside our tag
	// space. The only float64 values with that property are exotic NaN
	// patterns. Canonicalize them to Go's standard quiet NaN.
	if bits&nanBits == nanBits {
		return Value(canonicalNaN)
	}
	return Value(bits)
}

// FromInt packs a signed integer into a 48-bit NaN-boxed Value.
// If the value overflows the 48-bit range, it is promoted to float64.
func FromInt(i int64) Value {
	if i > maxInt48 || i < minInt48 {
		// Overflow: promote to float64 (matches LuaJIT semantics).
		return FromFloat64(float64(i))
	}
	return Value(tagInt | (uint64(i) & payloadMask))
}

// FromBool packs a boolean into a Value.
func FromBool(b bool) Value {
	if b {
		return True
	}
	return False
}

// FromNil returns the nil Value.
func FromNil() Value {
	return Nil
}

// FromPointer packs an unsafe.Pointer into a Value.
// The pointer must fit in 48 bits (which is true on all current platforms).
func FromPointer(p unsafe.Pointer) Value {
	return Value(tagPtr | (uint64(uintptr(p)) & payloadMask))
}

// -------------------------------------------------------------------------
// Type checks
// -------------------------------------------------------------------------

// IsFloat reports whether the Value holds a float64.
func (v Value) IsFloat() bool {
	// A value is a float if bits 50-62 are NOT all set.
	// All tagged (non-float) values have these bits set.
	return uint64(v)&nanBits != nanBits
}

// IsInt reports whether the Value holds a 48-bit integer.
func (v Value) IsInt() bool {
	return uint64(v)&tagMask == tagInt
}

// IsBool reports whether the Value holds a boolean.
func (v Value) IsBool() bool {
	return uint64(v)&tagMask == tagBool
}

// IsNil reports whether the Value is nil.
func (v Value) IsNil() bool {
	return uint64(v) == valNil
}

// IsPointer reports whether the Value holds a pointer.
func (v Value) IsPointer() bool {
	return uint64(v)&tagMask == tagPtr
}

// IsNumber reports whether the Value holds a float64 or an integer.
func (v Value) IsNumber() bool {
	return v.IsFloat() || v.IsInt()
}

// Truthy reports whether the Value is truthy (not nil and not false).
func (v Value) Truthy() bool {
	return uint64(v) != valNil && uint64(v) != valFalse
}

// -------------------------------------------------------------------------
// Value extraction
// -------------------------------------------------------------------------

// ToFloat64 extracts the float64 from a float Value.
// The caller must ensure IsFloat() is true.
func (v Value) ToFloat64() float64 {
	return math.Float64frombits(uint64(v))
}

// ToInt extracts the int64 from an integer Value.
// The 48-bit payload is sign-extended to 64 bits.
// The caller must ensure IsInt() is true.
func (v Value) ToInt() int64 {
	raw := uint64(v) & payloadMask
	// Sign-extend: if bit 47 is set, fill top 16 bits with 1s.
	if raw&(1<<47) != 0 {
		return int64(raw | 0xFFFF000000000000)
	}
	return int64(raw)
}

// ToBool extracts the boolean from a bool Value.
// The caller must ensure IsBool() is true.
func (v Value) ToBool() bool {
	return uint64(v)&1 != 0
}

// ToPointer extracts the unsafe.Pointer from a pointer Value.
// The caller must ensure IsPointer() is true.
func (v Value) ToPointer() unsafe.Pointer {
	return unsafe.Pointer(uintptr(uint64(v) & payloadMask))
}

// -------------------------------------------------------------------------
// Number coercion
// -------------------------------------------------------------------------

// ToNumber converts a numeric Value (int or float) to float64.
// The caller must ensure IsNumber() is true.
func (v Value) ToNumber() float64 {
	if v.IsFloat() {
		return v.ToFloat64()
	}
	return float64(v.ToInt())
}

// -------------------------------------------------------------------------
// Stringer (debugging)
// -------------------------------------------------------------------------

// String returns a human-readable representation for debugging.
func (v Value) String() string {
	switch {
	case v.IsNil():
		return "nil"
	case v.IsBool():
		if v.ToBool() {
			return "true"
		}
		return "false"
	case v.IsInt():
		return fmt.Sprintf("int(%d)", v.ToInt())
	case v.IsPointer():
		return fmt.Sprintf("ptr(%p)", v.ToPointer())
	case v.IsFloat():
		return fmt.Sprintf("float(%g)", v.ToFloat64())
	default:
		return fmt.Sprintf("unknown(0x%016X)", uint64(v))
	}
}

// -------------------------------------------------------------------------
// Raw access (for VM / JIT)
// -------------------------------------------------------------------------

// Raw returns the underlying uint64 bits.
func (v Value) Raw() uint64 {
	return uint64(v)
}

// FromRaw constructs a Value from raw uint64 bits (no validation).
func FromRaw(bits uint64) Value {
	return Value(bits)
}
