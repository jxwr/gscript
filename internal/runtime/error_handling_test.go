package runtime

import (
	"strings"
	"testing"
)

// ==================================================================
// Error handling edge cases (beyond what stdlib_test.go covers)
// ==================================================================

// --- pcall catching different error types ---

func TestPcallCatchesDivByZero(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(func() {
			return 1 / 0
		})
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("pcall should catch division by zero")
	}
	if !msg.IsString() || !strings.Contains(msg.Str(), "zero") {
		t.Errorf("expected error about zero, got %v", msg)
	}
}

func TestPcallCatchesIndexNil(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(func() {
			x := nil
			return x.foo
		})
	`)
	ok := interp.GetGlobal("ok")
	if ok.Truthy() {
		t.Errorf("pcall should catch index on nil")
	}
}

func TestPcallCatchesCallNonFunction(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(func() {
			x := 42
			return x()
		})
	`)
	ok := interp.GetGlobal("ok")
	if ok.Truthy() {
		t.Errorf("pcall should catch calling non-function")
	}
}

func TestPcallCatchesUndefinedVar(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(func() {
			return undefined_var
		})
	`)
	ok := interp.GetGlobal("ok")
	if ok.Truthy() {
		t.Errorf("pcall should catch undefined variable")
	}
}

// --- error() with different value types ---

func TestErrorWithNil(t *testing.T) {
	interp := runProgram(t, `
		ok, val := pcall(func() {
			error(nil)
		})
	`)
	ok := interp.GetGlobal("ok")
	val := interp.GetGlobal("val")
	if ok.Truthy() {
		t.Errorf("pcall should return false")
	}
	if !val.IsNil() {
		t.Errorf("expected nil error value, got %v", val)
	}
}

func TestErrorWithNumber(t *testing.T) {
	interp := runProgram(t, `
		ok, val := pcall(func() {
			error(404)
		})
	`)
	ok := interp.GetGlobal("ok")
	val := interp.GetGlobal("val")
	if ok.Truthy() {
		t.Errorf("pcall should return false")
	}
	if !val.IsInt() || val.Int() != 404 {
		t.Errorf("expected 404, got %v", val)
	}
}

func TestErrorWithBool(t *testing.T) {
	interp := runProgram(t, `
		ok, val := pcall(func() {
			error(true)
		})
	`)
	ok := interp.GetGlobal("ok")
	val := interp.GetGlobal("val")
	if ok.Truthy() {
		t.Errorf("pcall should return false")
	}
	if !val.IsBool() || !val.Bool() {
		t.Errorf("expected true, got %v", val)
	}
}

func TestErrorWithTable(t *testing.T) {
	interp := runProgram(t, `
		ok, val := pcall(func() {
			error({code: 500, msg: "internal error"})
		})
		code := val.code
		msg := val.msg
	`)
	ok := interp.GetGlobal("ok")
	if ok.Truthy() {
		t.Errorf("pcall should return false")
	}
	code := interp.GetGlobal("code")
	msg := interp.GetGlobal("msg")
	if code.Int() != 500 {
		t.Errorf("expected code=500, got %v", code)
	}
	if msg.Str() != "internal error" {
		t.Errorf("expected 'internal error', got %v", msg)
	}
}

// --- Nested pcall ---

func TestPcallNestedSuccess(t *testing.T) {
	interp := runProgram(t, `
		func inner() {
			return 42
		}
		func middle() {
			ok, val := pcall(inner)
			return ok, val
		}
		ok, val := middle()
	`)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("expected ok=true")
	}
	if interp.GetGlobal("val").Int() != 42 {
		t.Errorf("expected val=42")
	}
}

func TestPcallNestedErrorInInner(t *testing.T) {
	interp := runProgram(t, `
		func inner() {
			error("deep error")
		}
		func middle() {
			return pcall(inner)
		}
		func outer() {
			return pcall(middle)
		}
		ok, val := outer()
	`)
	// middle uses pcall(inner) which catches the error
	// so middle returns (false, "deep error") successfully
	// outer's pcall(middle) sees success, returns (true, false, "deep error")
	ok := interp.GetGlobal("ok")
	if !ok.Bool() {
		t.Errorf("expected outer pcall to succeed, got %v", ok)
	}
}

func TestPcallNestedErrorPropagates(t *testing.T) {
	interp := runProgram(t, `
		func inner() {
			error("boom")
		}
		func outer() {
			inner()
		}
		ok, msg := pcall(outer)
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("expected false")
	}
	if msg.Str() != "boom" {
		t.Errorf("expected 'boom', got %v", msg)
	}
}

// --- Stack unwinding: closures in pcall ---

func TestPcallWithClosure(t *testing.T) {
	interp := runProgram(t, `
		x := 0
		func f() {
			x = 1
			error("fail")
			x = 2
		}
		ok, msg := pcall(f)
	`)
	x := interp.GetGlobal("x")
	// x should be 1, because the assignment happens before the error
	if x.Int() != 1 {
		t.Errorf("expected x=1, got %v", x)
	}
	ok := interp.GetGlobal("ok")
	if ok.Truthy() {
		t.Errorf("expected false")
	}
}

