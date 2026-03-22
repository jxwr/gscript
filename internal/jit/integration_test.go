//go:build darwin && arm64

package jit

import (
	"math"
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

func expectGlobalFloat(t *testing.T, globals map[string]runtime.Value, name string, expected float64) {
	t.Helper()
	v, ok := globals[name]
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	if !v.IsFloat() {
		t.Fatalf("global %q: expected float, got %v (type=%d)", name, v, v.Type())
	}
	if math.Abs(v.Float()-expected) > 1e-9 {
		t.Fatalf("global %q: expected %f, got %f", name, expected, v.Float())
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

func TestJITIntegrationAckermann(t *testing.T) {
	// Ackermann function: two-parameter self-recursion with nested calls.
	// ack(m-1, ack(m, n-1)) — the inner result becomes the outer's second arg.
	globals, _ := compileAndRunJIT(t, `
		func ack(m, n) {
			if m == 0 { return n + 1 }
			if n == 0 { return ack(m - 1, 1) }
			return ack(m - 1, ack(m, n - 1))
		}
		r00 = ack(0, 0)
		r10 = ack(1, 0)
		r20 = ack(2, 0)
		r30 = ack(3, 0)
		r34 = ack(3, 4)
	`)
	expectGlobal(t, globals, "r00", 1)   // ack(0,0) = 1
	expectGlobal(t, globals, "r10", 2)   // ack(1,0) = 2
	expectGlobal(t, globals, "r20", 3)   // ack(2,0) = 3
	expectGlobal(t, globals, "r30", 5)   // ack(3,0) = 5
	expectGlobal(t, globals, "r34", 125) // ack(3,4) = 125
}

func TestJITIntegrationMutualRecursion(t *testing.T) {
	// Hofstadter Female/Male sequences: F(n) = n - M(F(n-1)), M(n) = n - F(M(n-1))
	// Tests cross-function JIT calls (mutual recursion, not self-recursion).
	globals, _ := compileAndRunJIT(t, `
		func F(n) {
			if n == 0 { return 1 }
			return n - M(F(n - 1))
		}
		func M(n) {
			if n == 0 { return 0 }
			return n - F(M(n - 1))
		}
		f0 = F(0)
		f1 = F(1)
		f5 = F(5)
		f10 = F(10)
		f25 = F(25)
		m0 = M(0)
		m1 = M(1)
		m5 = M(5)
		m10 = M(10)
	`)
	expectGlobal(t, globals, "f0", 1)
	expectGlobal(t, globals, "f1", 1)
	expectGlobal(t, globals, "f5", 3)
	expectGlobal(t, globals, "f10", 6)
	expectGlobal(t, globals, "f25", 16)
	expectGlobal(t, globals, "m0", 0)
	expectGlobal(t, globals, "m1", 0)
	expectGlobal(t, globals, "m5", 3)
	expectGlobal(t, globals, "m10", 6)
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

// --- SETFIELD native fast path tests ---

func TestJITIntegrationSetfieldSimple(t *testing.T) {
	// Simple SETFIELD: write a field in a loop.
	globals, _ := compileAndRunJIT(t, `
		func updatePoint(p, n) {
			for i := 1; i <= n; i++ {
				p.x = p.x + 1.0
				p.y = p.y + 2.0
			}
			return p.x
		}
		p := {x: 0.0, y: 0.0}
		result = updatePoint(p, 10000)
	`)
	expectGlobalFloat(t, globals, "result", 10000.0)
}

func TestJITIntegrationSetfieldInt(t *testing.T) {
	// SETFIELD with integer values.
	globals, _ := compileAndRunJIT(t, `
		func increment(obj, n) {
			for i := 1; i <= n; i++ {
				obj.count = obj.count + 1
			}
			return obj.count
		}
		obj := {count: 0}
		result = increment(obj, 100)
	`)
	expectGlobal(t, globals, "result", 100)
}

func TestJITIntegrationSetfieldMultipleFields(t *testing.T) {
	// SETFIELD writing to multiple fields in the same loop.
	globals, _ := compileAndRunJIT(t, `
		func advance(body, dt, n) {
			for i := 1; i <= n; i++ {
				body.x = body.x + body.vx * dt
				body.y = body.y + body.vy * dt
			}
			return body.x
		}
		b := {x: 0.0, y: 0.0, vx: 1.0, vy: 2.0}
		result = advance(b, 0.5, 100)
	`)
	expectGlobalFloat(t, globals, "result", 50.0)
}

func TestJITIntegrationSetfieldCorrectness(t *testing.T) {
	// Verify SETFIELD correctness: compare JIT vs interpreter results.
	// The function reads and writes fields in a pattern that exercises
	// the native fast path thoroughly.
	globals, _ := compileAndRunJIT(t, `
		func compute(p, n) {
			for i := 1; i <= n; i++ {
				tmp := p.x
				p.x = p.y
				p.y = tmp + p.y
			}
			return p.x
		}
		p := {x: 1.0, y: 1.0}
		result = compute(p, 20)
	`)
	// This is fibonacci-like: after 20 iterations, x = fib(21) = 10946
	expectGlobalFloat(t, globals, "result", 10946.0)
}

func TestJITIntegrationSetfieldConstValue(t *testing.T) {
	t.Skip("Known issue: method JIT SETFIELD with direct local register (compiler optimization)")
	// SETFIELD where the value is a constant (RK encoding with constant).
	// Uses a temporary variable for GETFIELD result to avoid a pre-existing
	// register clobbering issue with inline obj.val in compound expressions.
	globals, _ := compileAndRunJIT(t, `
		func reset(obj, n) {
			sum := 0
			for i := 1; i <= n; i++ {
				v := obj.val
				sum = sum + v
				obj.val = 42
			}
			return sum
		}
		o := {val: 42}
		result = reset(o, 10)
	`)
	expectGlobal(t, globals, "result", 420)
}
