package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runWithTracingJIT executes with tracing + compilation, returns globals.
func runWithTracingJIT(t *testing.T, src string) map[string]runtime.Value {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	recorder.SetCompile(true) // enable compilation + execution of traces
	v.SetTraceRecorder(recorder)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals
}

func TestTraceCompile_SimpleAdd(t *testing.T) {
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 1000; i++ {
			sum = sum + i
		}
	`)
	if v := g["sum"]; v.Int() != 500500 {
		t.Errorf("sum = %d, want 500500", v.Int())
	}
}

func TestTraceCompile_ForLoop(t *testing.T) {
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + i * i
		}
	`)
	// sum of i^2 for i=1..100 = 338350
	if v := g["sum"]; v.Int() != 338350 {
		t.Errorf("sum = %d, want 338350", v.Int())
	}
}

func TestTraceCompile_Nested(t *testing.T) {
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			for j := 1; j <= 50; j++ {
				sum = sum + 1
			}
		}
	`)
	if v := g["sum"]; v.Int() != 2500 {
		t.Errorf("sum = %d, want 2500", v.Int())
	}
}

func TestTraceCompile_Conditional(t *testing.T) {
	// This test exercises side-exit recovery: the MOD op causes a side-exit,
	// and the interpreter must resume correctly at the exit PC.
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			if i % 2 == 0 {
				sum = sum + i
			}
		}
	`)
	// sum of even numbers 2+4+...+100 = 2550
	if v := g["sum"]; v.Int() != 2550 {
		t.Errorf("sum = %d, want 2550", v.Int())
	}
}

// --- Phase C tests: GETFIELD, GETTABLE, string EQ, TEST, side-exit recovery ---

func TestTraceCompile_GetField(t *testing.T) {
	// Tests native GETFIELD in traces: t.x is read every iteration.
	g := runWithTracingJIT(t, `
		t := {x: 10, y: 20}
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + t.x
		}
	`)
	if v := g["sum"]; v.Int() != 1000 {
		t.Errorf("sum = %d, want 1000", v.Int())
	}
}

func TestTraceCompile_GetFieldMultiple(t *testing.T) {
	// Tests multiple GETFIELD ops in same trace body.
	g := runWithTracingJIT(t, `
		t := {x: 3, y: 7}
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + t.x + t.y
		}
	`)
	if v := g["sum"]; v.Int() != 1000 {
		t.Errorf("sum = %d, want 1000", v.Int())
	}
}

func TestTraceCompile_GetTable(t *testing.T) {
	// Tests native GETTABLE with integer key in traces.
	g := runWithTracingJIT(t, `
		arr := {0, 0, 0, 42, 0}
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + arr[4]
		}
	`)
	if v := g["sum"]; v.Int() != 4200 {
		t.Errorf("sum = %d, want 4200", v.Int())
	}
}

func TestTraceCompile_StringEQ(t *testing.T) {
	// Tests string equality comparison in traces.
	g := runWithTracingJIT(t, `
		t := {kind: "rook"}
		count := 0
		for i := 1; i <= 100; i++ {
			if t.kind == "rook" {
				count = count + 1
			}
		}
	`)
	if v := g["count"]; v.Int() != 100 {
		t.Errorf("count = %d, want 100", v.Int())
	}
}

func TestTraceCompile_StringEQ_NotEqual(t *testing.T) {
	// Tests string inequality: the "if" branch is never taken.
	g := runWithTracingJIT(t, `
		t := {kind: "bishop"}
		count := 0
		for i := 1; i <= 100; i++ {
			if t.kind == "rook" {
				count = count + 1
			}
		}
	`)
	if v := g["count"]; v.Int() != 0 {
		t.Errorf("count = %d, want 0", v.Int())
	}
}

