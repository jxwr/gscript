package runtime

import (
	"sync"
	"testing"
)

func TestNativeStringArenaReserveConcurrentUnique(t *testing.T) {
	const (
		goroutines = 8
		perWorker  = 512
		size       = uintptr(32)
	)

	results := make(chan uintptr, goroutines*perWorker)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				p := NativeStringArenaReserve(size)
				if p == nil {
					t.Errorf("NativeStringArenaReserve returned nil")
					return
				}
				results <- uintptr(p)
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[uintptr]struct{}, goroutines*perWorker)
	for p := range results {
		if _, ok := seen[p]; ok {
			t.Fatalf("duplicate native string arena reservation at %#x", p)
		}
		seen[p] = struct{}{}
	}
	if got, want := len(seen), goroutines*perWorker; got != want {
		t.Fatalf("reservation count=%d, want %d", got, want)
	}
}

func TestNativeStringArenaReserveAlignsHeaders(t *testing.T) {
	p1 := uintptr(NativeStringArenaReserve(1))
	p2 := uintptr(NativeStringArenaReserve(1))
	if p1 == 0 || p2 == 0 {
		t.Fatal("NativeStringArenaReserve returned nil")
	}
	if p1%16 != 0 || p2%16 != 0 {
		t.Fatalf("reservations are not 16-byte aligned: %#x %#x", p1, p2)
	}
	if got, want := p2-p1, uintptr(16); got != want {
		t.Fatalf("reservation stride=%d, want %d", got, want)
	}
}
