package runtime

import (
	"strings"
	"testing"
)

// ==================================================================
// Coroutine tests
// ==================================================================

// Test 1: Basic yield/resume cycle
func TestCoroutineBasic(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() {
	coroutine.yield(1)
	coroutine.yield(2)
	coroutine.yield(3)
})
ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co)
ok3, v3 := coroutine.resume(co)
ok4 := coroutine.resume(co)
`)
	if ok1 := interp.GetGlobal("ok1"); !ok1.IsBool() || !ok1.Bool() {
		t.Errorf("expected ok1=true, got %v", ok1)
	}
	if v1 := interp.GetGlobal("v1"); !v1.IsInt() || v1.Int() != 1 {
		t.Errorf("expected v1=1, got %v", v1)
	}
	if ok2 := interp.GetGlobal("ok2"); !ok2.IsBool() || !ok2.Bool() {
		t.Errorf("expected ok2=true, got %v", ok2)
	}
	if v2 := interp.GetGlobal("v2"); !v2.IsInt() || v2.Int() != 2 {
		t.Errorf("expected v2=2, got %v", v2)
	}
	if ok3 := interp.GetGlobal("ok3"); !ok3.IsBool() || !ok3.Bool() {
		t.Errorf("expected ok3=true, got %v", ok3)
	}
	if v3 := interp.GetGlobal("v3"); !v3.IsInt() || v3.Int() != 3 {
		t.Errorf("expected v3=3, got %v", v3)
	}
	// After function returns (no explicit return value), ok4 should still be true
	if ok4 := interp.GetGlobal("ok4"); !ok4.IsBool() || !ok4.Bool() {
		t.Errorf("expected ok4=true, got %v", ok4)
	}
}

// Test 2: Values passed to yield and from resume
func TestCoroutinePassValues(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func(a) {
	b := coroutine.yield(a * 2)
	return b + 1
})
ok1, v1 := coroutine.resume(co, 5)
ok2, v2 := coroutine.resume(co, 20)
`)
	if v1 := interp.GetGlobal("v1"); !v1.IsInt() || v1.Int() != 10 {
		t.Errorf("expected v1=10, got %v", v1)
	}
	if v2 := interp.GetGlobal("v2"); !v2.IsInt() || v2.Int() != 21 {
		t.Errorf("expected v2=21, got %v", v2)
	}
	if ok1 := interp.GetGlobal("ok1"); !ok1.Bool() {
		t.Errorf("expected ok1=true, got %v", ok1)
	}
	if ok2 := interp.GetGlobal("ok2"); !ok2.Bool() {
		t.Errorf("expected ok2=true, got %v", ok2)
	}
}

// Test 3: coroutine.wrap creates an iterator function
func TestCoroutineWrap(t *testing.T) {
	interp := runProgram(t, `
gen := coroutine.wrap(func() {
	coroutine.yield(1)
	coroutine.yield(2)
	coroutine.yield(3)
})
r1 := gen()
r2 := gen()
r3 := gen()
`)
	if r1 := interp.GetGlobal("r1"); !r1.IsInt() || r1.Int() != 1 {
		t.Errorf("expected r1=1, got %v", r1)
	}
	if r2 := interp.GetGlobal("r2"); !r2.IsInt() || r2.Int() != 2 {
		t.Errorf("expected r2=2, got %v", r2)
	}
	if r3 := interp.GetGlobal("r3"); !r3.IsInt() || r3.Int() != 3 {
		t.Errorf("expected r3=3, got %v", r3)
	}
}

// Test 4: coroutine.status returns correct status strings
func TestCoroutineStatus(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() {
	coroutine.yield()
})
s1 := coroutine.status(co)
coroutine.resume(co)
s2 := coroutine.status(co)
coroutine.resume(co)
s3 := coroutine.status(co)
`)
	if s1 := interp.GetGlobal("s1"); s1.Str() != "suspended" {
		t.Errorf("expected s1='suspended', got %v", s1)
	}
	if s2 := interp.GetGlobal("s2"); s2.Str() != "suspended" {
		t.Errorf("expected s2='suspended', got %v", s2)
	}
	if s3 := interp.GetGlobal("s3"); s3.Str() != "dead" {
		t.Errorf("expected s3='dead', got %v", s3)
	}
}

// Test 5: Producer pattern with coroutine
func TestCoroutineProducer(t *testing.T) {
	interp := runProgram(t, `
