package gscript

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"github.com/gscript/gscript/internal/runtime"
)

// capitalizeFirst returns the string with the first letter uppercased.
// This replaces the deprecated strings.Title.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// StructBinding holds configuration for a bound Go struct type.
type StructBinding struct {
	typeName    string
	typ         reflect.Type
	constructor reflect.Value // optional custom constructor func
}

// bindStructToInterp creates a GScript "class" table for a Go struct type
// and registers it as a global in the interpreter.
//
// Given struct:
//
//	type Vec2 struct { X, Y float64 }
//	func (v Vec2) Length() float64 { ... }
//
// In GScript:
//
//	v := Vec2.new(0, 1)  -- creates new instance
//	print(v.X)           -- field access
//	v.X = 5              -- field set
//	print(v.Length())     -- method call
func bindStructToInterp(interp *runtime.Interpreter, name string, proto interface{}, customCtor interface{}) error {
	var typ reflect.Type
	if proto != nil {
		typ = reflect.TypeOf(proto)
		for typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
	}
	if typ == nil || typ.Kind() != reflect.Struct {
		return fmt.Errorf("BindStruct: proto must be a struct or pointer to struct, got %T", proto)
	}

	// Create the class table
	classTable := runtime.NewTable()

	// Add type name constant
	classTable.RawSet(runtime.StringValue("__type"), runtime.StringValue(name))

	// Create the instance metatable
	// This is shared across all instances of this struct type
	instanceMeta := runtime.NewTable()

	// __index: field access + method dispatch
	instanceMeta.RawSet(runtime.StringValue("__index"), runtime.FunctionValue(&runtime.GoFunction{
		Name: name + ".__index",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 2 {
				return []runtime.Value{runtime.NilValue()}, nil
			}
			self := args[0]
			key := args[1]
			if !key.IsString() {
				return []runtime.Value{runtime.NilValue()}, nil
			}
			fieldName := key.Str()

			// Get the underlying Go struct from userdata table
			goVal := extractGoValue(self)
			if !goVal.IsValid() {
				return []runtime.Value{runtime.NilValue()}, nil
			}

			rv := goVal
			for rv.Kind() == reflect.Ptr {
				rv = rv.Elem()
			}

			// Try exported field (exact match, then capitalized first letter)
			if rv.Kind() == reflect.Struct {
				f := rv.FieldByName(fieldName)
				if !f.IsValid() {
					f = rv.FieldByName(capitalizeFirst(fieldName))
				}
				if f.IsValid() && f.CanInterface() {
					val, err := reflectToValue(f)
					if err != nil {
						return []runtime.Value{runtime.NilValue()}, nil
					}
					return []runtime.Value{val}, nil
				}
			}

			// Try method (value receiver first, then pointer)
			method := goVal.MethodByName(fieldName)
			if !method.IsValid() {
				method = goVal.MethodByName(capitalizeFirst(fieldName))
			}
			if !method.IsValid() && goVal.CanAddr() {
				method = goVal.Addr().MethodByName(fieldName)
				if !method.IsValid() {
					method = goVal.Addr().MethodByName(capitalizeFirst(fieldName))
				}
			}
			if method.IsValid() {
				fn, err := wrapGoFunc(method)
				if err != nil {
					return []runtime.Value{runtime.NilValue()}, nil
				}
				return []runtime.Value{runtime.FunctionValue(fn)}, nil
			}

			return []runtime.Value{runtime.NilValue()}, nil
		},
	}))

	// __newindex: field set
	instanceMeta.RawSet(runtime.StringValue("__newindex"), runtime.FunctionValue(&runtime.GoFunction{
		Name: name + ".__newindex",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 3 {
				return nil, nil
			}
			self := args[0]
			key := args[1]
			val := args[2]
			if !key.IsString() {
				return nil, fmt.Errorf("struct field key must be string, got %s", key.TypeName())
			}
			fieldName := key.Str()

			goVal := extractGoValue(self)
			if !goVal.IsValid() {
				return nil, fmt.Errorf("invalid struct instance")
			}

			rv := goVal
			for rv.Kind() == reflect.Ptr {
				rv = rv.Elem()
			}

			if rv.Kind() != reflect.Struct {
				return nil, fmt.Errorf("not a struct")
			}

			f := rv.FieldByName(fieldName)
			if !f.IsValid() {
				f = rv.FieldByName(capitalizeFirst(fieldName))
			}
			if !f.IsValid() || !f.CanSet() {
				return nil, fmt.Errorf("struct %s has no settable field %q", name, fieldName)
			}

			goFieldVal, err := FromValue(val, f.Type())
			if err != nil {
				return nil, fmt.Errorf("setting field %q: %v", fieldName, err)
			}
			f.Set(goFieldVal)
			return nil, nil
		},
	}))

	// __tostring
	instanceMeta.RawSet(runtime.StringValue("__tostring"), runtime.FunctionValue(&runtime.GoFunction{
		Name: name + ".__tostring",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 1 {
				return []runtime.Value{runtime.StringValue(name + "{}")}, nil
			}
			goVal := extractGoValue(args[0])
			if !goVal.IsValid() {
				return []runtime.Value{runtime.StringValue(name + "{}")}, nil
			}
			return []runtime.Value{runtime.StringValue(fmt.Sprintf("%v", goVal.Interface()))}, nil
		},
	}))

	// Store metatable in class table for use in constructor
	classTable.RawSet(runtime.StringValue("__instanceMeta"), runtime.TableValue(instanceMeta))
	classTable.RawSet(runtime.StringValue("__goType"), runtime.StringValue(typ.String()))

	// .new() constructor
	var newFn *runtime.GoFunction
	if customCtor != nil {
		// Use the custom constructor function
		ctorVal := reflect.ValueOf(customCtor)
		wrappedCtor, err := wrapGoFunc(ctorVal)
		if err != nil {
			return fmt.Errorf("BindStruct: invalid constructor: %v", err)
		}
		newFn = &runtime.GoFunction{
			Name: name + ".new",
			Fn: func(args []runtime.Value) ([]runtime.Value, error) {
				results, err := wrappedCtor.Fn(args)
				if err != nil {
					return nil, err
				}
				if len(results) == 0 {
					return nil, fmt.Errorf("%s.new: constructor returned nothing", name)
				}
				// If it returned a plain Go value, wrap it as struct instance
				result := results[0]
				if result.IsTable() {
					return results, nil
				}
				// Convert struct return to instance table
				rv, err2 := FromValue(result, typ)
				if err2 != nil {
					return results, nil
				}
				instance := makeStructInstance(rv, instanceMeta)
				return []runtime.Value{instance}, nil
			},
		}
	} else {
		// Auto-constructor: Vec2.new(x, y) fills fields in order
		fields := make([]reflect.StructField, 0)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.IsExported() {
				fields = append(fields, f)
			}
		}
		capturedMeta := instanceMeta
		capturedTyp := typ
		newFn = &runtime.GoFunction{
			Name: name + ".new",
			Fn: func(args []runtime.Value) ([]runtime.Value, error) {
				instance := reflect.New(capturedTyp).Elem()
				for i, f := range fields {
					if i >= len(args) {
						break
					}
					goVal, err := FromValue(args[i], f.Type)
					if err != nil {
						return nil, fmt.Errorf("%s.new field %s: %v", name, f.Name, err)
					}
					instance.FieldByName(f.Name).Set(goVal)
				}
				result := makeStructInstance(instance, capturedMeta)
				return []runtime.Value{result}, nil
			},
		}
	}
	classTable.RawSet(runtime.StringValue("new"), runtime.FunctionValue(newFn))

	// Register static methods (exported methods on pointer type)
	ptrType := reflect.PointerTo(typ)
	for i := 0; i < ptrType.NumMethod(); i++ {
		m := ptrType.Method(i)
		if !m.IsExported() {
			continue
		}
		methodName := m.Name
		// skip if already added (new, etc.)
		existing := classTable.RawGet(runtime.StringValue(methodName))
		if !existing.IsNil() {
			continue
		}

		// Create a static wrapper (takes instance as first arg)
		capturedMethod := m
		fn := &runtime.GoFunction{
			Name: name + "." + methodName,
			Fn: func(args []runtime.Value) ([]runtime.Value, error) {
				if len(args) < 1 {
					return nil, fmt.Errorf("%s.%s: missing self argument", name, capturedMethod.Name)
				}
				goVal := extractGoValue(args[0])
				if !goVal.IsValid() {
					return nil, fmt.Errorf("%s.%s: invalid self", name, capturedMethod.Name)
				}
				// Get pointer for pointer receiver methods
				var receiver reflect.Value
				if goVal.Kind() != reflect.Ptr {
					ptr := reflect.New(goVal.Type())
					ptr.Elem().Set(goVal)
					receiver = ptr
				} else {
					receiver = goVal
				}
				method := receiver.MethodByName(capturedMethod.Name)
				if !method.IsValid() {
					return nil, fmt.Errorf("%s.%s: method not found", name, capturedMethod.Name)
				}
				wrapped, err := wrapGoFunc(method)
				if err != nil {
					return nil, err
				}
				return wrapped.Fn(args[1:])
			},
		}
		classTable.RawSet(runtime.StringValue(methodName), runtime.FunctionValue(fn))
	}

	interp.SetGlobal(name, runtime.TableValue(classTable))
	return nil
}

