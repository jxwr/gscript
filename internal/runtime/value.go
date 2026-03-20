package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"
)

// ValueType represents the type of a GScript value.
type ValueType uint8

const (
	TypeNil       ValueType = iota
	TypeBool                // boolean
	TypeInt                 // integer numbers
	TypeFloat               // floating-point numbers
	TypeString              // strings
	TypeTable               // tables (associative arrays)
	TypeFunction            // functions (closures and Go functions)
	TypeCoroutine           // coroutines
	TypeChannel             // channels
)

// iface is the memory layout of a Go interface{}/any value.
// Used to pack/unpack interface values into the compact Value representation.
type iface struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// Value is a compact tagged-union representation of all GScript values.
// 24 bytes: {typ(1+7pad) + data(8) + ptr(8)}.
//
// Storage layout:
//   Nil:       {TypeNil,   0,              nil}
//   Bool:      {TypeBool,  0 or 1,         nil}
//   Int:       {TypeInt,   uint64(i),      nil}
//   Float:     {TypeFloat, Float64bits(f), nil}
//   String:    {TypeString,0,              *string}
//   Table:     {TypeTable, 0,              *Table}
//   Function:  {TypeFunction,ifaceTyp,     ifaceData} — stores iface type ptr in data for Ptr() reconstruction
//   Coroutine: {TypeCoroutine,ifaceTyp,    ifaceData} — same as Function for AnyCoroutineValue
//   Channel:   {TypeChannel,0,             *Channel}
//
// The ptr field is unsafe.Pointer (8 bytes) instead of any (16 bytes),
// saving 8 bytes per Value compared to the old interface-based layout.
type Value struct {
	typ  ValueType       // offset 0: type tag (1 byte + 7 padding)
	data uint64          // offset 8: int/float/bool payload, or iface type ptr for Function/Coroutine
	ptr  unsafe.Pointer  // offset 16: GC-visible pointer (nil for scalars)
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

func NilValue() Value {
	return Value{typ: TypeNil}
}

func BoolValue(b bool) Value {
	var d uint64
	if b {
		d = 1
	}
	return Value{typ: TypeBool, data: d}
}

func IntValue(i int64) Value {
	return Value{typ: TypeInt, data: uint64(i)}
}

func FloatValue(f float64) Value {
	return Value{typ: TypeFloat, data: math.Float64bits(f)}
}

func StringValue(s string) Value {
	sp := new(string)
	*sp = s
	return Value{typ: TypeString, ptr: unsafe.Pointer(sp)}
}

func TableValue(t *Table) Value {
	return Value{typ: TypeTable, ptr: unsafe.Pointer(t)}
}

// FunctionValue stores a function value (either *Closure or *GoFunction or any
// other pointer type). The interface type pointer is stored in data so
// that Ptr() can reconstruct the original interface for type assertions.
func FunctionValue(f interface{}) Value {
	i := (*iface)(unsafe.Pointer(&f))
	return Value{
		typ:  TypeFunction,
		data: uint64(uintptr(i.typ)),
		ptr:  i.data,
	}
}

func CoroutineValue(c *Coroutine) Value {
	return Value{typ: TypeCoroutine, ptr: unsafe.Pointer(c)}
}

// AnyCoroutineValue stores a coroutine value from an arbitrary pointer type
// (e.g. *VMCoroutine from the vm package). Like FunctionValue, it preserves
// the interface type pointer for Ptr() reconstruction.
func AnyCoroutineValue(c any) Value {
	i := (*iface)(unsafe.Pointer(&c))
	return Value{
		typ:  TypeCoroutine,
		data: uint64(uintptr(i.typ)),
		ptr:  i.data,
	}
}

func ChannelValue(ch *Channel) Value {
	return Value{typ: TypeChannel, ptr: unsafe.Pointer(ch)}
}

// ---------------------------------------------------------------------------
// In-place mutation (hot-loop optimization)
// ---------------------------------------------------------------------------

// SetInt updates a Value to an integer in place.
func (v *Value) SetInt(i int64) {
	v.typ = TypeInt
	v.data = uint64(i)
}

// ---------------------------------------------------------------------------
// Pointer-receiver fast paths (avoid 24-byte Value copies in VM hot loop)
// ---------------------------------------------------------------------------

func (v *Value) RawType() ValueType { return v.typ }
func (v *Value) RawInt() int64      { return int64(v.data) }
func (v *Value) RawFloat() float64  { return math.Float64frombits(v.data) }

func AddInts(dst, a, b *Value) bool {
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeInt
		dst.data = uint64(int64(a.data) + int64(b.data))
		return true
	}
	return false
}

// AddNums tries to add *a + *b as numbers (int or float), storing result in *dst.
// Returns true on success. Handles int+int, float+float, and int+float promotions.
func AddNums(dst, a, b *Value) bool {
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeInt
		dst.data = uint64(int64(a.data) + int64(b.data))
		return true
	}
	if a.typ <= TypeFloat && b.typ <= TypeFloat && a.typ >= TypeInt && b.typ >= TypeInt {
		dst.typ = TypeFloat
		dst.data = math.Float64bits(a.Number() + b.Number())
		return true
	}
	return false
}

