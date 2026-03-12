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
	TypeCoroutine           // coroutines (Phase 6)
)

// Value is the tagged-union representation of all GScript values.
// It is designed to be passed by value (no pointer indirection for scalars).
type Value struct {
	typ  ValueType
	ival int64   // Bool (0/1) or Int
	fval float64 // Float
	sval string  // String
	ptr  any     // *Table | *Closure | *GoFunction | *Coroutine
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// NilValue returns the nil value.
func NilValue() Value {
	return Value{typ: TypeNil}
}

// BoolValue returns a boolean value.
func BoolValue(b bool) Value {
	var iv int64
	if b {
		iv = 1
	}
	return Value{typ: TypeBool, ival: iv}
}

// IntValue returns an integer value.
func IntValue(i int64) Value {
	return Value{typ: TypeInt, ival: i}
}

// FloatValue returns a floating-point value.
func FloatValue(f float64) Value {
	return Value{typ: TypeFloat, fval: f}
}

// StringValue returns a string value.
func StringValue(s string) Value {
	return Value{typ: TypeString, sval: s}
}

// TableValue returns a table value.
func TableValue(t *Table) Value {
	return Value{typ: TypeTable, ptr: t}
}

// FunctionValue returns a function value wrapping either a *Closure or *GoFunction.
func FunctionValue(f interface{}) Value {
	return Value{typ: TypeFunction, ptr: f}
}

// CoroutineValue returns a coroutine value.
func CoroutineValue(c *Coroutine) Value {
	return Value{typ: TypeCoroutine, ptr: c}
}

// ---------------------------------------------------------------------------
// Type checks
// ---------------------------------------------------------------------------

// Type returns the ValueType tag.
func (v Value) Type() ValueType { return v.typ }

// IsNil returns true if the value is nil.
func (v Value) IsNil() bool { return v.typ == TypeNil }

// IsBool returns true if the value is a boolean.
func (v Value) IsBool() bool { return v.typ == TypeBool }

// IsNumber returns true if the value is an integer or float.
func (v Value) IsNumber() bool { return v.typ == TypeInt || v.typ == TypeFloat }

// IsInt returns true if the value is an integer.
func (v Value) IsInt() bool { return v.typ == TypeInt }

// IsFloat returns true if the value is a float.
func (v Value) IsFloat() bool { return v.typ == TypeFloat }

// IsString returns true if the value is a string.
func (v Value) IsString() bool { return v.typ == TypeString }

// IsTable returns true if the value is a table.
func (v Value) IsTable() bool { return v.typ == TypeTable }

// IsFunction returns true if the value is a function (closure or Go function).
func (v Value) IsFunction() bool { return v.typ == TypeFunction }

// IsCoroutine returns true if the value is a coroutine.
func (v Value) IsCoroutine() bool { return v.typ == TypeCoroutine }

// ---------------------------------------------------------------------------
// Value accessors
// ---------------------------------------------------------------------------

// Bool returns the boolean payload. Panics if not TypeBool.
func (v Value) Bool() bool { return v.ival != 0 }

// Int returns the integer payload. Panics if not TypeInt.
func (v Value) Int() int64 { return v.ival }

// Float returns the float payload. Panics if not TypeFloat.
func (v Value) Float() float64 { return v.fval }

// Number converts an int or float to float64.
func (v Value) Number() float64 {
	if v.typ == TypeInt {
		return float64(v.ival)
	}
	return v.fval
}

// Str returns the raw string payload (named to avoid conflict with String()/Stringer).
func (v Value) Str() string { return v.sval }

// Table returns the *Table pointer.
func (v Value) Table() *Table {
	if v.ptr == nil {
		return nil
	}
	return v.ptr.(*Table)
}

// Closure returns the *Closure pointer, or nil if not a closure.
func (v Value) Closure() *Closure {
	if v.ptr == nil {
		return nil
	}
	c, _ := v.ptr.(*Closure)
	return c
}

// GoFunction returns the *GoFunction pointer, or nil if not a Go function.
func (v Value) GoFunction() *GoFunction {
	if v.ptr == nil {
		return nil
	}
	gf, _ := v.ptr.(*GoFunction)
	return gf
}

// Ptr returns the raw pointer field. Used by the VM package for type assertions.
func (v Value) Ptr() any {
	return v.ptr
}

// Coroutine returns the *Coroutine pointer.
func (v Value) Coroutine() *Coroutine {
	if v.ptr == nil {
		return nil
	}
	return v.ptr.(*Coroutine)
}

// ---------------------------------------------------------------------------
// TypeName, Truthiness, Equality
// ---------------------------------------------------------------------------

// TypeName returns a Lua-style type name string.
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
	default:
		return "unknown"
	}
}

