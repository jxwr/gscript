package gscript_test

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	gs "github.com/gscript/gscript/gscript"
)

// --- Basic VM tests ---

func TestExec(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	err := vm.Exec(`print("hello", "world")`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 || output[0] != "hello\tworld" {
		t.Fatalf("expected 'hello\\tworld', got %v", output)
	}
}

func TestExecError(t *testing.T) {
	vm := gs.New()
	err := vm.Exec(`x :=`)
	if err == nil {
		t.Fatal("expected parse error")
	}
	gsErr, ok := err.(*gs.Error)
	if !ok {
		t.Fatalf("expected *gscript.Error, got %T", err)
	}
	if gsErr.Kind != gs.ErrParse {
		t.Fatalf("expected ErrParse, got %s", gsErr.Kind)
	}
}

func TestCall(t *testing.T) {
	vm := gs.New()
	err := vm.Exec(`
		func add(a, b) {
			return a + b
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := vm.Call("add", 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// GScript int + int returns int
	if results[0] != int64(7) {
		t.Fatalf("expected 7, got %v (%T)", results[0], results[0])
	}
}

func TestCallNotFound(t *testing.T) {
	vm := gs.New()
	_, err := vm.Call("nonexistent")
	if err == nil {
		t.Fatal("expected error calling nonexistent function")
	}
}

func TestSetGet(t *testing.T) {
	vm := gs.New()
	if err := vm.Set("x", 42); err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("x")
	if err != nil {
		t.Fatal(err)
	}
	if val != int64(42) {
		t.Fatalf("expected 42, got %v (%T)", val, val)
	}
}

func TestSetGet_string(t *testing.T) {
	vm := gs.New()
	if err := vm.Set("name", "gscript"); err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("name")
	if err != nil {
		t.Fatal(err)
	}
	if val != "gscript" {
		t.Fatalf("expected 'gscript', got %v", val)
	}
}

func TestSetGet_float(t *testing.T) {
	vm := gs.New()
	if err := vm.Set("pi", 3.14); err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("pi")
	if err != nil {
		t.Fatal(err)
	}
	if val != 3.14 {
		t.Fatalf("expected 3.14, got %v", val)
	}
}

func TestSetGet_bool(t *testing.T) {
	vm := gs.New()
	if err := vm.Set("flag", true); err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("flag")
	if err != nil {
		t.Fatal(err)
	}
	if val != true {
		t.Fatalf("expected true, got %v", val)
	}
}

func TestSetGet_nil(t *testing.T) {
	vm := gs.New()
	if err := vm.Set("nothing", nil); err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("nothing")
	if err != nil {
		t.Fatal(err)
	}
	if val != nil {
		t.Fatalf("expected nil, got %v", val)
	}
}

func TestRegisterFunc(t *testing.T) {
	vm := gs.New()
	err := vm.RegisterFunc("square", func(x float64) float64 {
		return x * x
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := vm.Call("square", 5.0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0] != float64(25) {
		t.Fatalf("expected 25.0, got %v (%T)", results[0], results[0])
	}
}

func TestRegisterFunc_multiReturn(t *testing.T) {
	vm := gs.New()
	err := vm.RegisterFunc("divmod", func(a, b int64) (int64, int64) {
		return a / b, a % b
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := vm.Call("divmod", 17, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0] != int64(3) || results[1] != int64(2) {
		t.Fatalf("expected [3 2], got %v", results)
	}
}

func TestRegisterFunc_error(t *testing.T) {
	vm := gs.New()
	err := vm.RegisterFunc("fail", func() error {
		return fmt.Errorf("something went wrong")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = vm.Call("fail")
	if err == nil {
		t.Fatal("expected error from fail()")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegisterFunc_fromScript(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	vm.RegisterFunc("double", func(x int64) int64 { return x * 2 })
	err := vm.Exec(`print(double(21))`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 || output[0] != "42" {
		t.Fatalf("expected '42', got %v", output)
	}
}

func TestRegisterTable(t *testing.T) {
	vm := gs.New()
	err := vm.RegisterTable("mymath", map[string]interface{}{
		"add": func(a, b float64) float64 { return a + b },
		"mul": func(a, b float64) float64 { return a * b },
		"pi":  3.14159,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = vm.Exec(`result := mymath.add(mymath.pi, 1.0)`)
	if err != nil {
		t.Fatal(err)
	}

	val, err := vm.Get("result")
	if err != nil {
		t.Fatal(err)
	}
	expected := 3.14159 + 1.0
	if val != expected {
		t.Fatalf("expected %v, got %v", expected, val)
	}
}

// --- Type conversion tests ---

func TestToValue_slice(t *testing.T) {
	vm := gs.New()
	err := vm.Set("arr", []int{10, 20, 30})
	if err != nil {
		t.Fatal(err)
	}
	// GScript: arr is a 1-based table
	err = vm.Exec(`result := arr[1] + arr[2] + arr[3]`)
	if err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("result")
	if err != nil {
		t.Fatal(err)
	}
	if val != int64(60) {
		t.Fatalf("expected 60, got %v", val)
	}
}

func TestToValue_map(t *testing.T) {
	vm := gs.New()
	err := vm.Set("data", map[string]interface{}{
		"name": "test",
		"val":  42,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = vm.Exec(`name := data.name`)
	if err != nil {
		t.Fatal(err)
	}
	val, err := vm.Get("name")
	if err != nil {
		t.Fatal(err)
	}
	if val != "test" {
		t.Fatalf("expected 'test', got %v", val)
	}
}

func TestToValue_func(t *testing.T) {
	vm := gs.New()
	err := vm.Set("greet", func(name string) string {
		return "Hello, " + name + "!"
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := vm.Call("greet", "world")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != "Hello, world!" {
		t.Fatalf("expected 'Hello, world!', got %v", results)
	}
}

// --- Struct binding tests ---

type Vec2 struct {
	X, Y float64
}

func (v Vec2) Length() float64          { return math.Sqrt(v.X*v.X + v.Y*v.Y) }
func (v Vec2) Add(other Vec2) Vec2      { return Vec2{v.X + other.X, v.Y + other.Y} }
func (v *Vec2) Scale(f float64)         { v.X *= f; v.Y *= f }
func (v Vec2) String() string           { return fmt.Sprintf("Vec2(%g, %g)", v.X, v.Y) }

func TestBindStruct_new(t *testing.T) {
	vm := gs.New()
	if err := vm.BindStruct("Vec2", Vec2{}); err != nil {
		t.Fatal(err)
	}
	err := vm.Exec(`v := Vec2.new(3, 4)`)
	if err != nil {
		t.Fatal(err)
	}
	// v should be a table wrapping Vec2{3, 4}
	val, err := vm.Get("v")
	if err != nil {
		t.Fatal(err)
	}
	if val == nil {
		t.Fatal("expected non-nil value for v")
	}
}

func TestBindStruct_fieldAccess(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	if err := vm.BindStruct("Vec2", Vec2{}); err != nil {
		t.Fatal(err)
	}
	err := vm.Exec(`
		v := Vec2.new(3, 4)
		print(v.X)
		print(v.Y)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 2 {
		t.Fatalf("expected 2 outputs, got %d: %v", len(output), output)
	}
	if output[0] != "3.0" && output[0] != "3" {
		t.Fatalf("expected X=3, got %q", output[0])
	}
	if output[1] != "4.0" && output[1] != "4" {
		t.Fatalf("expected Y=4, got %q", output[1])
	}
}

func TestBindStruct_fieldSet(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	if err := vm.BindStruct("Vec2", Vec2{}); err != nil {
		t.Fatal(err)
	}
	err := vm.Exec(`
		v := Vec2.new(3, 4)
		v.X = 10
		print(v.X)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 {
		t.Fatalf("expected 1 output, got %d: %v", len(output), output)
	}
	if output[0] != "10.0" && output[0] != "10" {
		t.Fatalf("expected X=10, got %q", output[0])
	}
}

func TestBindStruct_methodCall(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	if err := vm.BindStruct("Vec2", Vec2{}); err != nil {
		t.Fatal(err)
	}
	err := vm.Exec(`
		v := Vec2.new(3, 4)
		print(v.Length())
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 {
		t.Fatalf("expected 1 output, got %d: %v", len(output), output)
	}
	if output[0] != "5.0" && output[0] != "5" {
		t.Fatalf("expected Length()=5, got %q", output[0])
	}
}

func TestBindStruct_returnStruct(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	if err := vm.BindStruct("Vec2", Vec2{}); err != nil {
		t.Fatal(err)
	}
	err := vm.Exec(`
		a := Vec2.new(1, 2)
		b := Vec2.new(3, 4)
		c := a.Add(b)
		print(c.X)
		print(c.Y)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 2 {
		t.Fatalf("expected 2 outputs, got %d: %v", len(output), output)
	}
	if output[0] != "4.0" && output[0] != "4" {
		t.Fatalf("expected c.X=4, got %q", output[0])
	}
	if output[1] != "6.0" && output[1] != "6" {
		t.Fatalf("expected c.Y=6, got %q", output[1])
	}
}

func TestBindStructWithConstructor(t *testing.T) {
	vm := gs.New()

	type Player struct {
		Name  string
		HP    int
		Level int
	}

	if err := vm.BindStructWithConstructor("Player", Player{}, func(name string) Player {
		return Player{Name: name, HP: 100, Level: 1}
	}); err != nil {
		t.Fatal(err)
	}

	var output []string
	vm = gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))
	if err := vm.BindStructWithConstructor("Player", Player{}, func(name string) Player {
		return Player{Name: name, HP: 100, Level: 1}
	}); err != nil {
		t.Fatal(err)
	}

	err := vm.Exec(`
		p := Player.new("Alice")
		print(p.Name)
		print(p.HP)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 2 {
		t.Fatalf("expected 2 outputs, got %d: %v", len(output), output)
	}
	if output[0] != "Alice" {
		t.Fatalf("expected 'Alice', got %q", output[0])
	}
	if output[1] != "100" {
		t.Fatalf("expected '100', got %q", output[1])
	}
}

// --- Pool tests ---

func TestPool(t *testing.T) {
	pool := gs.NewPool(5, func() *gs.VM {
		return gs.New()
	})

	vm := pool.Get()
	if vm == nil {
		t.Fatal("expected non-nil VM")
	}
	pool.Put(vm)
	if pool.Size() != 1 {
		t.Fatalf("expected pool size 1, got %d", pool.Size())
	}

	// Get should reuse
	vm2 := pool.Get()
	if vm2 == nil {
		t.Fatal("expected non-nil VM")
	}
	if pool.Size() != 0 {
		t.Fatalf("expected pool size 0, got %d", pool.Size())
	}
}

func TestPool_concurrent(t *testing.T) {
	pool := gs.NewPool(10, func() *gs.VM {
		vm := gs.New()
		vm.RegisterFunc("square", func(x int64) int64 { return x * x })
		return vm
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := pool.Do(func(vm *gs.VM) error {
				results, err := vm.Call("square", int64(n))
				if err != nil {
					return err
				}
				expected := int64(n) * int64(n)
				if results[0] != expected {
					return fmt.Errorf("expected %d^2=%d, got %v", n, expected, results[0])
				}
				return nil
			})
			if err != nil {
				t.Errorf("goroutine %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestPool_Do(t *testing.T) {
	pool := gs.NewPool(2, func() *gs.VM {
		vm := gs.New()
		vm.RegisterFunc("inc", func(x int64) int64 { return x + 1 })
		return vm
	})

	err := pool.Do(func(vm *gs.VM) error {
		results, err := vm.Call("inc", 41)
		if err != nil {
			return err
		}
		if results[0] != int64(42) {
			return fmt.Errorf("expected 42, got %v", results[0])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// VM should be returned to pool
	if pool.Size() != 1 {
		t.Fatalf("expected pool size 1 after Do, got %d", pool.Size())
	}
}

// --- Error handling tests ---

func TestError_parseError(t *testing.T) {
	vm := gs.New()
	err := vm.Exec(`func {`)
	if err == nil {
		t.Fatal("expected error")
	}
	gsErr, ok := err.(*gs.Error)
	if !ok {
		t.Fatalf("expected *gscript.Error, got %T", err)
	}
	if gsErr.Kind != gs.ErrParse {
		t.Fatalf("expected ErrParse, got %s", gsErr.Kind)
	}
}

func TestError_runtimeError(t *testing.T) {
	vm := gs.New()
	err := vm.Exec(`x := 1 + "abc"`)
	if err == nil {
		t.Fatal("expected runtime error")
	}
	gsErr, ok := err.(*gs.Error)
	if !ok {
		t.Fatalf("expected *gscript.Error, got %T", err)
	}
	if gsErr.Kind != gs.ErrRuntime {
		t.Fatalf("expected ErrRuntime, got %s", gsErr.Kind)
	}
}

// --- Options tests ---

func TestWithPrint(t *testing.T) {
	var captured []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		captured = append(captured, strings.Join(parts, " "))
	}))
	vm.Exec(`print("test", 123)`)
	if len(captured) != 1 {
		t.Fatalf("expected 1 captured, got %d", len(captured))
	}
	if captured[0] != "test 123" {
		t.Fatalf("expected 'test 123', got %q", captured[0])
	}
}

func TestWithLibs(t *testing.T) {
	// LibSafe should still work for basic math
	vm := gs.New(gs.WithLibs(gs.LibSafe))
	err := vm.Exec(`x := 1 + 2`)
	if err != nil {
		t.Fatal(err)
	}
}

// --- Integration: Go functions called from GScript ---

func TestIntegration_goFuncWithScriptCallback(t *testing.T) {
	var output []string
	vm := gs.New(gs.WithPrint(func(args ...interface{}) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		output = append(output, strings.Join(parts, "\t"))
	}))

	vm.RegisterFunc("applyTwice", func(x int64) int64 {
		return x * 2 * 2
	})

	err := vm.Exec(`
		result := applyTwice(5)
		print(result)
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 || output[0] != "20" {
		t.Fatalf("expected '20', got %v", output)
	}
}
