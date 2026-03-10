package runtime

import (
	"testing"
)

// ==================================================================
// Metatable tests
// ==================================================================

// 1. __index as table (inheritance / prototype chain)
func TestMetatableIndexTable(t *testing.T) {
	src := `
proto := {greet: func(self) { return "hello from " .. self.name }}
obj := {name: "alice"}
setmetatable(obj, {__index: proto})
result := obj.greet(obj)
`
	v := getGlobal(t, src, "result")
	if !v.IsString() || v.Str() != "hello from alice" {
		t.Errorf("expected 'hello from alice', got %v", v)
	}
}

// 2. __index as function
func TestMetatableIndexFunc(t *testing.T) {
	src := `
t := {}
setmetatable(t, {
	__index: func(table, key) {
		return "missing:" .. key
	}
})
result := t.foo
`
	v := getGlobal(t, src, "result")
	if !v.IsString() || v.Str() != "missing:foo" {
		t.Errorf("expected 'missing:foo', got %v", v)
	}
}

// 3. __index chain (multi-level prototype)
func TestMetatableIndexChain(t *testing.T) {
	src := `
base := {kind: "animal"}
mid := {}
setmetatable(mid, {__index: base})
child := {}
setmetatable(child, {__index: mid})
result := child.kind
`
	v := getGlobal(t, src, "result")
	if !v.IsString() || v.Str() != "animal" {
		t.Errorf("expected 'animal', got %v", v)
	}
}