func producer() {
	coroutine.yield(10)
	coroutine.yield(20)
	coroutine.yield(30)
	coroutine.yield(40)
	coroutine.yield(50)
}

co := coroutine.create(producer)
total := 0
for {
	ok, v := coroutine.resume(co)
	if !ok || v == nil {
		break
	}
	total = total + v
}
`)
	if total := interp.GetGlobal("total"); !total.IsInt() || total.Int() != 150 {
		t.Errorf("expected total=150, got %v", total)
	}
}

// Test 6: Generator pattern with wrap and for-range
func TestCoroutineGenerator(t *testing.T) {
	interp := runProgram(t, `
func range_gen(n) {
	return coroutine.wrap(func() {
		for i := 1; i <= n; i++ {
			coroutine.yield(i)
		}
	})
}

sum := 0
gen := range_gen(5)
for {
	v := gen()
	if v == nil {
		break
	}
	sum = sum + v
}
`)
	if sum := interp.GetGlobal("sum"); !sum.IsInt() || sum.Int() != 15 {
		t.Errorf("expected sum=15, got %v", sum)
	}
}

// Test 7: Dead coroutine returns false with error message
func TestCoroutineDeadError(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() { return 42 })
coroutine.resume(co)
ok, msg := coroutine.resume(co)
`)
	if ok := interp.GetGlobal("ok"); ok.Bool() {
		t.Errorf("expected ok=false, got %v", ok)
	}
	if msg := interp.GetGlobal("msg"); !strings.Contains(msg.String(), "dead") {
		t.Errorf("expected msg to contain 'dead', got %v", msg)
	}
}

// Test 8: Nested coroutines (outer resumes inner)
func TestCoroutineNested(t *testing.T) {
	interp := runProgram(t, `
inner := coroutine.create(func() {
	coroutine.yield(1)
	coroutine.yield(2)
})

outer := coroutine.create(func() {
	ok, v := coroutine.resume(inner)
	coroutine.yield(v * 10)
	ok, v = coroutine.resume(inner)
	coroutine.yield(v * 10)
})

_, r1 := coroutine.resume(outer)
_, r2 := coroutine.resume(outer)
`)
	if r1 := interp.GetGlobal("r1"); !r1.IsInt() || r1.Int() != 10 {
		t.Errorf("expected r1=10, got %v", r1)
	}
	if r2 := interp.GetGlobal("r2"); !r2.IsInt() || r2.Int() != 20 {
		t.Errorf("expected r2=20, got %v", r2)
	}
}

// Test 9: coroutine.isyieldable
func TestCoroutineIsYieldable(t *testing.T) {
	interp := runProgram(t, `
outside := coroutine.isyieldable()
inside := false
co := coroutine.create(func() {
	inside = coroutine.isyieldable()
	coroutine.yield()
})
coroutine.resume(co)
`)
	if outside := interp.GetGlobal("outside"); outside.Bool() {
		t.Errorf("expected outside=false, got %v", outside)
	}
	if inside := interp.GetGlobal("inside"); !inside.Bool() {
		t.Errorf("expected inside=true, got %v", inside)
	}
}

