package gscript

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

// VM is a GScript virtual machine instance.
// A VM is NOT goroutine-safe; use Pool for concurrent access.
type VM struct {
	interp *runtime.Interpreter
	opts   vmOptions
	bvm    *bytecodevm.VM // persisted bytecode VM for Call routing (nil if tree-walker mode)
}

// New creates a new GScript VM with the given options.
func New(opts ...Option) *VM {
	o := vmOptions{
		libs: LibAll,
	}
	for _, opt := range opts {
		opt(&o)
	}

	interp := runtime.New()

	// Override print if requested
	if o.printFunc != nil {
		interp.SetGlobal("print", runtime.FunctionValue(&runtime.GoFunction{
			Name: "print",
			Fn: func(args []runtime.Value) ([]runtime.Value, error) {
				iArgs := make([]interface{}, len(args))
				for i, a := range args {
					iArgs[i] = a.String()
				}
				o.printFunc(iArgs...)
				return nil, nil
			},
		}))
	}

	if o.requirePath != "" {
		interp.SetScriptDir(o.requirePath)
	}

	return &VM{interp: interp, opts: o}
}

// Exec compiles and executes a GScript source string.
func (vm *VM) Exec(src string) error {
	return vm.exec(src, "<string>")
}

// ExecFile reads and executes a GScript source file.
func (vm *VM) ExecFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return &Error{Kind: ErrRuntime, Message: err.Error(), File: path}
	}
	abs, _ := filepath.Abs(path)
	vm.interp.SetScriptDir(filepath.Dir(abs))
	return vm.exec(string(src), path)
}

func (vm *VM) exec(src, filename string) error {
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return &Error{Kind: ErrLex, Message: err.Error(), File: filename}
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		return &Error{Kind: ErrParse, Message: err.Error(), File: filename}
	}

	if vm.opts.useVM {
		// Bytecode VM path
		proto, err := bytecodevm.Compile(prog)
		if err != nil {
			return &Error{Kind: ErrRuntime, Message: err.Error(), File: filename}
		}
		globals := vm.interp.ExportGlobals()
		// Reuse existing bytecode VM if available (preserves JIT state)
		bvm := vm.bvm
		if bvm == nil {
			bvm = bytecodevm.New(globals)
			bvm.SetStringMeta(vm.interp.StringMeta())
			if vm.opts.useJIT {
				enableJIT(bvm)
			}
		}
		if _, err := bvm.Execute(proto); err != nil {
			return &Error{Kind: ErrRuntime, Message: err.Error(), File: filename}
		}
		// Persist the bytecode VM for future Call routing
		vm.bvm = bvm
		// Sync globals back to interpreter
		for name, val := range globals {
			vm.interp.SetGlobal(name, val)
		}
		return nil
	}

	if err := vm.interp.Exec(prog); err != nil {
		return &Error{Kind: ErrRuntime, Message: err.Error(), File: filename}
	}
	return nil
}

// Call calls a named GScript function with Go arguments and returns Go values.
// Args and return values are automatically converted via reflection.
func (vm *VM) Call(name string, args ...interface{}) ([]interface{}, error) {
	fn := vm.interp.GetGlobal(name)
	if fn.IsNil() {
		return nil, &Error{Kind: ErrRuntime, Message: fmt.Sprintf("function %q not found", name)}
	}
	return vm.callValue(fn, args...)
}

// CallValue calls a GScript function value (obtained via Get) with Go arguments.
func (vm *VM) CallValue(fn interface{}, args ...interface{}) ([]interface{}, error) {
	var gsVal runtime.Value
	if v, ok := fn.(runtime.Value); ok {
		gsVal = v
	} else {
		v2, err := ToValue(fn)
		if err != nil {
			return nil, err
		}
		gsVal = v2
	}
	return vm.callValue(gsVal, args...)
}

func (vm *VM) callValue(fn runtime.Value, args ...interface{}) ([]interface{}, error) {
	gsArgs := make([]runtime.Value, len(args))
	for i, a := range args {
		v, err := ToValue(a)
		if err != nil {
			return nil, &Error{Kind: ErrRuntime, Message: fmt.Sprintf("arg %d: %v", i, err)}
		}
		gsArgs[i] = v
	}

	var results []runtime.Value
	var err error
	// Route through bytecode VM if available (handles bytecode closures correctly)
	if vm.bvm != nil {
		results, err = vm.bvm.CallValue(fn, gsArgs)
	} else {
		results, err = vm.interp.CallFunction(fn, gsArgs)
	}
	if err != nil {
		return nil, &Error{Kind: ErrRuntime, Message: err.Error()}
	}
	out := make([]interface{}, len(results))
	for i, r := range results {
		rv, err := fromValueDefault(r)
		if err != nil || !rv.IsValid() {
			out[i] = nil
		} else {
			out[i] = rv.Interface()
		}
	}
	return out, nil
}