// makeStructInstance creates a GScript table wrapping a Go struct value.
// The table uses instanceMeta for field/method access via __index/__newindex.
func makeStructInstance(rv reflect.Value, instanceMeta *runtime.Table) runtime.Value {
	t := runtime.NewTable()
	// Store the reflect.Value in a global registry keyed by the table pointer.
	// This is our "userdata" implementation.
	storeGoValue(t, rv)
	t.SetMetatable(instanceMeta)
	return runtime.TableValue(t)
}

// structToValue converts a Go struct reflect.Value into a GScript table instance.
// This is used when returning Go structs from wrapped functions.
func structToValue(rv reflect.Value) (runtime.Value, error) {
	// Create a simple instance meta with __index for field access
	typ := rv.Type()
	name := typ.Name()

	instanceMeta := runtime.NewTable()

	// __index: field and method access
	instanceMeta.RawSet(runtime.StringValue("__index"), runtime.FunctionValue(&runtime.GoFunction{
		Name: name + ".__index",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 2 {
				return []runtime.Value{runtime.NilValue()}, nil
			}
			self := args[0]
			key := args[1]
			if !key.IsString() {
				return []runtime.Value{runtime.NilValue()}, nil
			}
			fieldName := key.Str()

			goVal := extractGoValue(self)
			if !goVal.IsValid() {
				return []runtime.Value{runtime.NilValue()}, nil
			}

			deref := goVal
			for deref.Kind() == reflect.Ptr {
				deref = deref.Elem()
			}

			// Try field
			if deref.Kind() == reflect.Struct {
				f := deref.FieldByName(fieldName)
				if !f.IsValid() {
					f = deref.FieldByName(capitalizeFirst(fieldName))
				}
				if f.IsValid() && f.CanInterface() {
					val, err := reflectToValue(f)
					if err != nil {
						return []runtime.Value{runtime.NilValue()}, nil
					}
					return []runtime.Value{val}, nil
				}
			}

			// Try method
			method := goVal.MethodByName(fieldName)
			if !method.IsValid() {
				method = goVal.MethodByName(capitalizeFirst(fieldName))
			}
			if !method.IsValid() && goVal.CanAddr() {
				method = goVal.Addr().MethodByName(fieldName)
				if !method.IsValid() {
					method = goVal.Addr().MethodByName(capitalizeFirst(fieldName))
				}
			}
			if method.IsValid() {
				fn, err := wrapGoFunc(method)
				if err != nil {
					return []runtime.Value{runtime.NilValue()}, nil
				}
				return []runtime.Value{runtime.FunctionValue(fn)}, nil
			}

			return []runtime.Value{runtime.NilValue()}, nil
		},
	}))

	// __newindex: field set
	instanceMeta.RawSet(runtime.StringValue("__newindex"), runtime.FunctionValue(&runtime.GoFunction{
		Name: name + ".__newindex",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 3 {
				return nil, nil
			}
			self := args[0]
			key := args[1]
			val := args[2]
			if !key.IsString() {
				return nil, fmt.Errorf("struct field key must be string, got %s", key.TypeName())
			}
			fieldName := key.Str()

			goVal := extractGoValue(self)
			if !goVal.IsValid() {
				return nil, fmt.Errorf("invalid struct instance")
			}

			deref := goVal
			for deref.Kind() == reflect.Ptr {
				deref = deref.Elem()
			}

			if deref.Kind() != reflect.Struct {
				return nil, fmt.Errorf("not a struct")
			}

			f := deref.FieldByName(fieldName)
			if !f.IsValid() {
				f = deref.FieldByName(capitalizeFirst(fieldName))
			}
			if !f.IsValid() || !f.CanSet() {
				return nil, fmt.Errorf("struct %s has no settable field %q", name, fieldName)
			}

			goFieldVal, err := FromValue(val, f.Type())
			if err != nil {
				return nil, fmt.Errorf("setting field %q: %v", fieldName, err)
			}
			f.Set(goFieldVal)
			return nil, nil
		},
	}))

	return makeStructInstance(rv, instanceMeta), nil
}