// Truthy returns the truthiness of a value.
// false and nil are falsy; everything else is truthy.
func (v Value) Truthy() bool {
	switch v.typ {
	case TypeNil:
		return false
	case TypeBool:
		return v.ival != 0
	default:
		return true
	}
}

// Equal tests structural equality between two values.
func (v Value) Equal(other Value) bool {
	if v.typ != other.typ {
		// int == float comparison
		if v.IsNumber() && other.IsNumber() {
			return v.Number() == other.Number()
		}
		return false
	}
	switch v.typ {
	case TypeNil:
		return true
	case TypeBool:
		return v.ival == other.ival
	case TypeInt:
		return v.ival == other.ival
	case TypeFloat:
		return v.fval == other.fval
	case TypeString:
		return v.sval == other.sval
	case TypeTable, TypeFunction, TypeCoroutine:
		return v.ptr == other.ptr // reference equality
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Arithmetic / conversion helpers
// ---------------------------------------------------------------------------

// ToNumber attempts to convert a string value to a number.
// Returns (converted value, true) on success, or (NilValue(), false) on failure.
func (v Value) ToNumber() (Value, bool) {
	if v.IsInt() || v.IsFloat() {
		return v, true
	}
	if v.typ != TypeString {
		return NilValue(), false
	}
	s := strings.TrimSpace(v.sval)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return IntValue(i), true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return FloatValue(f), true
	}
	return NilValue(), false
}

// ---------------------------------------------------------------------------
// fmt.Stringer (human-readable representation for debugging)
// ---------------------------------------------------------------------------

// String implements fmt.Stringer. Returns a human-readable representation.
func (v Value) String() string {
	switch v.typ {
	case TypeNil:
		return "nil"
	case TypeBool:
		if v.ival != 0 {
			return "true"
		}
		return "false"
	case TypeInt:
		return strconv.FormatInt(v.ival, 10)
	case TypeFloat:
		s := strconv.FormatFloat(v.fval, 'g', -1, 64)
		// Ensure there's always a decimal point so it looks like a float.
		if !strings.Contains(s, ".") && !strings.Contains(s, "e") && !strings.Contains(s, "E") && !strings.Contains(s, "Inf") && !strings.Contains(s, "NaN") {
			s += ".0"
		}
		return s
	case TypeString:
		return v.sval
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
	default:
		return "unknown"
	}
}

// Hashable returns a representation usable as a map key. Value is already
// comparable for basic types. For table/function/coroutine it uses pointer identity.
// This is used internally by Table.
func (v Value) hashKey() Value {
	return v
}

// LessThan compares two values for ordering (used by < <= > >=).
// Returns (result, ok). ok is false if the types are not comparable.
func (v Value) LessThan(other Value) (bool, bool) {
	if v.IsNumber() && other.IsNumber() {
		return v.Number() < other.Number(), true
	}
	if v.typ == TypeString && other.typ == TypeString {
		return v.sval < other.sval, true
	}
	return false, false
}

// floatIsInt returns true if a float64 is an exact integer and within int64 range.
func floatIsInt(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	return f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64
}

// Coroutine is defined in coroutine.go.
