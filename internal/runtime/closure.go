package runtime

import (
	"unsafe"

	"github.com/gscript/gscript/internal/ast"
)

// FuncProto holds the parsed function information (shared across all closures
// created from the same source function definition).
type FuncProto struct {
	Params    []string // parameter names
	HasVarArg bool     // whether the last param is vararg
	Body      *ast.BlockStmt
	Name      string // for error messages
}

// Upvalue is a shared mutable reference to a Value.
// When a closure captures a local variable, both the enclosing scope and the
// closure hold a pointer to the same Upvalue, allowing mutations to be visible
// from both sides.
type Upvalue struct {
	val *Value
}

// NewUpvalue creates a new Upvalue pointing at the given value.
func NewUpvalue(v *Value) *Upvalue {
	return &Upvalue{val: v}
}

// Get returns the current value.
func (u *Upvalue) Get() Value { return *u.val }

// Set assigns a new value.
func (u *Upvalue) Set(v Value) { *u.val = v }

// Closure is a function prototype paired with captured upvalues and the
// defining environment.
type Closure struct {
	Proto    *FuncProto
	Upvalues map[string]*Upvalue
	Env      *Environment // the environment where the closure was defined
}

// GoFunction is a native Go function callable from GScript.
type GoFunction struct {
	Name  string
	Fn    func(args []Value) ([]Value, error)
	Fast1 func(args []Value) (Value, error)
	// NativeKind/NativeData let the bytecode VM attach optional direct-dispatch
	// metadata while keeping Fn as the semantic fallback.
	NativeKind uint8
	NativeData unsafe.Pointer
}
