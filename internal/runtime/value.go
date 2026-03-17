package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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

// Value is a compact tagged-union representation of all GScript values.
// 32 bytes: {typ(8) + data(8) + ptr(16)}.
//
// Storage layout:
//   Nil:       {TypeNil,   0,              nil}
//   Bool:      {TypeBool,  0 or 1,         nil}
//   Int:       {TypeInt,   uint64(i),      nil}
//   Float:     {TypeFloat, Float64bits(f), nil}
//   String:    {TypeString,0,              string(s)}
//   Table:     {TypeTable, 0,              *Table}
//   Function:  {TypeFunction,0,            *Closure/*GoFunction}
//   Coroutine: {TypeCoroutine,0,           any}
//   Channel:   {TypeChannel,0,             *Channel}
type Value struct {
	typ  ValueType
	data uint64 // Int/Bool payload, or Float64bits for float
	ptr  any    // *Table | *Closure | *GoFunction | *Coroutine | *Channel | string
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
	return Value{typ: TypeString, ptr: s}
}

func TableValue(t *Table) Value {
	return Value{typ: TypeTable, ptr: t}
}

func FunctionValue(f interface{}) Value {
	return Value{typ: TypeFunction, ptr: f}
}

func CoroutineValue(c *Coroutine) Value {
	return Value{typ: TypeCoroutine, ptr: c}
}

func AnyCoroutineValue(c any) Value {
	return Value{typ: TypeCoroutine, ptr: c}
}

func ChannelValue(ch *Channel) Value {
	return Value{typ: TypeChannel, ptr: ch}
}

// ---------------------------------------------------------------------------
// In-place mutation (hot-loop optimization)
// ---------------------------------------------------------------------------

// SetInt updates a Value to an integer in place.
// NOTE: ptr field may be stale. Table operations use cleanHashKey to normalize.
func (v *Value) SetInt(i int64) {
	v.typ = TypeInt
	v.data = uint64(i)
}

// ---------------------------------------------------------------------------
// Pointer-receiver fast paths (avoid 32-byte Value copies in VM hot loop)
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
	return v.ptr.(string)
}

func (v Value) Table() *Table {
	if v.ptr == nil {
		return nil
	}
	return v.ptr.(*Table)
}

func (v Value) Closure() *Closure {
	if v.ptr == nil {
		return nil
	}
	c, _ := v.ptr.(*Closure)
	return c
}

func (v Value) GoFunction() *GoFunction {
	if v.ptr == nil {
		return nil
	}
	gf, _ := v.ptr.(*GoFunction)
	return gf
}

func (v Value) Ptr() any {
	return v.ptr
}

func (v Value) Coroutine() *Coroutine {
	if v.ptr == nil {
		return nil
	}
	return v.ptr.(*Coroutine)
}

func (v Value) Channel() *Channel {
	if v.ptr == nil {
		return nil
	}
	return v.ptr.(*Channel)
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
		return v.ptr.(string) == other.ptr.(string)
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
	s := strings.TrimSpace(v.ptr.(string))
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
		return v.ptr.(string)
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
		return v.ptr.(string) < other.ptr.(string), true
	}
	return false, false
}

func floatIsInt(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	return f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64
}
