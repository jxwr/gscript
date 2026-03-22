package runtime

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Shape registry: maps ordered field name sequences to unique uint32 IDs.
// ShapeID 0 is reserved as "no shape" sentinel (hash mode or empty table).

var (
	shapeCounter uint32 = 0 // atomic, starts at 0, first real shape = 1
	shapeRegistry sync.Map  // map[string]uint32, key = NUL-joined skeys
)

// GetShapeID returns the shapeID for the given ordered field name sequence.
// Tables with identical fields in the same order share the same shapeID.
func GetShapeID(skeys []string) uint32 {
	if len(skeys) == 0 {
		return 0
	}
	key := strings.Join(skeys, "\x00")
	if id, ok := shapeRegistry.Load(key); ok {
		return id.(uint32)
	}
	newID := atomic.AddUint32(&shapeCounter, 1)
	actual, _ := shapeRegistry.LoadOrStore(key, newID)
	return actual.(uint32)
}
