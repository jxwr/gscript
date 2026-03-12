package jit

import "unsafe"

// CodeBlock represents an allocated block of executable memory.
type CodeBlock struct {
	mem  []byte         // mmap'd memory region
	ptr  unsafe.Pointer // pointer to start of code
	size int            // size of code within the block (may be smaller than len(mem))
}

// Ptr returns the function pointer to the start of the code.
func (b *CodeBlock) Ptr() unsafe.Pointer {
	return b.ptr
}

// Size returns the code size in bytes.
func (b *CodeBlock) Size() int {
	return b.size
}
