//go:build darwin && arm64

package jit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// compileAndRunJIT compiles GScript source, runs it with JIT enabled.
func compileAndRunJIT(t *testing.T, src string) (map[string]runtime.Value, string) {
	t.Helper()

	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	var buf strings.Builder
	globals := runtime.NewInterpreterGlobals()
	globals["print"] = runtime.FunctionValue(&runtime.GoFunction{
		Name: "print",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = a.String()
			}
			buf.WriteString(strings.Join(parts, "\t"))
			buf.WriteString("\n")
			return nil, nil
		},
	})

	engine := NewEngine()
	engine.SetThreshold(1) // compile on first call
	defer engine.Free()

	v := vm.New(globals)
	v.SetJIT(engine)
	_, err = v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals, buf.String()
}

func expectGlobal(t *testing.T, globals map[string]runtime.Value, name string, expected int64) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	if !v.IsInt() || v.Int() != expected {
		t.Fatalf("global %q: expected %d, got %v", name, expected, v)
	}
}

func TestJITIntegrationSimpleArith(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func add(a, b) {
			return a + b
		}
		result = add(10, 32)
	`)
	expectGlobal(t, globals, "result", 42)
}

func TestJITIntegrationForLoop(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func sumTo(n) {
			sum := 0
			for i := 1; i <= n; i++ {
				sum += i
			}
			return sum
		}
		result = sumTo(100)
	`)
	expectGlobal(t, globals, "result", 5050)
}

func TestJITIntegrationFibIterative(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func fib(n) {
			a, b := 0, 1
			for i := 0; i < n; i++ {
				temp := a + b
				a = b
				b = temp
			}
			return a
		}
		result = fib(30)
	`)
	expectGlobal(t, globals, "result", 832040)
}

func TestJITIntegrationFibRecursive(t *testing.T) {
	// Recursive fib uses CALL which causes side-exit → falls back to interpreter.
	// This tests the side-exit + interpreter resume path.
	globals, _ := compileAndRunJIT(t, `
		func fib(n) {
			if n <= 1 {
				return n
			}
			return fib(n-1) + fib(n-2)
		}
		result = fib(20)
	`)
	expectGlobal(t, globals, "result", 6765)
}

func TestJITIntegrationNestedLoops(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func matrix() {
			sum := 0
			for i := 0; i < 100; i++ {
				for j := 0; j < 100; j++ {
					sum += 1
				}
			}
			return sum
		}
		result = matrix()
	`)
	expectGlobal(t, globals, "result", 10000)
}

func TestJITIntegrationConditionals(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func abs(x) {
			if x < 0 {
				return -x
			}
			return x
		}
		a = abs(42)
		b = abs(-42)
	`)
	expectGlobal(t, globals, "a", 42)
	expectGlobal(t, globals, "b", 42)
}

func TestJITIntegrationMultipleReturns(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func swap(a, b) {
			return b, a
		}
		x, y := swap(1, 2)
		result = x * 10 + y
	`)
	expectGlobal(t, globals, "result", 21)
}

func TestJITIntegrationPrint(t *testing.T) {
	// Print calls GETGLOBAL (for 'print') + CALL → both side-exit from JIT.
	// This tests that functions with globals work correctly via interpreter fallback.
	_, output := compileAndRunJIT(t, `
		func hello() {
			print("hello world")
		}
		hello()
	`)
	if !strings.Contains(output, "hello world") {
		t.Fatalf("expected 'hello world', got %q", output)
	}
}

func TestJITIntegrationCountdown(t *testing.T) {
	globals, _ := compileAndRunJIT(t, `
		func countdown(n) {
			count := 0
			for i := n; i > 0; i-- {
				count += 1
			}
			return count
		}
		result = countdown(1000)
	`)
	expectGlobal(t, globals, "result", 1000)
}