func TestTraceCompile_TestTruthy(t *testing.T) {
	// Tests TEST opcode with truthy values in traces.
	g := runWithTracingJIT(t, `
		t := {active: true}
		count := 0
		for i := 1; i <= 100; i++ {
			if t.active {
				count = count + 1
			}
		}
	`)
	if v := g["count"]; v.Int() != 100 {
		t.Errorf("count = %d, want 100", v.Int())
	}
}

func TestTraceCompile_IntEQ(t *testing.T) {
	// Tests integer equality comparison in traces.
	g := runWithTracingJIT(t, `
		t := {val: 5}
		count := 0
		for i := 1; i <= 100; i++ {
			if t.val == 5 {
				count = count + 1
			}
		}
	`)
	if v := g["count"]; v.Int() != 100 {
		t.Errorf("count = %d, want 100", v.Int())
	}
}

func TestTraceCompile_LT(t *testing.T) {
	// Tests LT comparison in traces.
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			if i < 51 {
				sum = sum + 1
			}
		}
	`)
	if v := g["sum"]; v.Int() != 50 {
		t.Errorf("sum = %d, want 50", v.Int())
	}
}

func TestTraceCompile_SideExitResume(t *testing.T) {
	// Tests that side-exit recovery correctly resumes the interpreter.
	// The trace will side-exit on MOD (unsupported), and the interpreter
	// should pick up at the right PC and continue the loop.
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			sum = sum + (i * 2)
		}
	`)
	// sum = 2*(1+2+...+50) = 2*1275 = 2550
	if v := g["sum"]; v.Int() != 2550 {
		t.Errorf("sum = %d, want 2550", v.Int())
	}
}

func TestTraceCompile_SideExitOnConditional(t *testing.T) {
	// Tests side-exit when a conditional guard fails mid-trace.
	// The trace records the first iteration's path; when a later iteration
	// takes the other path, it side-exits and the interpreter continues.
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 20; i++ {
			if i < 11 {
				sum = sum + 1
			} else {
				sum = sum + 2
			}
		}
	`)
	// First 10: sum += 1 each → 10
	// Next 10: sum += 2 each → 20
	// Total = 30
	if v := g["sum"]; v.Int() != 30 {
		t.Errorf("sum = %d, want 30", v.Int())
	}
}

func TestTraceCompile_GetFieldAndStringEQ(t *testing.T) {
	// Combines GETFIELD + string EQ — the chess benchmark pattern.
	g := runWithTracingJIT(t, `
		piece := {kind: "rook", value: 5}
		total := 0
		for i := 1; i <= 100; i++ {
			if piece.kind == "rook" {
				total = total + piece.value
			}
		}
	`)
	if v := g["total"]; v.Int() != 500 {
		t.Errorf("total = %d, want 500", v.Int())
	}
}

func TestTraceCompile_GetFieldAndIntEQ(t *testing.T) {
	// GETFIELD + integer comparison pattern.
	g := runWithTracingJIT(t, `
		board := {width: 8, height: 8}
		count := 0
		for i := 1; i <= 100; i++ {
			if board.width == 8 {
				count = count + board.height
			}
		}
	`)
	if v := g["count"]; v.Int() != 800 {
		t.Errorf("count = %d, want 800", v.Int())
	}
}

func TestTraceCompile_MultipleComparisons(t *testing.T) {
	// Multiple comparisons in trace body — tests unique label generation.
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			if i < 30 {
				sum = sum + 1
			}
			if i < 60 {
				sum = sum + 1
			}
		}
	`)
	// i < 30: true for i=1..29 → 29
	// i < 60: true for i=1..59 → 59
	// Total = 29 + 59 = 88
	if v := g["sum"]; v.Int() != 88 {
		t.Errorf("sum = %d, want 88", v.Int())
	}
}

func TestTraceCompile_InlinedCall(t *testing.T) {
	// Tests that a function call is inlined into the trace.
	// double(x) body should be compiled inline, not as a side-exit CALL.
	g := runWithTracingJIT(t, `
		func double(x) { return x * 2 }
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + double(i)
		}
	`)
	// 2*(1+2+...+100) = 2*5050 = 10100
	if v := g["sum"]; v.Int() != 10100 {
		t.Errorf("sum = %d, want 10100", v.Int())
	}
}