func SubInts(dst, a, b *Value) bool {
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeInt
		dst.data = uint64(int64(a.data) - int64(b.data))
		return true
	}
	return false
}

func SubNums(dst, a, b *Value) bool {
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeInt
		dst.data = uint64(int64(a.data) - int64(b.data))
		return true
	}
	if a.typ <= TypeFloat && b.typ <= TypeFloat && a.typ >= TypeInt && b.typ >= TypeInt {
		dst.typ = TypeFloat
		dst.data = math.Float64bits(a.Number() - b.Number())
		return true
	}
	return false
}

func MulInts(dst, a, b *Value) bool {
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeInt
		dst.data = uint64(int64(a.data) * int64(b.data))
		return true
	}
	return false
}

func MulNums(dst, a, b *Value) bool {
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeInt
		dst.data = uint64(int64(a.data) * int64(b.data))
		return true
	}
	if a.typ <= TypeFloat && b.typ <= TypeFloat && a.typ >= TypeInt && b.typ >= TypeInt {
		dst.typ = TypeFloat
		dst.data = math.Float64bits(a.Number() * b.Number())
		return true
	}
	return false
}

func DivNums(dst, a, b *Value) bool {
	// DIV always returns float in Lua/GScript semantics (5/2 = 2.5).
	// Division by zero produces +Inf or -Inf (standard IEEE 754 behavior).
	if a.typ == TypeInt && b.typ == TypeInt {
		dst.typ = TypeFloat
		dst.data = math.Float64bits(float64(int64(a.data)) / float64(int64(b.data)))
		return true
	}
	if a.typ <= TypeFloat && b.typ <= TypeFloat && a.typ >= TypeInt && b.typ >= TypeInt {
		dst.typ = TypeFloat
		dst.data = math.Float64bits(a.Number() / b.Number())
		return true
	}
	return false
}

func LTInts(a, b *Value) (bool, bool) {
	if a.typ == TypeInt && b.typ == TypeInt {
		return int64(a.data) < int64(b.data), true
	}
	return false, false
}

func LEInts(a, b *Value) (bool, bool) {
	if a.typ == TypeInt && b.typ == TypeInt {
		return int64(a.data) <= int64(b.data), true
	}
	return false, false
}

// ---------------------------------------------------------------------------
// Type checks
// ---------------------------------------------------------------------------

func (v Value) Type() ValueType    { return v.typ }
func (v Value) IsNil() bool        { return v.typ == TypeNil }
func (v Value) IsBool() bool       { return v.typ == TypeBool }
func (v Value) IsNumber() bool     { return v.typ == TypeInt || v.typ == TypeFloat }
func (v Value) IsInt() bool        { return v.typ == TypeInt }
func (v Value) IsFloat() bool      { return v.typ == TypeFloat }
func (v Value) IsString() bool     { return v.typ == TypeString }
func (v Value) IsTable() bool      { return v.typ == TypeTable }
func (v Value) IsFunction() bool   { return v.typ == TypeFunction }
func (v Value) IsCoroutine() bool  { return v.typ == TypeCoroutine }
func (v Value) IsChannel() bool    { return v.typ == TypeChannel }

// ---------------------------------------------------------------------------
// Value accessors
// ---------------------------------------------------------------------------

func (v Value) Bool() bool      { return v.data != 0 }
func (v Value) Int() int64      { return int64(v.data) }
func (v Value) Float() float64  { return math.Float64frombits(v.data) }

func (v Value) Number() float64 {
	if v.typ == TypeInt {
		return float64(int64(v.data))
	}
	return math.Float64frombits(v.data)
}

func (v Value) Str() string {
	if v.ptr == nil {
		return ""
	}
	return *(*string)(v.ptr)
}

func (v Value) Table() *Table {
	if v.ptr == nil {
		return nil
	}
	return (*Table)(v.ptr)
}

// Closure returns the value as *runtime.Closure, or nil if the underlying
// pointer is not a *runtime.Closure. Uses Ptr() to reconstruct the interface
// for a safe type assertion.
func (v Value) Closure() *Closure {
	if v.ptr == nil {
		return nil
	}
	c, _ := v.Ptr().(*Closure)
	return c
}

// GoFunction returns the value as *GoFunction, or nil.
func (v Value) GoFunction() *GoFunction {
	if v.ptr == nil {
		return nil
	}
	gf, _ := v.Ptr().(*GoFunction)
	return gf
}