// Test 10: Coroutine with multiple yield values
func TestCoroutineMultipleYieldValues(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() {
	coroutine.yield(1, 2, 3)
})
ok, a, b, c := coroutine.resume(co)
`)
	if ok := interp.GetGlobal("ok"); !ok.Bool() {
		t.Errorf("expected ok=true, got %v", ok)
	}
	if a := interp.GetGlobal("a"); !a.IsInt() || a.Int() != 1 {
		t.Errorf("expected a=1, got %v", a)
	}
	if b := interp.GetGlobal("b"); !b.IsInt() || b.Int() != 2 {
		t.Errorf("expected b=2, got %v", b)
	}
	if c := interp.GetGlobal("c"); !c.IsInt() || c.Int() != 3 {
		t.Errorf("expected c=3, got %v", c)
	}
}

// Test 11: Coroutine with return value
func TestCoroutineReturnValue(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() {
	coroutine.yield(1)
	return 99
})
ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co)
s := coroutine.status(co)
`)
	if v1 := interp.GetGlobal("v1"); !v1.IsInt() || v1.Int() != 1 {
		t.Errorf("expected v1=1, got %v", v1)
	}
	if ok1 := interp.GetGlobal("ok1"); !ok1.Bool() {
		t.Errorf("expected ok1=true, got %v", ok1)
	}
	if v2 := interp.GetGlobal("v2"); !v2.IsInt() || v2.Int() != 99 {
		t.Errorf("expected v2=99, got %v", v2)
	}
	if ok2 := interp.GetGlobal("ok2"); !ok2.Bool() {
		t.Errorf("expected ok2=true, got %v", ok2)
	}
	if s := interp.GetGlobal("s"); s.Str() != "dead" {
		t.Errorf("expected s='dead', got %v", s)
	}
}

// Test 12: wrap with error propagation on dead coroutine
func TestCoroutineWrapDead(t *testing.T) {
	err := runProgramExpectError(t, `
gen := coroutine.wrap(func() {
	coroutine.yield(1)
})
gen()
gen()
gen()
`)
	if err == nil {
		t.Fatal("expected an error when calling wrapped dead coroutine")
	}
	if !strings.Contains(err.Error(), "dead") {
		t.Errorf("expected error to contain 'dead', got %v", err)
	}
}

// Test 13: Fibonacci generator using coroutines
func TestCoroutineFibonacci(t *testing.T) {
	interp := runProgram(t, `
func fib_gen() {
	a, b := 0, 1
	for {
		coroutine.yield(a)
		a, b = b, a + b
	}
}

co := coroutine.create(fib_gen)
results := {}
for i := 0; i < 10; i++ {
	_, v := coroutine.resume(co)
	results[i + 1] = v
}
`)
	expected := []int64{0, 1, 1, 2, 3, 5, 8, 13, 21, 34}
	for i, exp := range expected {
		interp2 := interp
		results := interp2.GetGlobal("results").Table()
		v := results.RawGet(IntValue(int64(i + 1)))
		if !v.IsInt() || v.Int() != exp {
			t.Errorf("fib[%d]: expected %d, got %v", i, exp, v)
		}
	}
}

// Test 14: Resume passes values back into yield
func TestCoroutineResumePassesValues(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() {
	x := coroutine.yield("first")
	y := coroutine.yield("second")
	return x .. " " .. y
})
ok1, v1 := coroutine.resume(co)
ok2, v2 := coroutine.resume(co, "hello")
ok3, v3 := coroutine.resume(co, "world")
`)
	if v1 := interp.GetGlobal("v1"); v1.Str() != "first" {
		t.Errorf("expected v1='first', got %v", v1)
	}
	if v2 := interp.GetGlobal("v2"); v2.Str() != "second" {
		t.Errorf("expected v2='second', got %v", v2)
	}
	if v3 := interp.GetGlobal("v3"); v3.Str() != "hello world" {
		t.Errorf("expected v3='hello world', got %v", v3)
	}
	_ = interp.GetGlobal("ok1")
	_ = interp.GetGlobal("ok2")
	_ = interp.GetGlobal("ok3")
}

// Test 15: Type function reports "coroutine"
func TestCoroutineType(t *testing.T) {
	interp := runProgram(t, `
co := coroutine.create(func() {})
t := type(co)
`)
	if tp := interp.GetGlobal("t"); tp.Str() != "coroutine" {
		t.Errorf("expected type='coroutine', got %v", tp)
	}
}

// Test 16: wrap with for-range using iterator function
func TestCoroutineWrapForRange(t *testing.T) {
	interp := runProgram(t, `
func counter(n) {
	return coroutine.wrap(func() {
		for i := 1; i <= n; i++ {
			coroutine.yield(i)
		}
	})
}

sum := 0
for v := range counter(4) {
	sum = sum + v
}
`)
	if sum := interp.GetGlobal("sum"); !sum.IsInt() || sum.Int() != 10 {
		t.Errorf("expected sum=10 (1+2+3+4), got %v", sum)
	}
}
