package runtime

import (
	"testing"
)

// ==================================================================
// Closure and upvalue advanced tests
// ==================================================================

// --- Counter pattern variations ---

func TestClosureCounterWithReset(t *testing.T) {
	interp := runProgram(t, `
		func makeCounter() {
			n := 0
			inc := func() { n = n + 1; return n }
			reset := func() { n = 0 }
			get := func() { return n }
			return inc, reset, get
		}
		inc, reset, get := makeCounter()
		inc()
		inc()
		inc()
		r1 := get()
		reset()
		r2 := get()
		inc()
		r3 := get()
	`)
	if interp.GetGlobal("r1").Int() != 3 {
		t.Errorf("expected r1=3, got %v", interp.GetGlobal("r1"))
	}
	if interp.GetGlobal("r2").Int() != 0 {
		t.Errorf("expected r2=0, got %v", interp.GetGlobal("r2"))
	}
	if interp.GetGlobal("r3").Int() != 1 {
		t.Errorf("expected r3=1, got %v", interp.GetGlobal("r3"))
	}
}

func TestClosureCounterStartValue(t *testing.T) {
	v := getGlobal(t, `
		func makeCounter(start) {
			n := start
			return func() {
				n = n + 1
				return n
			}
		}
		c := makeCounter(10)
		c()
		result := c()
	`, "result")
	if !v.IsInt() || v.Int() != 12 {
		t.Errorf("expected 12, got %v", v)
	}
}

// --- Memoization pattern ---

func TestClosureMemoize(t *testing.T) {
	interp := runProgram(t, `
		func makeMemoFib() {
			cache := {}
			func fib(n) {
				if cache[n] != nil {
					return cache[n]
				}
				result := 0
				if n <= 1 {
					result = n
				} else {
					result = fib(n - 1) + fib(n - 2)
				}
				cache[n] = result
				return result
			}
			return fib
		}
		fib := makeMemoFib()
		r1 := fib(10)
		r2 := fib(15)
	`)
	if interp.GetGlobal("r1").Int() != 55 {
		t.Errorf("expected fib(10)=55, got %v", interp.GetGlobal("r1"))
	}
	if interp.GetGlobal("r2").Int() != 610 {
		t.Errorf("expected fib(15)=610, got %v", interp.GetGlobal("r2"))
	}
}

// --- Closure over loop variable with copy ---

func TestClosureLoopCopy(t *testing.T) {
	interp := runProgram(t, `
		funcs := {}
		for i := 1; i <= 5; i++ {
			val := i
			funcs[i] = func() { return val }
		}
		r1 := funcs[1]()
		r3 := funcs[3]()
		r5 := funcs[5]()
	`)
	if interp.GetGlobal("r1").Int() != 1 {
		t.Errorf("expected r1=1, got %v", interp.GetGlobal("r1"))
	}
	if interp.GetGlobal("r3").Int() != 3 {
		t.Errorf("expected r3=3, got %v", interp.GetGlobal("r3"))
	}
	if interp.GetGlobal("r5").Int() != 5 {
		t.Errorf("expected r5=5, got %v", interp.GetGlobal("r5"))
	}
}

// --- Multiple closures sharing same upvalue ---

func TestClosureSharedUpvalueThree(t *testing.T) {
	interp := runProgram(t, `
		func make() {
			x := 0
			add := func(n) { x = x + n }
			sub := func(n) { x = x - n }
			get := func() { return x }
			return add, sub, get
		}
		add, sub, get := make()
		add(10)
		add(5)
		sub(3)
		result := get()
	`)
	// 10+5-3 = 12
	result := interp.GetGlobal("result")
	if result.Int() != 12 {
		t.Errorf("expected 12, got %v", result)
	}
}

// --- Recursive closure via variable ---

