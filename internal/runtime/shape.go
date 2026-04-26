// Package runtime: shape.go implements the Shape (hidden-class) system for
// GScript tables.  Each unique ordered field sequence maps to a single Shape
// instance shared across all tables with those fields.  Shapes form a
// transition graph: Shape.Transition(key) returns (and caches) the shape
// reached by appending key, enabling V8-style O(1) field lookup.
//
// ShapeID 0 is reserved as the "no shape" sentinel (hash mode or empty table).

package runtime

import (
	"strings"
	"sync"
	"sync/atomic"
)

var (
	shapeIDCounter uint32   = 0 // atomic; first real shape gets ID 1
	shapeByKey     sync.Map     // string → *Shape  (key = NUL-joined field names)
	shapeByID      sync.Map     // uint32 → *Shape
)

// Shape is an immutable hidden-class descriptor for a GScript table.
// All tables that have the same fields in the same insertion order share a
// single Shape instance.
type Shape struct {
	ID          uint32
	FieldKeys   []string       // ordered field names (immutable)
	FieldMap    map[string]int // key → index for O(1) GetFieldIndex
	transitions sync.Map       // string → *Shape (cached addField transitions)
}

// GetFieldIndex returns the slot index of key in FieldKeys, or -1 if absent.
func (s *Shape) GetFieldIndex(key string) int {
	if idx, ok := s.FieldMap[key]; ok {
		return idx
	}
	return -1
}

// Transition returns the Shape produced by appending key to s.FieldKeys.
// The result is cached so repeated calls with the same key return the same
// instance.
func (s *Shape) Transition(key string) *Shape {
	if v, ok := s.transitions.Load(key); ok {
		return v.(*Shape)
	}
	newKeys := make([]string, len(s.FieldKeys)+1)
	copy(newKeys, s.FieldKeys)
	newKeys[len(s.FieldKeys)] = key
	next := getOrCreateShape(newKeys)
	actual, _ := s.transitions.LoadOrStore(key, next)
	return actual.(*Shape)
}

// getOrCreateShape is the internal factory.  It is thread-safe.
func getOrCreateShape(keys []string) *Shape {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) == 1 {
		return getOrCreateSingleFieldShape(keys[0])
	}
	k := strings.Join(keys, "\x00")
	if v, ok := shapeByKey.Load(k); ok {
		return v.(*Shape)
	}
	id := atomic.AddUint32(&shapeIDCounter, 1)
	fm := make(map[string]int, len(keys))
	for i, key := range keys {
		fm[key] = i
	}
	s := &Shape{
		ID:        id,
		FieldKeys: keys,
		FieldMap:  fm,
	}
	actual, loaded := shapeByKey.LoadOrStore(k, s)
	if loaded {
		// Another goroutine won the race; discard ours (ID is wasted, harmless).
		return actual.(*Shape)
	}
	shapeByID.Store(id, s)
	return s
}

func getOrCreateSingleFieldShape(key string) *Shape {
	if v, ok := shapeByKey.Load(key); ok {
		return v.(*Shape)
	}
	id := atomic.AddUint32(&shapeIDCounter, 1)
	keys := []string{key}
	s := &Shape{
		ID:        id,
		FieldKeys: keys,
		FieldMap:  map[string]int{key: 0},
	}
	actual, loaded := shapeByKey.LoadOrStore(key, s)
	if loaded {
		return actual.(*Shape)
	}
	shapeByID.Store(id, s)
	return s
}

// GetShape returns the canonical Shape for the given ordered field sequence,
// or nil for an empty slice.
func GetShape(skeys []string) *Shape {
	return getOrCreateShape(skeys)
}

// GetShapeID returns the uint32 ID for the given ordered field sequence.
// Returns 0 for an empty slice (the "no shape" sentinel).
// Backward-compatible with code that only uses the numeric ID.
func GetShapeID(skeys []string) uint32 {
	if len(skeys) == 0 {
		return 0
	}
	return getOrCreateShape(skeys).ID
}

// LookupShapeByID returns the Shape registered under id, or nil.
func LookupShapeByID(id uint32) *Shape {
	if v, ok := shapeByID.Load(id); ok {
		return v.(*Shape)
	}
	return nil
}
