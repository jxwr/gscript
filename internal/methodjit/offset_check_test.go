//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"testing"
	"unsafe"
	
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/jit"
)

func TestTableOffsets(t *testing.T) {
	// Create a table and fill it with 1000 integers (like make_random_array)
	tbl := runtime.NewTable()
	x := int64(42)
	for i := int64(1); i <= 1000; i++ {
		x = (x * 1103515245 + 12345) % 2147483648
		tbl.RawSetInt(i, runtime.IntValue(x))
	}
	
	p := uintptr(unsafe.Pointer(tbl))
	
	// Read all relevant fields
	arrayDataPtr := *(*uintptr)(unsafe.Pointer(p + 8))
	arrayLen := *(*uintptr)(unsafe.Pointer(p + 16))
	arrayKindByte := *(*uint8)(unsafe.Pointer(p + uintptr(jit.TableOffArrayKind)))
	intArrayDataPtr := *(*uintptr)(unsafe.Pointer(p + uintptr(jit.TableOffIntArray)))
	intArrayLen := *(*uintptr)(unsafe.Pointer(p + uintptr(jit.TableOffIntArrayLen)))
	
	fmt.Printf("Table at: 0x%x\n", p)
	fmt.Printf("array.data (offset 8): 0x%x (nil=%v)\n", arrayDataPtr, arrayDataPtr == 0)
	fmt.Printf("array.len  (offset 16): %d\n", arrayLen)
	fmt.Printf("arrayKind  (offset %d): %d (AKInt=%d, AKMixed=0)\n", jit.TableOffArrayKind, arrayKindByte, jit.AKInt)
	fmt.Printf("intArray.data (offset %d): 0x%x (nil=%v)\n", jit.TableOffIntArray, intArrayDataPtr, intArrayDataPtr == 0)
	fmt.Printf("intArray.len  (offset %d): %d\n", jit.TableOffIntArrayLen, intArrayLen)
	
	if arrayKindByte != jit.AKInt {
		t.Errorf("expected ArrayInt (%d), got %d", jit.AKInt, arrayKindByte)
	}
	if intArrayDataPtr == 0 {
		t.Errorf("intArray data pointer is nil despite len=%d", intArrayLen)
	}
	if intArrayLen != 1001 {
		t.Errorf("expected intArrayLen=1001, got %d", intArrayLen)
	}
}
