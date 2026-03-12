package runtime

import (
	"testing"
)

// containerInterp creates an interpreter with the container library registered.
func containerInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	interp := New()
	interp.globals.Define("container", TableValue(buildContainerLib(interp)))
	tokens, err := lexerNew(src)
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parserNew(tokens)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	return interp
}

// ==================================================================
// Set tests
// ==================================================================

func TestContainerSetNew(t *testing.T) {
	interp := containerInterp(t, `
		s := container.setNew(1, 2, 3)
		has1 := container.setHas(s, 1)
		has2 := container.setHas(s, 2)
		has3 := container.setHas(s, 3)
		has4 := container.setHas(s, 4)
	`)
	if !interp.GetGlobal("has1").Bool() { t.Error("expected has 1") }
	if !interp.GetGlobal("has2").Bool() { t.Error("expected has 2") }
	if !interp.GetGlobal("has3").Bool() { t.Error("expected has 3") }
	if interp.GetGlobal("has4").Bool() { t.Error("should not have 4") }
}

func TestContainerSetAdd(t *testing.T) {
	interp := containerInterp(t, `
		s := container.setNew()
		container.setAdd(s, "hello")
		result := container.setHas(s, "hello")
	`)
	if !interp.GetGlobal("result").Bool() { t.Error("expected has hello") }
}

func TestContainerSetRemove(t *testing.T) {
	interp := containerInterp(t, `
		s := container.setNew(1, 2, 3)
		container.setRemove(s, 2)
		has2 := container.setHas(s, 2)
		has1 := container.setHas(s, 1)
	`)
	if interp.GetGlobal("has2").Bool() { t.Error("2 should be removed") }
	if !interp.GetGlobal("has1").Bool() { t.Error("1 should remain") }
}

func TestContainerSetSize(t *testing.T) {
	interp := containerInterp(t, `
		s := container.setNew(1, 2, 3)
		size := container.setSize(s)
	`)
	if interp.GetGlobal("size").Int() != 3 { t.Errorf("expected 3, got %d", interp.GetGlobal("size").Int()) }
}

func TestContainerSetUnion(t *testing.T) {
	interp := containerInterp(t, `
		a := container.setNew(1, 2, 3)
		b := container.setNew(3, 4, 5)
		u := container.setUnion(a, b)
		size := container.setSize(u)
		has1 := container.setHas(u, 1)
		has5 := container.setHas(u, 5)
	`)
	if interp.GetGlobal("size").Int() != 5 { t.Errorf("expected 5, got %d", interp.GetGlobal("size").Int()) }
	if !interp.GetGlobal("has1").Bool() { t.Error("union should have 1") }
	if !interp.GetGlobal("has5").Bool() { t.Error("union should have 5") }
}

func TestContainerSetIntersect(t *testing.T) {
	interp := containerInterp(t, `
		a := container.setNew(1, 2, 3)
		b := container.setNew(2, 3, 4)
		i := container.setIntersect(a, b)
		size := container.setSize(i)
		has2 := container.setHas(i, 2)
		has3 := container.setHas(i, 3)
		has1 := container.setHas(i, 1)
	`)
	if interp.GetGlobal("size").Int() != 2 { t.Errorf("expected 2, got %d", interp.GetGlobal("size").Int()) }
	if !interp.GetGlobal("has2").Bool() { t.Error("intersect should have 2") }
	if !interp.GetGlobal("has3").Bool() { t.Error("intersect should have 3") }
	if interp.GetGlobal("has1").Bool() { t.Error("intersect should not have 1") }
}

func TestContainerSetDifference(t *testing.T) {
	interp := containerInterp(t, `
		a := container.setNew(1, 2, 3)
		b := container.setNew(2, 3, 4)
		d := container.setDifference(a, b)
		size := container.setSize(d)
		has1 := container.setHas(d, 1)
		has2 := container.setHas(d, 2)
	`)
	if interp.GetGlobal("size").Int() != 1 { t.Errorf("expected 1, got %d", interp.GetGlobal("size").Int()) }
	if !interp.GetGlobal("has1").Bool() { t.Error("difference should have 1") }
	if interp.GetGlobal("has2").Bool() { t.Error("difference should not have 2") }
}

func TestContainerSetToArray(t *testing.T) {
	interp := containerInterp(t, `
		s := container.setNew(10, 20, 30)
		arr := container.setToArray(s)
		count := #arr
	`)
	count := interp.GetGlobal("count")
	if count.Int() != 3 { t.Errorf("expected 3 elements, got %d", count.Int()) }
}

// ==================================================================
// Queue tests
// ==================================================================

func TestContainerQueue(t *testing.T) {
	interp := containerInterp(t, `
		q := container.queueNew()
		container.queuePush(q, "a")
		container.queuePush(q, "b")
		container.queuePush(q, "c")
		first := container.queuePop(q)
		second := container.queuePop(q)
		size := container.queueSize(q)
	`)
	if interp.GetGlobal("first").Str() != "a" { t.Error("expected a") }
	if interp.GetGlobal("second").Str() != "b" { t.Error("expected b") }
	if interp.GetGlobal("size").Int() != 1 { t.Error("expected 1") }
}

func TestContainerQueueEmpty(t *testing.T) {
	interp := containerInterp(t, `
		q := container.queueNew()
		empty := container.queueEmpty(q)
		result := container.queuePop(q)
	`)
	if !interp.GetGlobal("empty").Bool() { t.Error("new queue should be empty") }
	if !interp.GetGlobal("result").IsNil() { t.Error("pop from empty should return nil") }
}