// 4. __index does not trigger when key exists
func TestMetatableIndexExisting(t *testing.T) {
	src := `
t := {x: 42}
setmetatable(t, {__index: func(table, key) { return "missing" }})
result := t.x
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// 5. __newindex as function
func TestMetatableNewindex(t *testing.T) {
	src := `
log := {}
t := {}
setmetatable(t, {
	__newindex: func(table, key, val) {
		rawset(log, key, val)
	}
})
t.x = 42
result := log.x
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// 6. __newindex does not trigger for existing keys
func TestMetatableNewindexExisting(t *testing.T) {
	src := `
called := false
t := {x: 1}
setmetatable(t, {
	__newindex: func(table, key, val) {
		called = true
	}
})
t.x = 99
r1 := t.x
r2 := called
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if !r1.IsInt() || r1.Int() != 99 {
		t.Errorf("expected r1=99, got %v", r1)
	}
	if !r2.IsBool() || r2.Bool() {
		t.Errorf("expected r2=false, got %v", r2)
	}
}

// 7. __newindex as table (redirect writes)
func TestMetatableNewindexTable(t *testing.T) {
	src := `
store := {}
t := {}
setmetatable(t, {__newindex: store})
t.x = 42
result := store.x
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// 8. __add metamethod
func TestMetatableAdd(t *testing.T) {
	src := `
mt := {
	__add: func(a, b) {
		return {x: a.x + b.x, y: a.y + b.y}
	}
}
v1 := {x: 1, y: 2}
v2 := {x: 3, y: 4}
setmetatable(v1, mt)
setmetatable(v2, mt)
v3 := v1 + v2
result := v3.x * 10 + v3.y
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 46 {
		t.Errorf("expected 46, got %v", v)
	}
}

// 9. __sub metamethod
func TestMetatableSub(t *testing.T) {
	src := `
mt := {__sub: func(a, b) { return {val: a.val - b.val} }}
a := {val: 10}
b := {val: 3}
setmetatable(a, mt)
result := (a - b).val
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected 7, got %v", v)
	}
}

// 10. __mul metamethod
func TestMetatableMul(t *testing.T) {
	src := `
mt := {__mul: func(a, b) { return {val: a.val * b.val} }}
a := {val: 6}
b := {val: 7}
setmetatable(a, mt)
result := (a * b).val
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// 11. __div metamethod
func TestMetatableDiv(t *testing.T) {
	src := `
mt := {__div: func(a, b) { return {val: a.val / b.val} }}
a := {val: 10}
b := {val: 2}
setmetatable(a, mt)
result := (a / b).val
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

// 12. __mod metamethod
func TestMetatableMod(t *testing.T) {
	src := `
mt := {__mod: func(a, b) { return {val: a.val % b.val} }}
a := {val: 10}
b := {val: 3}
setmetatable(a, mt)
result := (a % b).val
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

// 13. __pow metamethod
func TestMetatablePow(t *testing.T) {
	src := `
mt := {__pow: func(a, b) { return {val: a.val ** b.val} }}
a := {val: 2}
b := {val: 10}
setmetatable(a, mt)
result := (a ** b).val
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 1024 {
		t.Errorf("expected 1024, got %v", v)
	}
}

// 14. __eq metamethod
func TestMetatableEq(t *testing.T) {
	src := `
mt := {__eq: func(a, b) { return a.x == b.x && a.y == b.y }}
v1 := {x: 1, y: 2}
v2 := {x: 1, y: 2}
setmetatable(v1, mt)
setmetatable(v2, mt)
result := v1 == v2
`
	v := getGlobal(t, src, "result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

// 15. __eq metamethod (not equal)
func TestMetatableEqFalse(t *testing.T) {
	src := `
mt := {__eq: func(a, b) { return a.x == b.x }}
v1 := {x: 1}
v2 := {x: 2}
setmetatable(v1, mt)
setmetatable(v2, mt)
result := v1 == v2
`
	v := getGlobal(t, src, "result")
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false, got %v", v)
	}
}

// 16. __eq with != operator
func TestMetatableNeq(t *testing.T) {
	src := `
mt := {__eq: func(a, b) { return a.x == b.x }}
v1 := {x: 1}
v2 := {x: 1}
setmetatable(v1, mt)
setmetatable(v2, mt)
result := v1 != v2
`
	v := getGlobal(t, src, "result")
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false, got %v", v)
	}
}

// 17. __len metamethod
func TestMetatableLen(t *testing.T) {
	src := `
t := {items: {10, 20, 30, 40}}
setmetatable(t, {
	__len: func(self) { return #self.items }
})
result := #t
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 4 {
		t.Errorf("expected 4, got %v", v)
	}
}

// 18. __call metamethod (callable table)
func TestMetatableCall(t *testing.T) {
	src := `
counter := {n: 0}
setmetatable(counter, {
	__call: func(self) {
		self.n = self.n + 1
		return self.n
	}
})
r1 := counter()
r2 := counter()
result := r2
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected 2, got %v", v)
	}
}

// 19. __call with arguments
func TestMetatableCallArgs(t *testing.T) {
	src := `
adder := {base: 10}
setmetatable(adder, {
	__call: func(self, x) {
		return self.base + x
	}
})
result := adder(5)
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

// 20. __concat metamethod
func TestMetatableConcat(t *testing.T) {
	src := `
mt := {__concat: func(a, b) { return a.val .. b.val }}
a := {val: "hello"}
b := {val: " world"}
setmetatable(a, mt)
result := a .. b
`
	v := getGlobal(t, src, "result")
	if !v.IsString() || v.Str() != "hello world" {
		t.Errorf("expected 'hello world', got %v", v)
	}
}

// 21. __lt and __le metamethods
func TestMetatableLtLe(t *testing.T) {
	src := `
mt := {
	__lt: func(a, b) { return a.n < b.n },
	__le: func(a, b) { return a.n <= b.n }
}
a := {n: 1}
b := {n: 2}
setmetatable(a, mt)
setmetatable(b, mt)
r1 := a < b
r2 := b < a
r3 := a <= a
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	r3 := interp.GetGlobal("r3")
	if !r1.IsBool() || !r1.Bool() {
		t.Errorf("expected r1=true, got %v", r1)
	}
	if !r2.IsBool() || r2.Bool() {
		t.Errorf("expected r2=false, got %v", r2)
	}
	if !r3.IsBool() || !r3.Bool() {
		t.Errorf("expected r3=true, got %v", r3)
	}
}

// 22. __gt and __ge via __lt and __le
func TestMetatableGtGe(t *testing.T) {
	src := `
mt := {
	__lt: func(a, b) { return a.n < b.n },
	__le: func(a, b) { return a.n <= b.n }
}
a := {n: 5}
b := {n: 3}
setmetatable(a, mt)
setmetatable(b, mt)
r1 := a > b
r2 := a >= b
r3 := b > a
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	r3 := interp.GetGlobal("r3")
	if !r1.IsBool() || !r1.Bool() {
		t.Errorf("expected r1=true, got %v", r1)
	}
	if !r2.IsBool() || !r2.Bool() {
		t.Errorf("expected r2=true, got %v", r2)
	}
	if !r3.IsBool() || r3.Bool() {
		t.Errorf("expected r3=false, got %v", r3)
	}
}

// 23. __unm (unary minus)
func TestMetatableUnm(t *testing.T) {
	src := `
mt := {__unm: func(a) { return {x: -a.x, y: -a.y} }}
v := {x: 3, y: -4}
setmetatable(v, mt)
neg := -v
result := neg.x * 10 + neg.y
`
	v := getGlobal(t, src, "result")
	// -3 * 10 + 4 = -26
	if !v.IsInt() || v.Int() != -26 {
		t.Errorf("expected -26, got %v", v)
	}
}

// 24. rawget/rawset bypass metamethods
func TestMetatableRawgetRawset(t *testing.T) {
	src := `
t := {}
setmetatable(t, {
	__index: func(self, key) { return "meta" },
	__newindex: func(self, key, val) { }
})
rawset(t, "x", 42)
r1 := rawget(t, "x")
r2 := t.y
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if !r1.IsInt() || r1.Int() != 42 {
		t.Errorf("expected r1=42, got %v", r1)
	}
	if !r2.IsString() || r2.Str() != "meta" {
		t.Errorf("expected r2='meta', got %v", r2)
	}
}

// 25. rawequal bypasses __eq
func TestMetatableRawequal(t *testing.T) {
	src := `
mt := {__eq: func(a, b) { return true }}
v1 := {x: 1}
v2 := {x: 2}
setmetatable(v1, mt)
setmetatable(v2, mt)
r1 := v1 == v2
r2 := rawequal(v1, v2)
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if !r1.IsBool() || !r1.Bool() {
		t.Errorf("expected r1=true (via metamethod), got %v", r1)
	}
	if !r2.IsBool() || r2.Bool() {
		t.Errorf("expected r2=false (raw equality), got %v", r2)
	}
}

// 26. getmetatable / setmetatable
func TestMetatableGetSet(t *testing.T) {
	src := `
t := {}
mt := {__index: {x: 99}}
setmetatable(t, mt)
r1 := t.x
mt2 := getmetatable(t)
r2 := mt2 == mt
setmetatable(t, nil)
r3 := getmetatable(t)
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	r3 := interp.GetGlobal("r3")
	if !r1.IsInt() || r1.Int() != 99 {
		t.Errorf("expected r1=99, got %v", r1)
	}
	if !r2.IsBool() || !r2.Bool() {
		t.Errorf("expected r2=true, got %v", r2)
	}
	if !r3.IsNil() {
		t.Errorf("expected r3=nil, got %v", r3)
	}
}

// 27. OOP-style prototype chain
func TestMetatableOOP(t *testing.T) {
	src := `
Animal := {}
Animal.new = func(name) {
	self := {name: name}
	setmetatable(self, {__index: Animal})
	return self
}
Animal.speak = func(self) {
	return self.name .. " says hello"
}

dog := Animal.new("Rex")
result := dog.speak(dog)
`
	v := getGlobal(t, src, "result")
	if !v.IsString() || v.Str() != "Rex says hello" {
		t.Errorf("expected 'Rex says hello', got %v", v)
	}
}

// 28. OOP with method syntax (:)
func TestMetatableOOPMethodCall(t *testing.T) {
	src := `
Vec := {}
Vec.new = func(x, y) {
	self := {x: x, y: y}
	setmetatable(self, {__index: Vec})
	return self
}
Vec.length = func(self) {
	return self.x * self.x + self.y * self.y
}

v := Vec.new(3, 4)
result := v:length()
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 25 {
		t.Errorf("expected 25, got %v", v)
	}
}

// 29. Metamethods - try right operand when left has no metamethod
func TestMetatableArithRightOperand(t *testing.T) {
	src := `
mt := {__add: func(a, b) { return {val: a.val + b.val} }}
a := {val: 10}
b := {val: 5}
setmetatable(b, mt)
result := (a + b).val
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

// 30. setmetatable returns the table
func TestMetatableSetReturns(t *testing.T) {
	src := `
t := {x: 42}
r := setmetatable(t, {})
result := r.x
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// 31. getmetatable on non-table returns nil
func TestMetatableGetNonTable(t *testing.T) {
	src := `result := getmetatable(42)`
	v := getGlobal(t, src, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

// 32. __call chained (callable table returns callable table)
func TestMetatableCallChained(t *testing.T) {
	src := `
mt := {__call: func(self, x) { return x * 2 }}
t := {}
setmetatable(t, mt)
result := t(21)
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

// 33. Combination: __index + __newindex for proxy table
func TestMetatableProxy(t *testing.T) {
	src := `
backing := {x: 10, y: 20}
proxy := {}
setmetatable(proxy, {
	__index: func(self, key) {
		return rawget(backing, key)
	},
	__newindex: func(self, key, val) {
		rawset(backing, key, val)
	}
})
r1 := proxy.x
proxy.x = 100
r2 := backing.x
`
	interp := runProgram(t, src)
	r1 := interp.GetGlobal("r1")
	r2 := interp.GetGlobal("r2")
	if !r1.IsInt() || r1.Int() != 10 {
		t.Errorf("expected r1=10, got %v", r1)
	}
	if !r2.IsInt() || r2.Int() != 100 {
		t.Errorf("expected r2=100, got %v", r2)
	}
}

// 34. __len on table without array part
func TestMetatableLenCustom(t *testing.T) {
	src := `
t := {a: 1, b: 2, c: 3}
setmetatable(t, {__len: func(self) { return 99 }})
result := #t
`
	v := getGlobal(t, src, "result")
	if !v.IsInt() || v.Int() != 99 {
		t.Errorf("expected 99, got %v", v)
	}
}

// 35. Multiple metamethods on same table
func TestMetatableMultiple(t *testing.T) {
	src := `
mt := {
	__add: func(a, b) { return {n: a.n + b.n} },
	__sub: func(a, b) { return {n: a.n - b.n} },
	__eq: func(a, b) { return a.n == b.n },
	__len: func(a) { return a.n },
	__unm: func(a) { return {n: -a.n} }
}
a := {n: 10}
b := {n: 3}
setmetatable(a, mt)
setmetatable(b, mt)

sum := a + b
diff := a - b
r_add := sum.n
r_sub := diff.n
r_eq := a == a
r_len := #a
neg := -a
r_unm := neg.n
`
	interp := runProgram(t, src)

	if v := interp.GetGlobal("r_add"); !v.IsInt() || v.Int() != 13 {
		t.Errorf("expected r_add=13, got %v", v)
	}
	if v := interp.GetGlobal("r_sub"); !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected r_sub=7, got %v", v)
	}
	if v := interp.GetGlobal("r_eq"); !v.IsBool() || !v.Bool() {
		t.Errorf("expected r_eq=true, got %v", v)
	}
	if v := interp.GetGlobal("r_len"); !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected r_len=10, got %v", v)
	}
	if v := interp.GetGlobal("r_unm"); !v.IsInt() || v.Int() != -10 {
		t.Errorf("expected r_unm=-10, got %v", v)
	}
}