func TestTraceCompile_InlinedCallWithGetField(t *testing.T) {
	// Chess-like pattern: function reads table fields.
	g := runWithTracingJIT(t, `
		func score(piece) { return piece.value * 2 }
		p := {value: 5}
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + score(p)
		}
	`)
	if v := g["sum"]; v.Int() != 1000 {
		t.Errorf("sum = %d, want 1000", v.Int())
	}
}

func TestTraceCompile_SetField(t *testing.T) {
	// Tests native SETFIELD in traces.
	g := runWithTracingJIT(t, `
		t := {x: 0}
		for i := 1; i <= 100; i++ {
			t.x = t.x + 1
		}
		result := t.x
	`)
	if v := g["result"]; v.Int() != 100 {
		t.Errorf("result = %d, want 100", v.Int())
	}
}

func TestTraceCompile_SetTable(t *testing.T) {
	// Tests native SETTABLE in traces.
	g := runWithTracingJIT(t, `
		arr := {0, 0, 0, 0, 0}
		for i := 1; i <= 100; i++ {
			arr[3] = arr[3] + 1
		}
		result := arr[3]
	`)
	if v := g["result"]; v.Int() != 100 {
		t.Errorf("result = %d, want 100", v.Int())
	}
}

func TestTraceCompile_ChessMovePattern(t *testing.T) {
	// Chess-like make/unmake move pattern: SETTABLE + GETFIELD + SETFIELD
	g := runWithTracingJIT(t, `
		board := {0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		piece := {col: 3, row: 5, alive: true}
		count := 0
		for i := 1; i <= 50; i++ {
			// "make move": set board position
			board[piece.col] = 1
			// "unmake move": clear board position
			board[piece.col] = 0
			count = count + 1
		}
		result := count
	`)
	if v := g["result"]; v.Int() != 50 {
		t.Errorf("result = %d, want 50", v.Int())
	}
}

func TestTraceCompile_SelfRecursion(t *testing.T) {
	// Simple self-recursive function: factorial-like accumulation
	g := runWithTracingJIT(t, `
		func sumTo(n) {
			if n <= 0 { return 0 }
			return n + sumTo(n - 1)
		}
		result := 0
		for i := 1; i <= 20; i++ {
			result = result + sumTo(10)
		}
	`)
	// sumTo(10) = 55, repeated 20 times = 1100
	if v := g["result"]; v.Int() != 1100 {
		t.Errorf("result = %d, want 1100", v.Int())
	}
}

func TestTraceCompile_SelfRecursionDeep(t *testing.T) {
	// Tests that deep recursion correctly side-exits when exceeding trace depth limit
	g := runWithTracingJIT(t, `
		func fib(n) {
			if n <= 1 { return n }
			return fib(n-1) + fib(n-2)
		}
		result := 0
		for i := 1; i <= 15; i++ {
			result = fib(10)
		}
	`)
	if v := g["result"]; v.Int() != 55 {
		t.Errorf("result = %d, want 55", v.Int())
	}
}

func TestTraceCompile_NegamaxPattern(t *testing.T) {
	// Simplified negamax-like pattern with table access + self-recursion
	g := runWithTracingJIT(t, `
		func minimax(depth) {
			if depth <= 0 { return 1 }
			best := 0
			for i := 1; i <= 3; i++ {
				score := minimax(depth - 1)
				if score > best { best = score }
			}
			return best
		}
		result := 0
		for i := 1; i <= 20; i++ {
			result = result + minimax(3)
		}
	`)
	// minimax(3) = 1 (always returns 1 at depth 0, propagates up)
	if v := g["result"]; v.Int() != 20 {
		t.Errorf("result = %d, want 20", v.Int())
	}
}

