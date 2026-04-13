//go:build darwin && arm64

// tiering_manager_diag_unsafe.go isolates the single unsafe.Pointer dance
// needed by CompileForDiagnostics to copy bytes out of the mmap'd code
// region. Kept in its own file so the main diag file stays unsafe-free.

package methodjit

import "unsafe"

// unsafeCodeSlice returns the mmap'd ARM64 code region of cf as a byte
// slice. The slice is only valid until cf.Code.Free() runs; callers must
// copy the bytes before freeing.
func unsafeCodeSlice(cf *CompiledFunction) []byte {
	return unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
}
