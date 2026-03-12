//go:build darwin && arm64

package jit

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS ARM64 constants not in syscall package.
const (
	_MAP_JIT = 0x0800 // MAP_JIT on macOS
)

var (
	libSystem            uintptr
	fnJitWriteProtect    func(enabled int32)
	fnSysIcacheInvalidate func(start unsafe.Pointer, size uintptr)
)

func init() {
	var err error
	libSystem, err = purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY)
	if err != nil {
		panic(fmt.Sprintf("jit: failed to open libSystem: %v", err))
	}

	purego.RegisterLibFunc(&fnJitWriteProtect, libSystem, "pthread_jit_write_protect_np")
	purego.RegisterLibFunc(&fnSysIcacheInvalidate, libSystem, "sys_icache_invalidate")
}

// AllocExec allocates executable memory of the given size.
// Returns a CodeBlock that can be written to and then made executable.
func AllocExec(size int) (*CodeBlock, error) {
	// Round up to page size.
	pageSize := syscall.Getpagesize()
	alignedSize := (size + pageSize - 1) &^ (pageSize - 1)

	// mmap with MAP_JIT on macOS ARM64
	mem, err := syscall.Mmap(-1, 0, alignedSize,
		syscall.PROT_READ|syscall.PROT_WRITE|syscall.PROT_EXEC,
		syscall.MAP_PRIVATE|syscall.MAP_ANON|_MAP_JIT)
	if err != nil {
		return nil, fmt.Errorf("jit: mmap failed: %w", err)
	}

	return &CodeBlock{
		mem:  mem,
		ptr:  unsafe.Pointer(&mem[0]),
		size: 0,
	}, nil
}

// WriteCode writes machine code into the block and makes it executable.
// LockOSThread is required because pthread_jit_write_protect_np is per-thread.
// Without it, Go's scheduler could migrate the goroutine to another OS thread
// between disabling and re-enabling W^X, leaving a thread in writable mode.
func (b *CodeBlock) WriteCode(code []byte) error {
	if len(code) > len(b.mem) {
		return fmt.Errorf("jit: code size %d exceeds block size %d", len(code), len(b.mem))
	}

	// Pin to current OS thread for the W^X toggle sequence.
	runtime.LockOSThread()

	// Disable W^X write protection to allow writing
	fnJitWriteProtect(0) // 0 = writable

	// Copy the code
	copy(b.mem, code)
	b.size = len(code)

	// Re-enable write protection (make executable)
	fnJitWriteProtect(1) // 1 = executable

	// Safe to unpin now — this thread is back in executable mode.
	runtime.UnlockOSThread()

	// Flush instruction cache
	fnSysIcacheInvalidate(b.ptr, uintptr(b.size))

	return nil
}

// Free releases the executable memory.
func (b *CodeBlock) Free() error {
	if b.mem == nil {
		return nil
	}
	err := syscall.Munmap(b.mem)
	b.mem = nil
	b.ptr = nil
	b.size = 0
	return err
}