// Global registry mapping table pointer to reflect.Value.
// This is the "userdata" implementation.
var goValueRegistry = map[uintptr]reflect.Value{}
var goValueMu sync.RWMutex

func storeGoValue(t *runtime.Table, rv reflect.Value) {
	// Store pointer-to-value so we can modify fields
	ptr := reflect.New(rv.Type())
	ptr.Elem().Set(rv)

	key := reflect.ValueOf(t).Pointer()
	goValueMu.Lock()
	goValueRegistry[key] = ptr
	goValueMu.Unlock()
}

func extractGoValue(val runtime.Value) reflect.Value {
	if !val.IsTable() {
		return reflect.Value{}
	}
	t := val.Table()
	key := reflect.ValueOf(t).Pointer()
	goValueMu.RLock()
	rv, ok := goValueRegistry[key]
	goValueMu.RUnlock()
	if !ok {
		return reflect.Value{}
	}
	// Return the pointed-to value (settable)
	return rv.Elem()
}

// fromValueStruct attempts to convert a GScript table back to a Go struct.
func fromValueStruct(val runtime.Value, target reflect.Type) (reflect.Value, error) {
	// Try registry first
	if val.IsTable() {
		goVal := extractGoValue(val)
		if goVal.IsValid() && goVal.Type().ConvertibleTo(target) {
			return goVal.Convert(target), nil
		}
	}

	// Fall back: create zero struct and fill fields from table
	result := reflect.New(target).Elem()
	if val.IsTable() {
		t := val.Table()
		for i := 0; i < target.NumField(); i++ {
			f := target.Field(i)
			if !f.IsExported() {
				continue
			}
			// Try lowercase name first, then exact
			gsVal := t.RawGet(runtime.StringValue(strings.ToLower(f.Name)))
			if gsVal.IsNil() {
				gsVal = t.RawGet(runtime.StringValue(f.Name))
			}
			if gsVal.IsNil() {
				continue
			}
			fv, err := FromValue(gsVal, f.Type)
			if err != nil {
				continue
			}
			result.Field(i).Set(fv)
		}
	}
	return result, nil
}
