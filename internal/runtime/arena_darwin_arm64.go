//go:build darwin && arm64

package runtime

import "syscall"

// mmapAlloc allocates a region of read/write memory via mmap.
// The memory is anonymous (not backed by a file) and private.
// On macOS, anonymous mmap memory is guaranteed to be zero-filled.
func mmapAlloc(size int) ([]byte, error) {
	return syscall.Mmap(-1, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
}

// mmapFree releases memory previously allocated by mmapAlloc.
func mmapFree(data []byte) error {
	return syscall.Munmap(data)
}
