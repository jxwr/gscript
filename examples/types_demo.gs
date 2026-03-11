// types_demo.gs - Demonstrates all types, type checking, and conversions
// GScript has: nil, bool, int, float, string, table, function, coroutine

print("=== GScript Type System Demo ===")
print()

// --- Nil ---
print("--- nil ---")
x := nil
print("x =", x, "  type:", type(x))
print()

// --- Booleans ---
print("--- bool ---")
t := true
f := false
print("t =", t, "  type:", type(t))
print("f =", f, "  type:", type(f))
print("!t =", !t, "  !f =", !f)
print()

// --- Integers ---
print("--- int ---")
n := 42
print("n =", n, "  type:", type(n))
print("math.type(n) =", math.type(n))
print()

// --- Floats ---
print("--- float ---")
pi := 3.14159
print("pi =", pi, "  type:", type(pi))
print("math.type(pi) =", math.type(pi))
print()

// --- Strings ---
print("--- string ---")
s := "Hello, GScript!"
print("s =", s, "  type:", type(s))
print("string.len(s) =", string.len(s))
print("string.upper(s) =", string.upper(s))
print()

// --- Tables (arrays and maps) ---
print("--- table ---")
arr := {10, 20, 30}
print("arr =", arr, "  type:", type(arr))
print("#arr =", #arr)

dict := {name: "Alice", age: 30}
print("dict.name =", dict.name, "  dict.age =", dict.age)
print()

// --- Functions ---
print("--- function ---")
add := func(a, b) { return a + b }
print("add =", add, "  type:", type(add))
print("add(3, 4) =", add(3, 4))
print()

// --- Coroutines ---
print("--- coroutine ---")
co := coroutine.create(func() {
    coroutine.yield(1)
    coroutine.yield(2)
    return 3
})
print("co =", co, "  type:", type(co))
print("status:", coroutine.status(co))
ok, val := coroutine.resume(co)
print("resume ->", val, "  status:", coroutine.status(co))
ok, val = coroutine.resume(co)
print("resume ->", val, "  status:", coroutine.status(co))
ok, val = coroutine.resume(co)
print("resume ->", val, "  status:", coroutine.status(co))
print()

// --- Type Conversion ---
print("=== Type Conversions ===")
print()

// tostring
print("--- tostring() ---")
print("tostring(42) =", tostring(42))
print("tostring(3.14) =", tostring(3.14))
print("tostring(true) =", tostring(true))
print("tostring(nil) =", tostring(nil))
print()

// tonumber
print("--- tonumber() ---")
print("tonumber(\"42\") =", tonumber("42"))
print("tonumber(\"3.14\") =", tonumber("3.14"))
print("tonumber(\"hello\") =", tonumber("hello"))
print("tonumber(true) =", tonumber(true))
print()

// --- Dynamic Typing ---
print("=== Dynamic Typing in Action ===")
print()

// A variable can hold any type
v := 42
print("v =", v, "  type:", type(v))
v = "now I'm a string"
print("v =", v, "  type:", type(v))
v = true
print("v =", v, "  type:", type(v))
v = {1, 2, 3}
print("v =", v, "  type:", type(v))
v = func() { return "I'm a function" }
print("v =", v, "  type:", type(v))
print()

// --- Truthiness ---
print("=== Truthiness Rules ===")
print("In GScript, only nil and false are falsy. Everything else is truthy.")
print()
values := {nil, false, true, 0, 1, "", "hello", {}}
names := {"nil", "false", "true", "0", "1", "\"\"", "\"hello\"", "{}"}
for i := 1; i <= #values; i++ {
    label := names[i]
    if values[i] {
        print("  " .. label .. " is truthy")
    } else {
        print("  " .. label .. " is falsy")
    }
}
print()

// --- Type checking pattern ---
print("=== Type Checking Pattern ===")
func describe(val) {
    t := type(val)
    if t == "nil" {
        return "nothing"
    } elseif t == "number" || t == "int" || t == "float" {
        if val > 0 { return "positive number" }
        elseif val < 0 { return "negative number" }
        return "zero"
    } elseif t == "string" {
        return "string of length " .. #val
    } elseif t == "table" {
        return "table with " .. #val .. " array elements"
    } elseif t == "function" {
        return "a callable function"
    } else {
        return "something of type " .. t
    }
}

print("describe(42) =", describe(42))
print("describe(-5) =", describe(-5))
print("describe(0) =", describe(0))
print("describe(\"hi\") =", describe("hi"))
print("describe({1,2,3}) =", describe({1, 2, 3}))
print("describe(print) =", describe(print))
print("describe(nil) =", describe(nil))