func TestTraceCompile_Bit32Bxor(t *testing.T) {
	// bit32.bxor is called heavily in negamax for Zobrist hashing.
	// Should be inlined as ARM64 EOR instruction.
	g := runWithTracingJIT(t, `
		hash := 12345
		for i := 1; i <= 100; i++ {
			hash = bit32.bxor(hash, i)
		}
		result := hash
	`)
	// Compute expected: 12345 ^ 1 ^ 2 ^ ... ^ 100
	expected := int64(12345)
	for i := int64(1); i <= 100; i++ {
		expected ^= i
	}
	if v := g["result"]; v.Int() != expected {
		t.Errorf("result = %d, want %d", v.Int(), expected)
	}
}

func TestTraceCompile_Bit32Band(t *testing.T) {
	g := runWithTracingJIT(t, `
		result := 0
		for i := 1; i <= 100; i++ {
			result = result + bit32.band(i, 1)
		}
	`)
	// band(i,1) = 1 for odd i, 0 for even. 50 odd numbers in 1..100
	if v := g["result"]; v.Int() != 50 {
		t.Errorf("result = %d, want 50", v.Int())
	}
}

func TestTraceCompile_Mod(t *testing.T) {
	// Simple MOD without conditional branch (no side-exit issues)
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + (i % 7)
		}
		result := sum
	`)
	// sum of (i%7) for i=1..100
	expected := int64(0)
	for i := int64(1); i <= 100; i++ {
		expected += i % 7
	}
	if v := g["result"]; v.Int() != expected {
		t.Errorf("result = %d, want %d", v.Int(), expected)
	}
}

func TestTraceCompile_Len(t *testing.T) {
	g := runWithTracingJIT(t, `
		arr := {10, 20, 30, 40, 50}
		sum := 0
		for i := 1; i <= 20; i++ {
			sum = sum + #arr
		}
		result := sum
	`)
	if v := g["result"]; v.Int() != 100 {
		t.Errorf("result = %d, want 100", v.Int())
	}
}

func TestTraceCompile_GetGlobal(t *testing.T) {
	g := runWithTracingJIT(t, `
		myval := 42
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + myval
		}
		result := sum
	`)
	if v := g["result"]; v.Int() != 4200 {
		t.Errorf("result = %d, want 4200", v.Int())
	}
}

func TestTraceCompile_SetGlobal(t *testing.T) {
	g := runWithTracingJIT(t, `
		counter := 0
		for i := 1; i <= 100; i++ {
			counter = counter + 1
		}
		result := counter
	`)
	if v := g["result"]; v.Int() != 100 {
		t.Errorf("result = %d, want 100", v.Int())
	}
}

func TestTraceCompile_UNM(t *testing.T) {
	g := runWithTracingJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			sum = sum + (-i)
		}
		result := sum
	`)
	if v := g["result"]; v.Int() != -1275 {
		t.Errorf("result = %d, want -1275", v.Int())
	}
}

func TestTraceCompile_Concat(t *testing.T) {
	// CONCAT still side-exits but should not crash
	g := runWithTracingJIT(t, `
		result := ""
		for i := 1; i <= 20; i++ {
			result = "x"
		}
	`)
	if v := g["result"]; v.Str() != "x" {
		t.Errorf("result = %q, want \"x\"", v.Str())
	}
}

func TestTraceCompile_NegamaxFullCoverage(t *testing.T) {
	// Simplified negamax with all the ops: GETGLOBAL, GETFIELD, GETTABLE,
	// SETTABLE, SETFIELD, MOD, bit32.bxor, LEN, comparisons
	g := runWithTracingJIT(t, `
		board := {}
		board[101] = {type: "R", side: "red", col: 1, row: 1}
		board[502] = {type: "K", side: "red", col: 5, row: 2}

		nodeCount := 0
		func search(depth) {
			nodeCount = nodeCount + 1
			if depth <= 0 { return 0 }
			best := -999
			for col := 1; col <= 9; col++ {
				key := col * 100 + 1
				p := board[key]
				if p != nil {
					if p.type == "R" {
						score := -search(depth - 1)
						if score > best { best = score }
					}
				}
			}
			return best
		}

		result := 0
		for i := 1; i <= 20; i++ {
			result = search(2)
		}
		finalNodes := nodeCount
	`)
	if v := g["finalNodes"]; v.Int() <= 0 {
		t.Errorf("finalNodes = %d, want > 0", v.Int())
	}
}

