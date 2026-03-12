package runtime

import (
	"container/heap"
	"fmt"
)

// buildContainerLib creates the "container" standard library table.
// Provides set, queue (deque), and priority queue data structures.
// Inspired by Odin's container package (queue, priority_queue, etc.).
func buildContainerLib(interp *Interpreter) *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "container." + name,
			Fn:   fn,
		}))
	}

	// ---------------------------------------------------------------
	// Set operations (using tables as sets)
	// ---------------------------------------------------------------

	// container.setNew(...) -> create a new set from values
	// Sets are tables with values as keys mapped to true
	set("setNew", func(args []Value) ([]Value, error) {
		s := NewTable()
		for _, v := range args {
			s.RawSet(v, BoolValue(true))
		}
		return []Value{TableValue(s)}, nil
	})

	// container.setAdd(set, value) -> add value to set
	set("setAdd", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.setAdd' (set and value expected)")
		}
		args[0].Table().RawSet(args[1], BoolValue(true))
		return nil, nil
	})

	// container.setRemove(set, value) -> remove value from set
	set("setRemove", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.setRemove' (set and value expected)")
		}
		args[0].Table().RawSet(args[1], NilValue())
		return nil, nil
	})

	// container.setHas(set, value) -> bool
	set("setHas", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.setHas' (set and value expected)")
		}
		v := args[0].Table().RawGet(args[1])
		return []Value{BoolValue(!v.IsNil())}, nil
	})

	// container.setSize(set) -> int (count of elements)
	set("setSize", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.setSize' (table expected)")
		}
		tbl := args[0].Table()
		count := int64(0)
		key := NilValue()
		for {
			k, _, ok := tbl.Next(key)
			if !ok {
				break
			}
			count++
			key = k
		}
		return []Value{IntValue(count)}, nil
	})

	// container.setUnion(a, b) -> new set containing elements in a or b
	set("setUnion", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.setUnion' (two sets expected)")
		}
		result := NewTable()
		// Add all from a
		key := NilValue()
		for {
			k, _, ok := args[0].Table().Next(key)
			if !ok {
				break
			}
			result.RawSet(k, BoolValue(true))
			key = k
		}
		// Add all from b
		key = NilValue()
		for {
			k, _, ok := args[1].Table().Next(key)
			if !ok {
				break
			}
			result.RawSet(k, BoolValue(true))
			key = k
		}
		return []Value{TableValue(result)}, nil
	})

	// container.setIntersect(a, b) -> new set containing elements in both a and b
	set("setIntersect", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.setIntersect' (two sets expected)")
		}
		result := NewTable()
		bTbl := args[1].Table()
		key := NilValue()
		for {
			k, _, ok := args[0].Table().Next(key)
			if !ok {
				break
			}
			if !bTbl.RawGet(k).IsNil() {
				result.RawSet(k, BoolValue(true))
			}
			key = k
		}
		return []Value{TableValue(result)}, nil
	})

	// container.setDifference(a, b) -> new set containing elements in a but not in b
	set("setDifference", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.setDifference' (two sets expected)")
		}
		result := NewTable()
		bTbl := args[1].Table()
		key := NilValue()
		for {
			k, _, ok := args[0].Table().Next(key)
			if !ok {
				break
			}
			if bTbl.RawGet(k).IsNil() {
				result.RawSet(k, BoolValue(true))
			}
			key = k
		}
		return []Value{TableValue(result)}, nil
	})

	// container.setToArray(set) -> array table of set elements
	set("setToArray", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.setToArray' (table expected)")
		}
		result := NewTable()
		idx := int64(1)
		key := NilValue()
		for {
			k, _, ok := args[0].Table().Next(key)
			if !ok {
				break
			}
			result.RawSet(IntValue(idx), k)
			idx++
			key = k
		}
		return []Value{TableValue(result)}, nil
	})

	// ---------------------------------------------------------------
	// Queue (double-ended queue / deque)
	// Implemented as a table with metadata: _head, _tail, _data
	// ---------------------------------------------------------------

	// container.queueNew() -> new empty queue
	set("queueNew", func(args []Value) ([]Value, error) {
		q := NewTable()
		q.RawSet(StringValue("_head"), IntValue(1))
		q.RawSet(StringValue("_tail"), IntValue(0))
		q.RawSet(StringValue("_data"), TableValue(NewTable()))
		return []Value{TableValue(q)}, nil
	})

	// container.queuePush(q, value) -> push to back of queue
	set("queuePush", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.queuePush'")
		}
		q := args[0].Table()
		tail := q.RawGet(StringValue("_tail")).Int() + 1
		q.RawSet(StringValue("_tail"), IntValue(tail))
		data := q.RawGet(StringValue("_data")).Table()
		data.RawSet(IntValue(tail), args[1])
		return nil, nil
	})

	// container.queuePop(q) -> pop from front of queue, returns value or nil
	set("queuePop", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.queuePop'")
		}
		q := args[0].Table()
		head := q.RawGet(StringValue("_head")).Int()
		tail := q.RawGet(StringValue("_tail")).Int()
		if head > tail {
			return []Value{NilValue()}, nil
		}
		data := q.RawGet(StringValue("_data")).Table()
		val := data.RawGet(IntValue(head))
		data.RawSet(IntValue(head), NilValue())
		q.RawSet(StringValue("_head"), IntValue(head+1))
		return []Value{val}, nil
	})

	// container.queuePeek(q) -> peek at front of queue without removing
	set("queuePeek", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.queuePeek'")
		}
		q := args[0].Table()
		head := q.RawGet(StringValue("_head")).Int()
		tail := q.RawGet(StringValue("_tail")).Int()
		if head > tail {
			return []Value{NilValue()}, nil
		}
		data := q.RawGet(StringValue("_data")).Table()
		return []Value{data.RawGet(IntValue(head))}, nil
	})

	// container.queuePushFront(q, value) -> push to front of queue
	set("queuePushFront", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.queuePushFront'")
		}
		q := args[0].Table()
		head := q.RawGet(StringValue("_head")).Int() - 1
		q.RawSet(StringValue("_head"), IntValue(head))
		data := q.RawGet(StringValue("_data")).Table()
		data.RawSet(IntValue(head), args[1])
		return nil, nil
	})

	// container.queuePopBack(q) -> pop from back of queue
	set("queuePopBack", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.queuePopBack'")
		}
		q := args[0].Table()
		head := q.RawGet(StringValue("_head")).Int()
		tail := q.RawGet(StringValue("_tail")).Int()
		if head > tail {
			return []Value{NilValue()}, nil
		}
		data := q.RawGet(StringValue("_data")).Table()
		val := data.RawGet(IntValue(tail))
		data.RawSet(IntValue(tail), NilValue())
		q.RawSet(StringValue("_tail"), IntValue(tail-1))
		return []Value{val}, nil
	})

	// container.queueSize(q) -> int
	set("queueSize", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.queueSize'")
		}
		q := args[0].Table()
		head := q.RawGet(StringValue("_head")).Int()
		tail := q.RawGet(StringValue("_tail")).Int()
		size := tail - head + 1
		if size < 0 {
			size = 0
		}
		return []Value{IntValue(size)}, nil
	})

	// container.queueEmpty(q) -> bool
	set("queueEmpty", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.queueEmpty'")
		}
		q := args[0].Table()
		head := q.RawGet(StringValue("_head")).Int()
		tail := q.RawGet(StringValue("_tail")).Int()
		return []Value{BoolValue(head > tail)}, nil
	})

	// ---------------------------------------------------------------
	// Priority Queue (min-heap)
	// ---------------------------------------------------------------

	// container.heapNew() -> new empty min-heap (as a table wrapping Go heap)
	set("heapNew", func(args []Value) ([]Value, error) {
		h := &valueHeap{}
		heap.Init(h)
		wrapper := NewTable()
		wrapper.RawSet(StringValue("_heap"), FunctionValue(&GoFunction{
			Name: "_heap_internal",
			Fn: func([]Value) ([]Value, error) {
				return nil, nil
			},
		}))
		// Store the Go heap in a closure
		// We use function closures to store Go data in GScript tables

		// Helper functions for this heap instance
		pushFn := &GoFunction{
			Name: "heap.push",
			Fn: func(innerArgs []Value) ([]Value, error) {
				if len(innerArgs) < 1 {
					return nil, fmt.Errorf("heap.push: value expected")
				}
				heap.Push(h, innerArgs[0])
				return nil, nil
			},
		}

		popFn := &GoFunction{
			Name: "heap.pop",
			Fn: func(innerArgs []Value) ([]Value, error) {
				if h.Len() == 0 {
					return []Value{NilValue()}, nil
				}
				v := heap.Pop(h).(Value)
				return []Value{v}, nil
			},
		}

		peekFn := &GoFunction{
			Name: "heap.peek",
			Fn: func(innerArgs []Value) ([]Value, error) {
				if h.Len() == 0 {
					return []Value{NilValue()}, nil
				}
				return []Value{(*h)[0]}, nil
			},
		}

		sizeFn := &GoFunction{
			Name: "heap.size",
			Fn: func(innerArgs []Value) ([]Value, error) {
				return []Value{IntValue(int64(h.Len()))}, nil
			},
		}

		emptyFn := &GoFunction{
			Name: "heap.empty",
			Fn: func(innerArgs []Value) ([]Value, error) {
				return []Value{BoolValue(h.Len() == 0)}, nil
			},
		}

		wrapper.RawSet(StringValue("push"), FunctionValue(pushFn))
		wrapper.RawSet(StringValue("pop"), FunctionValue(popFn))
		wrapper.RawSet(StringValue("peek"), FunctionValue(peekFn))
		wrapper.RawSet(StringValue("size"), FunctionValue(sizeFn))
		wrapper.RawSet(StringValue("empty"), FunctionValue(emptyFn))

		return []Value{TableValue(wrapper)}, nil
	})

	// ---------------------------------------------------------------
	// Stack (LIFO)
	// ---------------------------------------------------------------

	// container.stackNew() -> new empty stack
	set("stackNew", func(args []Value) ([]Value, error) {
		s := NewTable()
		s.RawSet(StringValue("_data"), TableValue(NewTable()))
		s.RawSet(StringValue("_size"), IntValue(0))
		return []Value{TableValue(s)}, nil
	})

	// container.stackPush(stack, value)
	set("stackPush", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'container.stackPush'")
		}
		s := args[0].Table()
		size := s.RawGet(StringValue("_size")).Int() + 1
		s.RawSet(StringValue("_size"), IntValue(size))
		data := s.RawGet(StringValue("_data")).Table()
		data.RawSet(IntValue(size), args[1])
		return nil, nil
	})

	// container.stackPop(stack) -> value or nil
	set("stackPop", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.stackPop'")
		}
		s := args[0].Table()
		size := s.RawGet(StringValue("_size")).Int()
		if size == 0 {
			return []Value{NilValue()}, nil
		}
		data := s.RawGet(StringValue("_data")).Table()
		val := data.RawGet(IntValue(size))
		data.RawSet(IntValue(size), NilValue())
		s.RawSet(StringValue("_size"), IntValue(size-1))
		return []Value{val}, nil
	})

	// container.stackPeek(stack) -> value or nil
	set("stackPeek", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.stackPeek'")
		}
		s := args[0].Table()
		size := s.RawGet(StringValue("_size")).Int()
		if size == 0 {
			return []Value{NilValue()}, nil
		}
		data := s.RawGet(StringValue("_data")).Table()
		return []Value{data.RawGet(IntValue(size))}, nil
	})

	// container.stackSize(stack) -> int
	set("stackSize", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.stackSize'")
		}
		s := args[0].Table()
		return []Value{s.RawGet(StringValue("_size"))}, nil
	})

	// container.stackEmpty(stack) -> bool
	set("stackEmpty", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'container.stackEmpty'")
		}
		s := args[0].Table()
		return []Value{BoolValue(s.RawGet(StringValue("_size")).Int() == 0)}, nil
	})

	return t
}

// valueHeap implements heap.Interface for min-heap of Values.
type valueHeap []Value

func (h valueHeap) Len() int { return len(h) }
func (h valueHeap) Less(i, j int) bool {
	less, ok := h[i].LessThan(h[j])
	return ok && less
}
func (h valueHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *valueHeap) Push(x interface{}) { *h = append(*h, x.(Value)) }
func (h *valueHeap) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
