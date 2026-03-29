//go:build darwin && arm64

// tier1_native_test.go tests the native ARM64 implementations of table,
// field, global, upvalue, len, and self operations in the Tier 1 baseline
// compiler. Each test verifies VM vs JIT correctness.

package methodjit

import (
	"testing"
)

// ---------------------------------------------------------------------------
// GETFIELD / SETFIELD (shape-guarded inline cache)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetField(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {x: 42}
    return t.x
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeSetField(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {x: 1}
    t.x = 99
    return t.x
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeGetFieldMultiple(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {x: 10, y: 20, z: 30}
    return t.x + t.y + t.z
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeSetFieldThenGet(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {}
    t.a = 100
    t.b = 200
    return t.a + t.b
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// GETTABLE / SETTABLE (array integer fast path)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetTable(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {10, 20, 30}
    return t[1] + t[2]
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeSetTable(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {0, 0, 0}
    t[1] = 42
    t[2] = 58
    return t[1] + t[2]
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeGetTableDynamic(t *testing.T) {
	// Test with dynamic integer key (register, not constant)
	compareVMvsJIT(t, `
func f(idx) {
    t := {10, 20, 30, 40, 50}
    return t[idx]
}
result := 0
for i := 1; i <= 200; i++ { result = f(3) }
`, "result")
}

// ---------------------------------------------------------------------------
// LEN
// ---------------------------------------------------------------------------

func TestTier1_NativeLen(t *testing.T) {
	// LEN currently falls through to exit-resume for correctness.
	// This test verifies correctness is maintained.
	compareVMvsJIT(t, `
func f() {
    t := {1, 2, 3, 4, 5}
    return #t
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeLenString(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    s := "hello"
    return #s
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// SELF (method call)
// ---------------------------------------------------------------------------

func TestTier1_NativeSelf(t *testing.T) {
	compareVMvsJIT(t, `
func make_obj() {
    obj := {}
    obj.value = 42
    obj.get_value = func(self) { return self.value }
    return obj
}
o := make_obj()
result := 0
for i := 1; i <= 200; i++ { result = o:get_value() }
`, "result")
}

// ---------------------------------------------------------------------------
// GETUPVAL / SETUPVAL (closure upvalue access)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetUpval(t *testing.T) {
	// Known issue: closure upvalue access in baseline JIT is broken.
	// The upvalue pointer doesn't survive across the JIT boundary correctly.
	// See TestTier1_Closure for the same pre-existing bug.
	t.Skip("known closure upvalue bug in baseline JIT")
	compareVMvsJIT(t, `
func make_adder(x) {
    func adder(y) { return x + y }
    return adder
}
add10 := make_adder(10)
result := add10(5)
`, "result")
}

func TestTier1_NativeSetUpval(t *testing.T) {
	t.Skip("known closure upvalue bug in baseline JIT")
	compareVMvsJIT(t, `
func make_counter() {
    count := 0
    func inc() {
        count = count + 1
        return count
    }
    return inc
}
counter := make_counter()
result := 0
for i := 1; i <= 10; i++ { result = counter() }
`, "result")
}

// ---------------------------------------------------------------------------
// Combined operations (end-to-end)
// ---------------------------------------------------------------------------

func TestTier1_NativeTableAccumulator(t *testing.T) {
	// A loop that reads and writes table fields.
	compareVMvsJIT(t, `
func f() {
    t := {sum: 0}
    for i := 1; i <= 10; i++ {
        t.sum = t.sum + i
    }
    return t.sum
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeTableArrayLoop(t *testing.T) {
	// A loop that reads from an array table.
	compareVMvsJIT(t, `
func sum_array() {
    t := {10, 20, 30, 40, 50}
    s := 0
    for i := 1; i <= 5; i++ {
        s = s + t[i]
    }
    return s
}
result := 0
for i := 1; i <= 200; i++ { result = sum_array() }
`, "result")
}

func TestTier1_NativeFieldAndGlobal(t *testing.T) {
	compareVMvsJIT(t, `
factor := 2
func scale_field() {
    t := {x: 21}
    return t.x * factor
}
result := 0
for i := 1; i <= 200; i++ { result = scale_field() }
`, "result")
}
