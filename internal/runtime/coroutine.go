package runtime

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// CoroutineStatus represents the state of a coroutine.
type CoroutineStatus int

const (
	CoroutineSuspended CoroutineStatus = iota
	CoroutineRunning
	CoroutineDead
	CoroutineNormal // running but yielded control to a nested resume
)

// yieldResult carries values or completion signals from a coroutine goroutine
// back to the caller of resume.
type yieldResult struct {
	values []Value
	err    error
	done   bool // true if the coroutine function returned (not yielded)
}

// Coroutine implements Lua-style coroutines using Go goroutines and channels.
// Each coroutine runs in its own goroutine, suspended at yield points via
// synchronous channel communication.
type Coroutine struct {
	status   CoroutineStatus
	fn       Value // the function to run inside the coroutine
	started  bool  // whether the goroutine has been launched

	resumeCh chan []Value     // caller -> coroutine: resume/initial args
	yieldCh  chan yieldResult // coroutine -> caller: yielded values or completion
}

// NewCoroutine creates a new coroutine wrapping the given function.
// The coroutine starts in the Suspended state; the goroutine is not launched
// until the first resume.
func NewCoroutine(fn Value) *Coroutine {
	return &Coroutine{
		status:   CoroutineSuspended,
		fn:       fn,
		resumeCh: make(chan []Value, 1),
		yieldCh:  make(chan yieldResult, 1),
	}
}

// Status returns the human-readable status string for the coroutine.
func (co *Coroutine) Status() string {
	switch co.status {
	case CoroutineSuspended:
		return "suspended"
	case CoroutineRunning:
		return "running"
	case CoroutineDead:
		return "dead"
	case CoroutineNormal:
		return "normal"
	}
	return "dead"
}

// goroutineCoMap maps goroutine IDs to their active *Coroutine.
// This allows coroutine.yield to find the correct coroutine for the
// current goroutine without requiring interpreter-level state that
// would be wrong when GoFunctions captured the main interpreter.
var goroutineCoMap sync.Map // map[int64]*Coroutine

// setCurrentCoroutine associates a coroutine with the current goroutine.
func setCurrentCoroutine(co *Coroutine) {
	gid := currentGoroutineID()
	if co == nil {
		goroutineCoMap.Delete(gid)
	} else {
		goroutineCoMap.Store(gid, co)
	}
}

// getCurrentCoroutine returns the coroutine for the current goroutine, or nil.
func getCurrentCoroutine() *Coroutine {
	gid := currentGoroutineID()
	v, ok := goroutineCoMap.Load(gid)
	if !ok {
		return nil
	}
	return v.(*Coroutine)
}

// currentGoroutineID extracts the current goroutine's ID from the runtime stack.
func currentGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// Stack output starts with "goroutine NNN [..."
	s := string(buf[:n])
	s = strings.TrimPrefix(s, "goroutine ")
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return 0
	}
	id, _ := strconv.ParseInt(s[:idx], 10, 64)
	return id
}
