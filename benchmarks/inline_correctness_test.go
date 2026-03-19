package benchmarks

import (
	"testing"

	gs "github.com/gscript/gscript/gscript"
)

func TestInlineCallCorrectness(t *testing.T) {
	v := gs.New(gs.WithJIT())
	err := v.Exec(`
func add(a, b) {
    return a + b
}
func callMany() {
    x := 0
    for i := 0; i < 10000; i++ {
        x = add(x, 1)
    }
    return x
}
for i := 1; i <= 15; i++ { callMany() }
`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := v.Call("callMany")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Fatal("no results")
	}
	result, ok := results[0].(int64)
	if !ok {
		t.Fatalf("expected int64, got %T: %v", results[0], results[0])
	}
	if result != 10000 {
		t.Fatalf("expected 10000, got %d", result)
	}
}

func TestInlineCallSubCorrectness(t *testing.T) {
	v := gs.New(gs.WithJIT())
	err := v.Exec(`
func sub(a, b) {
    return a - b
}
func callManySub() {
    x := 10000
    for i := 0; i < 10000; i++ {
        x = sub(x, 1)
    }
    return x
}
for i := 1; i <= 15; i++ { callManySub() }
`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := v.Call("callManySub")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Fatal("no results")
	}
	result, ok := results[0].(int64)
	if !ok {
		t.Fatalf("expected int64, got %T: %v", results[0], results[0])
	}
	if result != 0 {
		t.Fatalf("expected 0, got %d", result)
	}
}

func TestInlineCallMulCorrectness(t *testing.T) {
	v := gs.New(gs.WithJIT())
	err := v.Exec(`
func mul(a, b) {
    return a * b
}
func callManyMul() {
    x := 1
    for i := 0; i < 20; i++ {
        x = mul(x, 2)
    }
    return x
}
for i := 1; i <= 15; i++ { callManyMul() }
`)
	if err != nil {
		t.Fatal(err)
	}
	results, err := v.Call("callManyMul")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Fatal("no results")
	}
	result, ok := results[0].(int64)
	if !ok {
		t.Fatalf("expected int64, got %T: %v", results[0], results[0])
	}
	if result != 1048576 { // 2^20
		t.Fatalf("expected 1048576, got %d", result)
	}
}
