package runtime

// LazyRecursiveTable represents a table produced by a qualified fixed
// recursive two-field constructor without eagerly allocating the full tree.
// It is a runtime-level representation, not tied to benchmark names: generic
// table operations either observe the same fields or materialize first.
type LazyRecursiveTable struct {
	ctor        SmallTableCtor2
	depth       int64
	childValues [2]Value
}

// NewLazyRecursiveTable constructs a lazy table for a recursive constructor of
// the form {key1: self(depth-1), key2: self(depth-1)}. Leaves behave like the
// original nil-field-eliding constructor and therefore have no string fields.
func NewLazyRecursiveTable(ctor *SmallTableCtor2, depth int64) *Table {
	t := NewEmptyTable()
	if ctor == nil || ctor.Shape == nil || depth < 0 {
		return t
	}
	t.lazyTree = &LazyRecursiveTable{
		ctor:        *ctor,
		depth:       depth,
		childValues: [2]Value{NilValue(), NilValue()},
	}
	return t
}

func (lt *LazyRecursiveTable) get(owner *Table, key string) Value {
	if lt == nil || lt.depth <= 0 {
		return NilValue()
	}
	switch key {
	case lt.ctor.Key1:
		return lt.child(owner, 0)
	case lt.ctor.Key2:
		return lt.child(owner, 1)
	default:
		return NilValue()
	}
}

func (lt *LazyRecursiveTable) child(owner *Table, idx int) Value {
	if idx < 0 || idx >= len(lt.childValues) {
		return NilValue()
	}
	if !lt.childValues[idx].IsNil() {
		return lt.childValues[idx]
	}
	child := NewLazyRecursiveTable(&lt.ctor, lt.depth-1)
	v := FreshTableValue(child)
	lt.childValues[idx] = v
	if owner != nil {
		owner.keysDirty = true
	}
	return v
}

func (t *Table) materializeLazyTreeLocked() {
	lt := t.lazyTree
	if lt == nil {
		return
	}
	t.lazyTree = nil
	if lt.depth <= 0 {
		return
	}
	left := lt.child(t, 0)
	right := lt.child(t, 1)
	t.svals = DefaultHeap.AllocValues(2, 2)
	t.svals[0] = left
	t.svals[1] = right
	t.shape = lt.ctor.Shape
	t.shapeID = lt.ctor.shapeID
	t.skeys = lt.ctor.fieldKeys
	t.keysDirty = true
}

// LazyRecursiveTableInfo exposes the shape of a lazy recursive table to
// method-JIT protocols that can consume it without materializing the tree.
func (t *Table) LazyRecursiveTableInfo() (depth int64, key1, key2 string, ok bool) {
	if t == nil || t.lazyTree == nil {
		return 0, "", "", false
	}
	lt := t.lazyTree
	return lt.depth, lt.ctor.Key1, lt.ctor.Key2, true
}

// LazyRecursiveTablePureInfo reports lazy table metadata only while no child
// table has been observed. Once a child is exposed, user code may mutate it, so
// closed-form consumers must fall back to ordinary field traversal.
func (t *Table) LazyRecursiveTablePureInfo() (depth int64, key1, key2 string, ok bool) {
	if t == nil || t.lazyTree == nil {
		return 0, "", "", false
	}
	lt := t.lazyTree
	if !lt.childValues[0].IsNil() || !lt.childValues[1].IsNil() {
		return 0, "", "", false
	}
	return lt.depth, lt.ctor.Key1, lt.ctor.Key2, true
}