func TestPcallPreservesState(t *testing.T) {
	interp := runProgram(t, `
		t := {1, 2, 3}
		func modify() {
			table.insert(t, 4)
			error("oops")
			table.insert(t, 5)
		}
		pcall(modify)
		result := #t
	`)
	// table.insert(t, 4) executes, then error, so #t = 4
	result := interp.GetGlobal("result")
	if result.Int() != 4 {
		t.Errorf("expected 4, got %v", result)
	}
}

// --- xpcall with handler ---

func TestXpcallHandlerReceivesError(t *testing.T) {
	interp := runProgram(t, `
		func fail() {
			error("original")
		}
		func handler(err) {
			return "wrapped: " .. err
		}
		ok, msg := xpcall(fail, handler)
	`)
	msg := interp.GetGlobal("msg")
	if msg.Str() != "wrapped: original" {
		t.Errorf("expected 'wrapped: original', got %v", msg)
	}
}

func TestXpcallHandlerWithTableError(t *testing.T) {
	interp := runProgram(t, `
		func fail() {
			error({code: 404})
		}
		func handler(err) {
			return "code:" .. tostring(err.code)
		}
		ok, msg := xpcall(fail, handler)
	`)
	msg := interp.GetGlobal("msg")
	if msg.Str() != "code:404" {
		t.Errorf("expected 'code:404', got %v", msg)
	}
}

func TestXpcallSuccessIgnoresHandler(t *testing.T) {
	interp := runProgram(t, `
		handlerCalled := false
		func good() {
			return 42
		}
		func handler(err) {
			handlerCalled = true
			return "bad"
		}
		ok, val := xpcall(good, handler)
	`)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("expected ok=true")
	}
	if interp.GetGlobal("val").Int() != 42 {
		t.Errorf("expected val=42")
	}
	if interp.GetGlobal("handlerCalled").Bool() {
		t.Errorf("handler should not be called on success")
	}
}

// --- pcall with multiple return values ---

func TestPcallMultipleReturns(t *testing.T) {
	interp := runProgram(t, `
		func multi() {
			return 1, 2, 3
		}
		ok, a, b, c := pcall(multi)
	`)
	if !interp.GetGlobal("ok").Bool() {
		t.Errorf("expected ok=true")
	}
	if interp.GetGlobal("a").Int() != 1 {
		t.Errorf("expected a=1")
	}
	if interp.GetGlobal("b").Int() != 2 {
		t.Errorf("expected b=2")
	}
	if interp.GetGlobal("c").Int() != 3 {
		t.Errorf("expected c=3")
	}
}

// --- assert tests ---

func TestAssertTruthyValues(t *testing.T) {
	tests := []struct {
		src string
	}{
		{`assert(true)`},
		{`assert(1)`},
		{`assert("hello")`},
		{`assert({})`},
		{`assert(0)`},
	}
	for _, tt := range tests {
		err := runProgramExpectError(t, tt.src)
		if err != nil {
			t.Errorf("%s should not error, got: %v", tt.src, err)
		}
	}
}

func TestAssertFalsyValues(t *testing.T) {
	tests := []string{
		`assert(false)`,
		`assert(nil)`,
	}
	for _, src := range tests {
		err := runProgramExpectError(t, src)
		if err == nil {
			t.Errorf("%s should error", src)
		}
	}
}

func TestAssertReturnsAllArgs(t *testing.T) {
	interp := runProgram(t, `
		a, b, c := assert(1, 2, 3)
	`)
	if interp.GetGlobal("a").Int() != 1 {
		t.Errorf("expected a=1")
	}
	if interp.GetGlobal("b").Int() != 2 {
		t.Errorf("expected b=2")
	}
	if interp.GetGlobal("c").Int() != 3 {
		t.Errorf("expected c=3")
	}
}

// --- Error in loop body caught by pcall ---

func TestPcallCatchesErrorInLoop(t *testing.T) {
	interp := runProgram(t, `
		func loopy() {
			for i := 1; i <= 10; i++ {
				if i == 5 {
					error("stopped at 5")
				}
			}
		}
		ok, msg := pcall(loopy)
	`)
	if interp.GetGlobal("ok").Truthy() {
		t.Errorf("expected false")
	}
	if interp.GetGlobal("msg").Str() != "stopped at 5" {
		t.Errorf("expected 'stopped at 5', got %v", interp.GetGlobal("msg"))
	}
}

// --- Pcall with no arguments ---

func TestPcallNoArgs(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall()
	`)
	if interp.GetGlobal("ok").Truthy() {
		t.Errorf("pcall with no args should fail")
	}
}

// --- Error message formatting ---

func TestPcallCatchesModByZero(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(func() {
			return 10 % 0
		})
	`)
	if interp.GetGlobal("ok").Truthy() {
		t.Errorf("expected false")
	}
	if !strings.Contains(interp.GetGlobal("msg").Str(), "zero") {
		t.Errorf("expected error about zero, got %v", interp.GetGlobal("msg"))
	}
}