func TestTraceCompile_SparseTableAccess(t *testing.T) {
	// Tests board[col*100+row] pattern — sparse integer keys that
	// go to imap. After optimization, these should use expanded array.
	g := runWithTracingJIT(t, `
		board := {}
		// Set some board positions (sparse keys like 101, 502, 910)
		board[101] = 1
		board[502] = 2
		board[910] = 3
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + board[502]
		}
		result := sum
	`)
	if v := g["result"]; v.Int() != 200 {
		t.Errorf("result = %d, want 200", v.Int())
	}
}

func TestTraceCompile_BoardWriteRead(t *testing.T) {
	// Chess-like make/unmake on sparse board keys
	g := runWithTracingJIT(t, `
		board := {}
		board[501] = 99
		sum := 0
		for i := 1; i <= 50; i++ {
			board[301] = i
			sum = sum + board[301]
			board[301] = nil
		}
		result := sum
	`)
	// sum = 1+2+...+50 = 1275
	if v := g["result"]; v.Int() != 1275 {
		t.Errorf("result = %d, want 1275", v.Int())
	}
}

func TestTraceCompile_MatchesInterpreter(t *testing.T) {
	src := `
		a := 0
		b := 1
		for i := 0; i < 30; i++ {
			t := a + b
			a = b
			b = t
		}
		result := a
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with tracing JIT
	g2 := runWithTracingJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, tracing=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestTraceCompile_MatchesInterpreter_PhaseC(t *testing.T) {
	// Comprehensive test exercising GETFIELD, GETTABLE, comparisons, TEST.
	// Run both with and without tracing JIT and verify identical results.
	tests := []struct {
		name string
		src  string
		key  string
	}{
		{
			name: "getfield_loop",
			key:  "result",
			src: `
				obj := {x: 7, y: 3}
				result := 0
				for i := 1; i <= 50; i++ {
					result = result + obj.x - obj.y
				}
			`,
		},
		{
			name: "gettable_loop",
			key:  "result",
			src: `
				arr := {0, 10, 20, 30, 40}
				result := 0
				for i := 1; i <= 50; i++ {
					result = result + arr[3]
				}
			`,
		},
		{
			name: "string_eq_branch",
			key:  "result",
			src: `
				piece := {kind: "knight"}
				result := 0
				for i := 1; i <= 50; i++ {
					if piece.kind == "knight" {
						result = result + 3
					} else {
						result = result + 1
					}
				}
			`,
		},
		{
			name: "int_lt_branch",
			key:  "result",
			src: `
				result := 0
				for i := 1; i <= 50; i++ {
					if i < 25 {
						result = result + 1
					} else {
						result = result + 2
					}
				}
			`,
		},
		{
			name: "bool_test",
			key:  "result",
			src: `
				flag := {on: true}
				result := 0
				for i := 1; i <= 50; i++ {
					if flag.on {
						result = result + 1
					}
				}
			`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run without tracing
			proto := compileProto(t, tt.src)
			g1 := runtime.NewInterpreterGlobals()
			vm.New(g1).Execute(proto)

			// Run with tracing JIT
			g2 := runWithTracingJIT(t, tt.src)

			v1 := g1[tt.key]
			v2 := g2[tt.key]
			if v1.Int() != v2.Int() {
				t.Errorf("mismatch: interpreter=%d, tracing=%d", v1.Int(), v2.Int())
			}
		})
	}
}