func TestClosureRecursiveViaVariable(t *testing.T) {
	v := getGlobal(t, `
		fact := nil
		fact = func(n) {
			if n <= 1 { return 1 }
			return n * fact(n - 1)
		}
		result := fact(6)
	`, "result")
	if !v.IsInt() || v.Int() != 720 {
		t.Errorf("expected 720, got %v", v)
	}
}

func TestClosureMutualRecursion(t *testing.T) {
	v := getGlobal(t, `
		isEven := nil
		isOdd := nil
		isEven = func(n) {
			if n == 0 { return true }
			return isOdd(n - 1)
		}
		isOdd = func(n) {
			if n == 0 { return false }
			return isEven(n - 1)
		}
		result := isEven(10)
	`, "result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

func TestClosureMutualRecursionOdd(t *testing.T) {
	v := getGlobal(t, `
		isEven := nil
		isOdd := nil
		isEven = func(n) {
			if n == 0 { return true }
			return isOdd(n - 1)
		}
		isOdd = func(n) {
			if n == 0 { return false }
			return isEven(n - 1)
		}
		result := isOdd(7)
	`, "result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

// --- Closure that modifies upvalue from outer scope ---

func TestClosureModifyOuterScope(t *testing.T) {
	v := getGlobal(t, `
		total := 0
		func accumulate(n) {
			total = total + n
		}
		accumulate(10)
		accumulate(20)
		accumulate(30)
		result := total
	`, "result")
	if !v.IsInt() || v.Int() != 60 {
		t.Errorf("expected 60, got %v", v)
	}
}

// --- Closure returning closure ---

func TestClosureReturningClosure(t *testing.T) {
	v := getGlobal(t, `
		func compose(f, g) {
			return func(x) {
				return f(g(x))
			}
		}
		func double(x) { return x * 2 }
		func addOne(x) { return x + 1 }
		h := compose(double, addOne)
		result := h(5)
	`, "result")
	// addOne(5) = 6, double(6) = 12
	if !v.IsInt() || v.Int() != 12 {
		t.Errorf("expected 12, got %v", v)
	}
}

// --- Higher-order function patterns ---

func TestClosureMap(t *testing.T) {
	interp := runProgram(t, `
		func map_table(t, f) {
			result := {}
			for i := 1; i <= #t; i++ {
				result[i] = f(t[i])
			}
			return result
		}
		doubled := map_table({1, 2, 3, 4}, func(x) { return x * 2 })
	`)
	tbl := interp.GetGlobal("doubled").Table()
	expected := []int64{2, 4, 6, 8}
	for i, exp := range expected {
		v := tbl.RawGet(IntValue(int64(i + 1)))
		if v.Int() != exp {
			t.Errorf("doubled[%d]: expected %d, got %v", i+1, exp, v)
		}
	}
}

func TestClosureFilter(t *testing.T) {
	interp := runProgram(t, `
		func filter(t, pred) {
			result := {}
			for i := 1; i <= #t; i++ {
				if pred(t[i]) {
					table.insert(result, t[i])
				}
			}
			return result
		}
		evens := filter({1, 2, 3, 4, 5, 6}, func(x) { return x % 2 == 0 })
	`)
	tbl := interp.GetGlobal("evens").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 evens, got %d", tbl.Length())
	}
	expected := []int64{2, 4, 6}
	for i, exp := range expected {
		v := tbl.RawGet(IntValue(int64(i + 1)))
		if v.Int() != exp {
			t.Errorf("evens[%d]: expected %d, got %v", i+1, exp, v)
		}
	}
}

func TestClosureReduce(t *testing.T) {
	v := getGlobal(t, `
		func reduce(t, f, init) {
			acc := init
			for i := 1; i <= #t; i++ {
				acc = f(acc, t[i])
			}
			return acc
		}
		result := reduce({1, 2, 3, 4, 5}, func(a, b) { return a + b }, 0)
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

// --- Closure captures table reference ---

func TestClosureCapturesTableRef(t *testing.T) {
	interp := runProgram(t, `
		func makeAppender() {
			items := {}
			add := func(val) {
				table.insert(items, val)
			}
			getAll := func() {
				return items
			}
			return add, getAll
		}
		add, getAll := makeAppender()
		add("a")
		add("b")
		add("c")
		items := getAll()
	`)
	tbl := interp.GetGlobal("items").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 items, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected items[1]='a', got %v", tbl.RawGet(IntValue(1)))
	}
	if tbl.RawGet(IntValue(3)).Str() != "c" {
		t.Errorf("expected items[3]='c', got %v", tbl.RawGet(IntValue(3)))
	}
}

// --- Deep closure chain ---

func TestClosureDeepChain(t *testing.T) {
	v := getGlobal(t, `
		func level1() {
			a := 1
			func level2() {
				b := 2
				func level3() {
					c := 3
					return func() {
						return a + b + c
					}
				}
				return level3()
			}
			return level2()
		}
		f := level1()
		result := f()
	`, "result")
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected 6, got %v", v)
	}
}

// --- Closure and method pattern ---

func TestClosureMethodPattern(t *testing.T) {
	v := getGlobal(t, `
		func makeObj(name) {
			self := {name: name}
			self.greet = func() {
				return "Hello, " .. self.name
			}
			return self
		}
		obj := makeObj("World")
		result := obj.greet()
	`, "result")
	if v.Str() != "Hello, World" {
		t.Errorf("expected 'Hello, World', got %v", v)
	}
}

// --- Closure with default values pattern ---

func TestClosureDefaultValues(t *testing.T) {
	v := getGlobal(t, `
		func withDefaults(opts) {
			defaults := {x: 0, y: 0, label: "none"}
			for k, v := range defaults {
				if opts[k] == nil {
					opts[k] = v
				}
			}
			return opts
		}
		result := withDefaults({x: 10})
	`, "result")
	tbl := v.Table()
	if tbl.RawGet(StringValue("x")).Int() != 10 {
		t.Errorf("expected x=10")
	}
	if tbl.RawGet(StringValue("y")).Int() != 0 {
		t.Errorf("expected y=0")
	}
	if tbl.RawGet(StringValue("label")).Str() != "none" {
		t.Errorf("expected label='none'")
	}
}

// --- IIFE (Immediately Invoked Function Expression) ---

func TestIIFE(t *testing.T) {
	v := getGlobal(t, `
		result := (func(x) { return x * x })(7)
	`, "result")
	if !v.IsInt() || v.Int() != 49 {
		t.Errorf("expected 49, got %v", v)
	}
}

// --- Closure over boolean ---

func TestClosureOverBoolean(t *testing.T) {
	interp := runProgram(t, `
		func makeToggle() {
			state := false
			return func() {
				state = !state
				return state
			}
		}
		toggle := makeToggle()
		r1 := toggle()
		r2 := toggle()
		r3 := toggle()
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected r1=true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected r2=false")
	}
	if !interp.GetGlobal("r3").Bool() {
		t.Errorf("expected r3=true")
	}
}

// --- Closures with string upvalue ---

func TestClosureStringAccumulator(t *testing.T) {
	v := getGlobal(t, `
		func makeStringBuilder() {
			s := ""
			return func(part) {
				s = s .. part
				return s
			}
		}
		builder := makeStringBuilder()
		builder("Hello")
		builder(", ")
		result := builder("World!")
	`, "result")
	if v.Str() != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", v.Str())
	}
}

// --- Pipeline pattern ---

func TestClosurePipeline(t *testing.T) {
	v := getGlobal(t, `
		func pipe(val, ...) {
			result := val
			for i := 1; i <= #...; i++ {
				result = ...[i](result)
			}
			return result
		}
		result := pipe(5,
			func(x) { return x * 2 },
			func(x) { return x + 3 },
			func(x) { return x * x }
		)
	`, "result")
	// 5*2=10, 10+3=13, 13*13=169
	if !v.IsInt() || v.Int() != 169 {
		t.Errorf("expected 169, got %v", v)
	}
}