func TestContainerQueuePeek(t *testing.T) {
	interp := containerInterp(t, `
		q := container.queueNew()
		container.queuePush(q, 42)
		peeked := container.queuePeek(q)
		size := container.queueSize(q)
	`)
	if interp.GetGlobal("peeked").Int() != 42 { t.Error("expected 42") }
	if interp.GetGlobal("size").Int() != 1 { t.Error("peek should not remove element") }
}

func TestContainerQueueDeque(t *testing.T) {
	interp := containerInterp(t, `
		q := container.queueNew()
		container.queuePush(q, "b")
		container.queuePushFront(q, "a")
		container.queuePush(q, "c")
		first := container.queuePop(q)
		last := container.queuePopBack(q)
		mid := container.queuePop(q)
	`)
	if interp.GetGlobal("first").Str() != "a" { t.Error("expected a from front") }
	if interp.GetGlobal("last").Str() != "c" { t.Error("expected c from back") }
	if interp.GetGlobal("mid").Str() != "b" { t.Error("expected b remaining") }
}

func TestContainerQueueSize(t *testing.T) {
	interp := containerInterp(t, `
		q := container.queueNew()
		s0 := container.queueSize(q)
		container.queuePush(q, 1)
		container.queuePush(q, 2)
		s2 := container.queueSize(q)
		container.queuePop(q)
		s1 := container.queueSize(q)
	`)
	if interp.GetGlobal("s0").Int() != 0 { t.Error("expected 0") }
	if interp.GetGlobal("s2").Int() != 2 { t.Error("expected 2") }
	if interp.GetGlobal("s1").Int() != 1 { t.Error("expected 1") }
}

// ==================================================================
// Heap (priority queue) tests
// ==================================================================

func TestContainerHeapBasic(t *testing.T) {
	interp := containerInterp(t, `
		h := container.heapNew()
		h.push(30)
		h.push(10)
		h.push(20)
		first := h.pop()
		second := h.pop()
		third := h.pop()
	`)
	if interp.GetGlobal("first").Int() != 10 { t.Errorf("expected 10, got %d", interp.GetGlobal("first").Int()) }
	if interp.GetGlobal("second").Int() != 20 { t.Errorf("expected 20, got %d", interp.GetGlobal("second").Int()) }
	if interp.GetGlobal("third").Int() != 30 { t.Errorf("expected 30, got %d", interp.GetGlobal("third").Int()) }
}

func TestContainerHeapPeek(t *testing.T) {
	interp := containerInterp(t, `
		h := container.heapNew()
		h.push(5)
		h.push(3)
		h.push(7)
		peeked := h.peek()
		size := h.size()
	`)
	if interp.GetGlobal("peeked").Int() != 3 { t.Errorf("expected 3, got %d", interp.GetGlobal("peeked").Int()) }
	if interp.GetGlobal("size").Int() != 3 { t.Error("peek should not remove") }
}

func TestContainerHeapEmpty(t *testing.T) {
	interp := containerInterp(t, `
		h := container.heapNew()
		empty := h.empty()
		result := h.pop()
		h.push(1)
		notEmpty := h.empty()
	`)
	if !interp.GetGlobal("empty").Bool() { t.Error("new heap should be empty") }
	if !interp.GetGlobal("result").IsNil() { t.Error("pop from empty should return nil") }
	if interp.GetGlobal("notEmpty").Bool() { t.Error("should not be empty after push") }
}

func TestContainerHeapStrings(t *testing.T) {
	interp := containerInterp(t, `
		h := container.heapNew()
		h.push("cherry")
		h.push("apple")
		h.push("banana")
		first := h.pop()
		second := h.pop()
	`)
	if interp.GetGlobal("first").Str() != "apple" { t.Errorf("expected apple, got %s", interp.GetGlobal("first").Str()) }
	if interp.GetGlobal("second").Str() != "banana" { t.Errorf("expected banana, got %s", interp.GetGlobal("second").Str()) }
}

// ==================================================================
// Stack tests
// ==================================================================

func TestContainerStack(t *testing.T) {
	interp := containerInterp(t, `
		s := container.stackNew()
		container.stackPush(s, "a")
		container.stackPush(s, "b")
		container.stackPush(s, "c")
		first := container.stackPop(s)
		second := container.stackPop(s)
		size := container.stackSize(s)
	`)
	if interp.GetGlobal("first").Str() != "c" { t.Error("expected c (LIFO)") }
	if interp.GetGlobal("second").Str() != "b" { t.Error("expected b") }
	if interp.GetGlobal("size").Int() != 1 { t.Error("expected 1") }
}

func TestContainerStackEmpty(t *testing.T) {
	interp := containerInterp(t, `
		s := container.stackNew()
		empty := container.stackEmpty(s)
		result := container.stackPop(s)
	`)
	if !interp.GetGlobal("empty").Bool() { t.Error("new stack should be empty") }
	if !interp.GetGlobal("result").IsNil() { t.Error("pop from empty should return nil") }
}

func TestContainerStackPeek(t *testing.T) {
	interp := containerInterp(t, `
		s := container.stackNew()
		container.stackPush(s, 42)
		peeked := container.stackPeek(s)
		size := container.stackSize(s)
	`)
	if interp.GetGlobal("peeked").Int() != 42 { t.Error("expected 42") }
	if interp.GetGlobal("size").Int() != 1 { t.Error("peek should not remove") }
}
