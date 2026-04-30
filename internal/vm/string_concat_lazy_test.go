package vm

import "testing"

func TestVMConcatLoopLazyLenAndMaterialize(t *testing.T) {
	g := compileAndRun(t, `
s := ""
for i := 1; i <= 200; i++ {
    s = s .. "x"
}
n := #s
tail := string.sub(s, 198, 200)
`)
	expectGlobalInt(t, g, "n", 200)
	expectGlobalString(t, g, "tail", "xxx")
}

func TestVMConcatLazyEscapeKeepsIntermediateValues(t *testing.T) {
	g := compileAndRun(t, `
s := ""
saved := {}
for i := 1; i <= 4; i++ {
    saved[i] = s
    s = s .. "x"
}
r1 := saved[2]
r2 := saved[4]
r3 := s
`)
	expectGlobalString(t, g, "r1", "x")
	expectGlobalString(t, g, "r2", "xxx")
	expectGlobalString(t, g, "r3", "xxxx")
}

func TestVMConcatFallbackMetamethod(t *testing.T) {
	g := compileAndRun(t, `
mt := {__concat: func(a, b) { return a.val .. b.val }}
a := {val: "hello"}
b := {val: " world"}
setmetatable(a, mt)
r := a .. b
`)
	expectGlobalString(t, g, "r", "hello world")
}

func TestVMConcatLazyCompareLenAndTableKeyMaterialization(t *testing.T) {
	g := compileAndRun(t, `
s := ""
for i := 1; i <= 70; i++ {
    s = s .. "a"
}
same := s == string.rep("a", 70)
less := s < (s .. "b")
n := #s
t := {}
t[s] = 42
lookup := t[string.rep("a", 70)]
`)
	expectGlobalBool(t, g, "same", true)
	expectGlobalBool(t, g, "less", true)
	expectGlobalInt(t, g, "n", 70)
	expectGlobalInt(t, g, "lookup", 42)
}

func TestVMConcatLazyNonStringCoercionAndErrors(t *testing.T) {
	g := compileAndRun(t, `
s := ""
for i := 1; i <= 12; i++ {
    s = s .. i
}
n := #s
tail := string.sub(s, n - 1, n)
`)
	expectGlobalInt(t, g, "n", 15)
	expectGlobalString(t, g, "tail", "12")

	if err := compileAndRunExpectError(t, `r := "x" .. true`); err == nil {
		t.Fatal("expected bool concat to fail")
	}
}