// Ptr reconstructs the original interface{} value from the stored type pointer
// and data pointer. For Function and Coroutine types that were created via
// FunctionValue()/AnyCoroutineValue(), the interface type pointer is stored in
// the data field. For other pointer types, it returns a typed pointer.
func (v Value) Ptr() any {
	switch v.typ {
	case TypeFunction:
		// Reconstruct the original interface from stored type+data pointers
		typPtr := unsafe.Pointer(uintptr(v.data))
		if typPtr == nil {
			return nil
		}
		i := iface{typ: typPtr, data: v.ptr}
		return *(*any)(unsafe.Pointer(&i))
	case TypeCoroutine:
		if v.data != 0 {
			// Created via AnyCoroutineValue — reconstruct interface
			typPtr := unsafe.Pointer(uintptr(v.data))
			i := iface{typ: typPtr, data: v.ptr}
			return *(*any)(unsafe.Pointer(&i))
		}
		// Created via CoroutineValue(*Coroutine) — return typed pointer
		return (*Coroutine)(v.ptr)
	case TypeTable:
		return (*Table)(v.ptr)
	case TypeString:
		if v.ptr == nil {
			return ""
		}
		return *(*string)(v.ptr)
	case TypeChannel:
		return (*Channel)(v.ptr)
	default:
		return nil
	}
}

func (v Value) Coroutine() *Coroutine {
	if v.ptr == nil {
		return nil
	}
	// If created via CoroutineValue(*Coroutine), data is 0 (no iface type ptr).
	if v.data == 0 {
		return (*Coroutine)(v.ptr)
	}
	// If created via AnyCoroutineValue, reconstruct and type-assert.
	c, _ := v.Ptr().(*Coroutine)
	return c
}

func (v Value) Channel() *Channel {
	if v.ptr == nil {
		return nil
	}
	return (*Channel)(v.ptr)
}

// ---------------------------------------------------------------------------
// TypeName, Truthiness, Equality
// ---------------------------------------------------------------------------

func (v Value) TypeName() string {
	switch v.typ {
	case TypeNil:
		return "nil"
	case TypeBool:
		return "boolean"
	case TypeInt, TypeFloat:
		return "number"
	case TypeString:
		return "string"
	case TypeTable:
		return "table"
	case TypeFunction:
		return "function"
	case TypeCoroutine:
		return "coroutine"
	case TypeChannel:
		return "channel"
	default:
		return "unknown"
	}
}

func (v Value) Truthy() bool {
	switch v.typ {
	case TypeNil:
		return false
	case TypeBool:
		return v.data != 0
	default:
		return true
	}
}

func (v Value) Equal(other Value) bool {
	if v.typ != other.typ {
		if v.IsNumber() && other.IsNumber() {
			return v.Number() == other.Number()
		}
		return false
	}
	switch v.typ {
	case TypeNil:
		return true
	case TypeBool, TypeInt:
		return v.data == other.data
	case TypeFloat:
		return math.Float64frombits(v.data) == math.Float64frombits(other.data)
	case TypeString:
		return *(*string)(v.ptr) == *(*string)(other.ptr)
	case TypeTable, TypeFunction, TypeCoroutine, TypeChannel:
		return v.ptr == other.ptr
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Arithmetic / conversion helpers
// ---------------------------------------------------------------------------

func (v Value) ToNumber() (Value, bool) {
	if v.IsInt() || v.IsFloat() {
		return v, true
	}
	if v.typ != TypeString {
		return NilValue(), false
	}
	s := strings.TrimSpace(*(*string)(v.ptr))
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return IntValue(i), true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return FloatValue(f), true
	}
	return NilValue(), false
}

// ---------------------------------------------------------------------------
// fmt.Stringer
// ---------------------------------------------------------------------------

func (v Value) String() string {
	switch v.typ {
	case TypeNil:
		return "nil"
	case TypeBool:
		if v.data != 0 {
			return "true"
		}
		return "false"
	case TypeInt:
		return strconv.FormatInt(int64(v.data), 10)
	case TypeFloat:
		f := math.Float64frombits(v.data)
		s := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.Contains(s, ".") && !strings.Contains(s, "e") && !strings.Contains(s, "E") && !strings.Contains(s, "Inf") && !strings.Contains(s, "NaN") {
			s += ".0"
		}
		return s
	case TypeString:
		return *(*string)(v.ptr)
	case TypeTable:
		return fmt.Sprintf("table: %p", v.ptr)
	case TypeFunction:
		if c := v.Closure(); c != nil {
			return fmt.Sprintf("function: %p", c)
		}
		if gf := v.GoFunction(); gf != nil {
			return fmt.Sprintf("function: %s", gf.Name)
		}
		return "function: <unknown>"
	case TypeCoroutine:
		return fmt.Sprintf("coroutine: %p", v.ptr)
	case TypeChannel:
		return fmt.Sprintf("channel: %p", v.ptr)
	default:
		return "unknown"
	}
}

func (v Value) hashKey() Value {
	return v
}

func (v Value) LessThan(other Value) (bool, bool) {
	if v.IsNumber() && other.IsNumber() {
		return v.Number() < other.Number(), true
	}
	if v.typ == TypeString && other.typ == TypeString {
		return *(*string)(v.ptr) < *(*string)(other.ptr), true
	}
	return false, false
}

func floatIsInt(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	return f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64
}