// Set sets a global variable to a Go value (auto-converted).
func (vm *VM) Set(name string, val interface{}) error {
	gsVal, err := ToValue(val)
	if err != nil {
		return err
	}
	vm.interp.SetGlobal(name, gsVal)
	return nil
}

// Get gets a global variable as a Go interface{} (auto-converted).
func (vm *VM) Get(name string) (interface{}, error) {
	gsVal := vm.interp.GetGlobal(name)
	rv, err := fromValueDefault(gsVal)
	if err != nil {
		return nil, err
	}
	if !rv.IsValid() {
		return nil, nil
	}
	return rv.Interface(), nil
}

// GetValue gets a global as a raw runtime.Value.
func (vm *VM) GetValue(name string) runtime.Value {
	return vm.interp.GetGlobal(name)
}

// SetValue sets a global to a raw runtime.Value.
func (vm *VM) SetValue(name string, val runtime.Value) {
	vm.interp.SetGlobal(name, val)
}

// RegisterFunc registers a Go function as a GScript global.
// fn must be a func type. Args/returns are auto-converted via reflection.
//
// Example:
//
//	vm.RegisterFunc("distance", func(x1, y1, x2, y2 float64) float64 {
//	    dx, dy := x2-x1, y2-y1
//	    return math.Sqrt(dx*dx + dy*dy)
//	})
func (vm *VM) RegisterFunc(name string, fn interface{}) error {
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("RegisterFunc: %q must be a func, got %T", name, fn)
	}
	wrapped, err := wrapGoFunc(rv)
	if err != nil {
		return fmt.Errorf("RegisterFunc: %q: %v", name, err)
	}
	wrapped.Name = name
	vm.interp.SetGlobal(name, runtime.FunctionValue(wrapped))
	return nil
}

// RegisterTable registers a table of Go functions as a global namespace.
//
// Example:
//
//	vm.RegisterTable("vec", map[string]interface{}{
//	    "dot":   func(ax,ay,bx,by float64) float64 { return ax*bx + ay*by },
//	    "cross": func(ax,ay,bx,by float64) float64 { return ax*by - ay*bx },
//	})
func (vm *VM) RegisterTable(name string, members map[string]interface{}) error {
	t := runtime.NewTable()
	for k, v := range members {
		gsVal, err := ToValue(v)
		if err != nil {
			return fmt.Errorf("RegisterTable %s.%s: %v", name, k, err)
		}
		t.RawSet(runtime.StringValue(k), gsVal)
	}
	vm.interp.SetGlobal(name, runtime.TableValue(t))
	return nil
}

// BindStruct registers a Go struct type as a GScript class.
// proto should be a zero value or example of the struct (e.g. Vec2{} or &Vec2{}).
//
// This creates a GScript global named `name` with a .new() constructor
// and field/method access via metatable.
//
// Example:
//
//	type Vec2 struct{ X, Y float64 }
//	func (v Vec2) Length() float64 { return math.Sqrt(v.X*v.X + v.Y*v.Y) }
//
//	vm.BindStruct("Vec2", Vec2{})
//
//	// In GScript:
//	v := Vec2.new(3, 4)
//	print(v.Length())  // 5
//	print(v.X)         // 3
//	v.X = 10
func (vm *VM) BindStruct(name string, proto interface{}) error {
	return bindStructToInterp(vm.interp, name, proto, nil)
}

// BindStructWithConstructor is like BindStruct but uses a custom constructor function.
// The constructor is called when GScript calls Name.new(args...).
func (vm *VM) BindStructWithConstructor(name string, proto interface{}, ctor interface{}) error {
	return bindStructToInterp(vm.interp, name, proto, ctor)
}

// BindMethod adds a method to an already-registered struct class.
func (vm *VM) BindMethod(className, methodName string, fn interface{}) error {
	classVal := vm.interp.GetGlobal(className)
	if !classVal.IsTable() {
		return fmt.Errorf("BindMethod: %q is not a registered class", className)
	}
	rv := reflect.ValueOf(fn)
	wrapped, err := wrapGoFunc(rv)
	if err != nil {
		return err
	}
	wrapped.Name = className + "." + methodName
	classVal.Table().RawSet(runtime.StringValue(methodName), runtime.FunctionValue(wrapped))
	return nil
}

// CallFunction exposes the interpreter's CallFunction for advanced use.
// Useful when you have a runtime.Value function and want to call it.
func (vm *VM) CallFunction(fn runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	return vm.interp.CallFunction(fn, args)
}

// Interpreter returns the underlying runtime.Interpreter.
// Use for advanced access; prefer the VM methods when possible.
func (vm *VM) Interpreter() *runtime.Interpreter {
	return vm.interp
}
