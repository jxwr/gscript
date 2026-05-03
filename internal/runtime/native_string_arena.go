package runtime

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

const nativeStringArenaSize = 4 << 20

var nativeStringArena struct {
	mu        sync.Mutex
	buf       []byte
	allocBase uintptr
	base      uintptr
	// cursor is accessed by Go with sync/atomic and by ARM64 native leaf code
	// with LDAXR/STLXR. Do not read or write it with ordinary loads/stores
	// after init.
	cursor uintptr
	end    uintptr
}

// NativeStringArenaEnsure initializes the process-local arena used by native
// string-format fast paths. The arena is intentionally lazy so ordinary gscript
// processes do not pay a fixed RSS cost when the fast path is never compiled.
func NativeStringArenaEnsure() bool {
	if atomic.LoadUintptr(&nativeStringArena.end) != 0 {
		return true
	}
	nativeStringArena.mu.Lock()
	defer nativeStringArena.mu.Unlock()
	if nativeStringArena.end != 0 {
		return true
	}
	nativeStringArena.buf = make([]byte, nativeStringArenaSize)
	if len(nativeStringArena.buf) == 0 {
		return false
	}
	allocBase := uintptr(unsafe.Pointer(&nativeStringArena.buf[0]))
	base := alignNativeStringArena(allocBase)
	nativeStringArena.allocBase = allocBase
	nativeStringArena.base = base
	atomic.StoreUintptr(&nativeStringArena.cursor, base)
	atomic.StoreUintptr(&nativeStringArena.end, allocBase+uintptr(len(nativeStringArena.buf)))
	return true
}

// NativeStringArenaCursorPtr returns the address of the bump cursor used by
// native string-format fast paths. Native users must update it with atomic
// exclusive load/store or CAS. The backing memory is process-lifetime and
// intentionally not reclaimed; callers must fall back if cursor reaches end.
func NativeStringArenaCursorPtr() *uintptr {
	return &nativeStringArena.cursor
}

// NativeStringArenaEndPtr returns the address of the native string arena limit.
func NativeStringArenaEndPtr() *uintptr {
	return &nativeStringArena.end
}

// NativeStringArenaReserve atomically reserves size bytes from the native string
// arena and returns the reservation base, or nil if the arena is exhausted.
// It is the Go equivalent of the JIT inline LDAXR/STLXR bump loop.
func NativeStringArenaReserve(size uintptr) unsafe.Pointer {
	if size == 0 {
		return nil
	}
	if !NativeStringArenaEnsure() {
		return nil
	}
	size = alignNativeStringArena(size)
	end := atomic.LoadUintptr(&nativeStringArena.end)
	for {
		old := atomic.LoadUintptr(&nativeStringArena.cursor)
		next := old + size
		if next < old || next > end {
			return nil
		}
		if atomic.CompareAndSwapUintptr(&nativeStringArena.cursor, old, next) {
			return unsafe.Add(unsafe.Pointer(&nativeStringArena.buf[0]), old-nativeStringArena.allocBase)
		}
	}
}

func alignNativeStringArena(n uintptr) uintptr {
	return (n + 15) &^ 15
}
