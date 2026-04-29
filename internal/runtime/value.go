package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ValueType represents the type of a GScript value.
type ValueType uint8

const (
	TypeNil       ValueType = iota
	TypeBool                // boolean
	TypeInt                 // integer numbers
	TypeFloat               // floating-point numbers
	TypeString              // strings
	TypeTable               // tables (associative arrays)
	TypeFunction            // functions (closures and Go functions)
	TypeCoroutine           // coroutines
	TypeChannel             // channels
)

// ---------------------------------------------------------------------------
// NaN-boxing constants
// ---------------------------------------------------------------------------
//
// Value is an 8-byte NaN-boxed uint64.
//
//	Float64:  any IEEE 754 bit pattern where bits 50-62 are NOT all 1.
//	Tagged:   bits 50-62 all 1 (qNaN), sign=1, bits 48-49 = type tag,
//	          bits 0-47 = 48-bit payload.
//
//	tag 00 = nil      (payload 0)
//	tag 01 = bool     (payload 0=false, 1=true)
//	tag 10 = int48    (48-bit two's complement)
//	tag 11 = pointer  (bits 0-43 = 44-bit address, bits 44-47 = ptr sub-type)

const (
	// nanBits: bits 50-62 all set = quiet NaN with our discriminator bit (50).
	nanBits uint64 = 0x7FFC000000000000

	// Type tags (sign=1 + nanBits + 2-bit tag in bits 48-49).
	tagNil  uint64 = 0xFFFC000000000000 // sign=1, tag=00
	tagBool uint64 = 0xFFFD000000000000 // sign=1, tag=01
	tagInt  uint64 = 0xFFFE000000000000 // sign=1, tag=10
	tagPtr  uint64 = 0xFFFF000000000000 // sign=1, tag=11

	// Masks.
	tagMask     uint64 = 0xFFFF000000000000 // top 16 bits
	payloadMask uint64 = 0x0000FFFFFFFFFFFF // bottom 48 bits

	// Pre-built special values.
	valNil   uint64 = tagNil
	valFalse uint64 = tagBool     // payload = 0
	valTrue  uint64 = tagBool | 1 // payload = 1

	// Canonical NaN (Go/IEEE 754 standard quiet NaN). Bit 50 is 0, so it
	// does NOT collide with our tagged space (which requires bit 50 = 1).
	canonicalNaN uint64 = 0x7FF8000000000000

	// Int48 range limits.
	maxInt48 int64 = (1 << 47) - 1 //  140_737_488_355_327
	minInt48 int64 = -(1 << 47)    // -140_737_488_355_328

	// Pointer sub-type bits (stored in bits 44-47 of the pointer payload).
	// macOS ARM64 pointers use ~41 bits, so bits 44-47 are free.
	ptrSubShift        = 44
	ptrSubMask  uint64 = 0xF << ptrSubShift     // bits 44-47
	ptrAddrMask uint64 = (1 << ptrSubShift) - 1 // bits 0-43

	ptrSubTable       uint64 = 0 << ptrSubShift
	ptrSubString      uint64 = 1 << ptrSubShift
	ptrSubClosure     uint64 = 2 << ptrSubShift // *runtime.Closure
	ptrSubGoFunction  uint64 = 3 << ptrSubShift // *GoFunction
	ptrSubCoroutine   uint64 = 4 << ptrSubShift // *Coroutine
	ptrSubChannel     uint64 = 5 << ptrSubShift
	ptrSubAnyFunction uint64 = 6 << ptrSubShift // interface-based function (needs ifaceRoots)
	ptrSubAnyCoro     uint64 = 7 << ptrSubShift // interface-based coroutine (needs ifaceRoots)
	ptrSubVMClosure   uint64 = 8 << ptrSubShift // *vm.Closure (direct pointer, fast OP_CALL path)
)

// Value is a NaN-boxed 8-byte representation of all GScript values.
// Replaces the old 24-byte struct {typ, data, ptr}.
type Value uint64

// ---------------------------------------------------------------------------
// GC roots: keeps Go-heap pointers alive while hidden inside uint64 Values
// ---------------------------------------------------------------------------
//
// NaN-boxed pointers are invisible to Go's GC. The root log keeps them alive.
// Periodic compaction removes dead entries to prevent unbounded growth.
//
// The root log is an append-only slice with an atomic cursor. keepAlive is
// lock-free for the common case (no mutex, just an atomic increment).
// lookupIface uses a separate locked map for the rare interface-based
// function/coroutine values that need type recovery.

// gcRootLog is a lock-free append-only log for keeping Go-heap pointers alive.
// Uses []unsafe.Pointer instead of []any for 2x less GC scan overhead
// (1 word per entry vs 2 words for interface).
type gcRootLog struct {
	entries []unsafe.Pointer
	cursor  int64 // next free index (accessed atomically)
}

// GCRootScanner is implemented by VMs to enumerate all live GC root pointers.
// Used by gcCompact to determine which gcRootLog entries are still needed.
type GCRootScanner interface {
	ScanGCRoots(visitor func(unsafe.Pointer))
}

const (
	// gcCompactInterval triggers compaction every N new allocations.
	// 1M balances keeping the log small vs amortizing compaction cost.
	gcCompactInterval int64 = 1 << 20 // 1M
)

var (
	gcLog gcRootLog
	// Separate locked map for interface-based values that need lookupIface.
	// Only used by AnyFunction/AnyCoroutine (cold path).
	ifaceMu    sync.Mutex
	ifaceRoots = make(map[uintptr]any, 64)

	// activeVMs tracks all live VMs for GC root scanning.
	activeVMsMu   sync.Mutex
	activeVMs     []GCRootScanner
	activeVMCount int32 // atomic count for fast check in keepAlive

	// gcCompacting prevents re-entrant compaction.
	gcCompacting int32

	// gcNeedsCompact is set by keepAlive when compaction threshold is reached.
	// Actual compaction is deferred to a VM safe point via CheckGC().
	gcNeedsCompact int32
)

// RegisterVM adds a VM to the active set for GC root scanning.
func RegisterVM(scanner GCRootScanner) {
	activeVMsMu.Lock()
	activeVMs = append(activeVMs, scanner)
	atomic.StoreInt32(&activeVMCount, int32(len(activeVMs)))
	activeVMsMu.Unlock()
}

// UnregisterVM removes a VM from the active set.
func UnregisterVM(scanner GCRootScanner) {
	activeVMsMu.Lock()
	for i, s := range activeVMs {
		if s == scanner {
			activeVMs[i] = activeVMs[len(activeVMs)-1]
			activeVMs[len(activeVMs)-1] = nil
			activeVMs = activeVMs[:len(activeVMs)-1]
			break
		}
	}
	atomic.StoreInt32(&activeVMCount, int32(len(activeVMs)))
	activeVMsMu.Unlock()
}

func init() {
	gcLog.entries = make([]unsafe.Pointer, 1<<20) // 1M entries (~8MB), grows if needed
}

// keepAlive registers a Go-heap pointer in the root log so the GC does not
// collect the object while it is hidden inside a NaN-boxed Value.
// Lock-free: uses atomic increment on the cursor.
func keepAlive(p unsafe.Pointer, _ any) {
	idx := atomic.AddInt64(&gcLog.cursor, 1) - 1
	if idx < int64(len(gcLog.entries)) {
		gcLog.entries[idx] = p
	} else {
		// Slow path: grow the log (rare, only when > 4M allocations)
		gcLogGrow(p)
	}
	// Signal that GC compaction is needed; actual compaction is deferred
	// to a VM safe point (CheckGC) where all values are in registers.
	if idx > 0 && idx%gcCompactInterval == 0 && atomic.LoadInt32(&activeVMCount) > 0 {
		atomic.StoreInt32(&gcNeedsCompact, 1)
	}
}

func gcLogGrow(p unsafe.Pointer) {
	ifaceMu.Lock()
	gcLog.entries = append(gcLog.entries, p)
	ifaceMu.Unlock()
}

// CheckGC runs deferred GC compaction if needed. Must be called at a VM safe
// point where all recent allocations have been stored into registers/globals.
func CheckGC() {
	if atomic.LoadInt32(&gcNeedsCompact) != 0 && atomic.CompareAndSwapInt32(&gcNeedsCompact, 1, 0) {
		gcCompact()
	}
}

// gcCompact rebuilds the gcRootLog with only pointers reachable from active VMs.
// Conservative: retains any pointer that appears in a VM's register file, globals,
// open upvalues, or recursively inside any reachable table.
func gcCompact() {
	// Prevent re-entrant compaction.
	if !atomic.CompareAndSwapInt32(&gcCompacting, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&gcCompacting, 0)

	// Snapshot current cursor.
	oldCursor := atomic.LoadInt64(&gcLog.cursor)
	if oldCursor <= gcCompactInterval/2 {
		return // not worth compacting
	}

	// Grab a snapshot of registered VMs.
	activeVMsMu.Lock()
	vms := make([]GCRootScanner, len(activeVMs))
	copy(vms, activeVMs)
	activeVMsMu.Unlock()

	if len(vms) == 0 {
		return // no VMs to scan; keep everything (conservative)
	}

	// Build the live set: all pointers reachable from any VM.
	liveSet := make(map[uintptr]struct{}, oldCursor/4)
	visitor := func(p unsafe.Pointer) {
		liveSet[uintptr(p)] = struct{}{}
	}
	for _, vm := range vms {
		vm.ScanGCRoots(visitor)
	}
	visitCurrentTableSlabRoot(visitor)

	// Compact: copy only live entries into a fresh log.
	// We allocate a new entries slice and atomically swap.
	n := int64(len(gcLog.entries))
	if n < gcCompactInterval {
		n = gcCompactInterval
	}
	newEntries := make([]unsafe.Pointer, n)
	var newCursor int64
	for i := int64(0); i < oldCursor && i < int64(len(gcLog.entries)); i++ {
		p := gcLog.entries[i]
		if p == nil {
			continue
		}
		if _, live := liveSet[uintptr(p)]; live {
			if newCursor < n {
				newEntries[newCursor] = p
				newCursor++
			}
		}
	}

	// Swap the log. During the swap, concurrent keepAlive calls may have
	// added entries beyond oldCursor. Copy those too (conservative).
	currentCursor := atomic.LoadInt64(&gcLog.cursor)
	for i := oldCursor; i < currentCursor && i < int64(len(gcLog.entries)); i++ {
		p := gcLog.entries[i]
		if p != nil && newCursor < n {
			newEntries[newCursor] = p
			newCursor++
		}
	}

	// Atomic swap.
	gcLog.entries = newEntries
	atomic.StoreInt64(&gcLog.cursor, newCursor)
}

// ScanValueRoots scans a single Value for GC root pointers.
// If the value is a pointer type, calls visitor with its raw pointer.
// If it's a table, recursively scans the table's contents.
// The `seen` map prevents infinite loops on circular table references.
func ScanValueRoots(v Value, visitor func(unsafe.Pointer), seen map[uintptr]struct{}) {
	bits := uint64(v)
	if bits&nanBits != nanBits {
		return // float, no pointer
	}
	if bits&tagMask != tagPtr {
		return // nil, bool, or int — no pointer
	}
	p := v.ptrPayload()
	if p == nil {
		return
	}
	addr := uintptr(p)

	sub := bits & ptrSubMask
	if sub == ptrSubTable {
		visitTableRoot(p, visitor)
		if _, already := seen[addr]; already {
			return
		}
		seen[addr] = struct{}{}
		t := (*Table)(p)
		scanTableRoots(t, visitor, seen)
		return
	}
	visitor(p)
}

func visitTableRoot(p unsafe.Pointer, visitor func(unsafe.Pointer)) {
	visitor(p)
	if root := tableSlabRootForPointer(p); root != nil && root != p {
		visitor(root)
	}
}

// scanTableRoots scans all Values inside a table for GC root pointers.
func scanTableRoots(t *Table, visitor func(unsafe.Pointer), seen map[uintptr]struct{}) {
	if t == nil {
		return
	}

	// Array part (only ArrayMixed can contain pointers)
	if t.arrayKind == ArrayMixed && t.array != nil {
		for _, v := range t.array {
			ScanValueRoots(v, visitor, seen)
		}
	}
	// String-keyed values (svals)
	if t.svals != nil {
		for _, v := range t.svals {
			ScanValueRoots(v, visitor, seen)
		}
	}
	// String map values
	for _, v := range t.smap {
		ScanValueRoots(v, visitor, seen)
	}
	// Integer map values
	for _, v := range t.imap {
		ScanValueRoots(v, visitor, seen)
	}
	// General hash values (both keys and values can be pointers)
	for k, v := range t.hash {
		ScanValueRoots(k, visitor, seen)
		ScanValueRoots(v, visitor, seen)
	}
	// Metatable
	if t.metatable != nil {
		mp := unsafe.Pointer(t.metatable)
		addr := uintptr(mp)
		if _, already := seen[addr]; !already {
			seen[addr] = struct{}{}
			visitTableRoot(mp, visitor)
			scanTableRoots(t.metatable, visitor, seen)
		}
	}
}

// ScanTableRootsExported is the exported version of scanTableRoots.
// Used by the vm package to scan string metatables and other table roots.
func ScanTableRootsExported(t *Table, visitor func(unsafe.Pointer), seen map[uintptr]struct{}) {
	scanTableRoots(t, visitor, seen)
}

// GCRootLogSize returns the current number of entries in the root log (for diagnostics).
func GCRootLogSize() int64 {
	return atomic.LoadInt64(&gcLog.cursor)
}

// keepAliveIface registers a Go-heap pointer AND stores the full interface
// for later type recovery via lookupIface. Used only for AnyFunction/AnyCoroutine.
func keepAliveIface(p unsafe.Pointer, obj any) {
	keepAlive(p, obj)
	ifaceMu.Lock()
	ifaceRoots[uintptr(p)] = obj
	ifaceMu.Unlock()
}

// lookupIface retrieves the original interface{} stored for a given pointer.
// Used by Ptr()/Closure()/GoFunction() for interface-based function/coroutine values.
func lookupIface(p unsafe.Pointer) any {
	ifaceMu.Lock()
	v := ifaceRoots[uintptr(p)]
	ifaceMu.Unlock()
	return v
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

func NilValue() Value {
	return Value(valNil)
}

func BoolValue(b bool) Value {
	if b {
		return Value(valTrue)
	}
	return Value(valFalse)
}

func IntValue(i int64) Value {
	if i > maxInt48 || i < minInt48 {
		// Overflow: promote to float64 (matches LuaJIT semantics).
		return FloatValue(float64(i))
	}
	return Value(tagInt | (uint64(i) & payloadMask))
}

func FloatValue(f float64) Value {
	bits := math.Float64bits(f)
	// Canonicalize exotic NaN patterns that collide with our tag space.
	if bits&nanBits == nanBits {
		return Value(canonicalNaN)
	}
	return Value(bits)
}

func StringValue(s string) Value {
	sp := new(string)
	*sp = s
	p := unsafe.Pointer(sp)
	keepAlive(p, sp)
	return Value(tagPtr | ptrSubString | (uint64(uintptr(p)) & ptrAddrMask))
}

// ConcatValues joins VM/JIT concat operands with one exact-size builder growth.
// The common binary path avoids the temporary operand slice used below.
func ConcatValues(values []Value) Value {
	switch len(values) {
	case 0:
		return StringValue("")
	case 1:
		return StringValue(values[0].String())
	case 2:
		left := concatString(values[0])
		right := concatString(values[1])
		var b strings.Builder
		b.Grow(len(left) + len(right))
		b.WriteString(left)
		b.WriteString(right)
		return StringValue(b.String())
	}

	var local [8]string
	parts := local[:0]
	if len(values) > len(local) {
		parts = make([]string, 0, len(values))
	}
	total := 0
	for _, v := range values {
		s := concatString(v)
		total += len(s)
		parts = append(parts, s)
	}

	var b strings.Builder
	b.Grow(total)
	for _, s := range parts {
		b.WriteString(s)
	}
	return StringValue(b.String())
}

func concatString(v Value) string {
	if v.IsString() {
		return v.Str()
	}
	return v.String()
}

func TableValue(t *Table) Value {
	if t == nil {
		return Value(valNil)
	}
	p := unsafe.Pointer(t)
	if DefaultHeap == nil || !DefaultHeap.tablePointerInCurrentSlab(uintptr(p)) {
		keepAlive(p, t)
	}
	return Value(tagPtr | ptrSubTable | (uint64(uintptr(p)) & ptrAddrMask))
}

// iface is the memory layout of a Go interface{}/any value.
type iface struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

// FunctionValue stores a function value (either *Closure or *GoFunction or any
// other pointer type). The pointer sub-type bits distinguish Closure from
// GoFunction. For other types, we use ptrSubAnyFunction and store the full
// interface in gcRoots for later reconstruction.
func FunctionValue(f interface{}) Value {
	if f == nil {
		return Value(valNil)
	}
	switch fn := f.(type) {
	case *Closure:
		p := unsafe.Pointer(fn)
		keepAlive(p, f)
		return Value(tagPtr | ptrSubClosure | (uint64(uintptr(p)) & ptrAddrMask))
	case *GoFunction:
		p := unsafe.Pointer(fn)
		keepAlive(p, f)
		return Value(tagPtr | ptrSubGoFunction | (uint64(uintptr(p)) & ptrAddrMask))
	default:
		// Unknown function type (e.g. *vm.Closure) -- store via interface
		i := (*iface)(unsafe.Pointer(&f))
		p := i.data
		keepAliveIface(p, f) // store the full interface for later reconstruction
		return Value(tagPtr | ptrSubAnyFunction | (uint64(uintptr(p)) & ptrAddrMask))
	}
}

// VMClosureFunctionValue stores a vm.Closure pointer using ptrSubVMClosure (8).
// The JIT can fast-check sub-type == 8 to know this is a vm.Closure and safely
// dereference the 44-bit pointer to access Proto and CompiledCodePtr.
// The caller must pass the raw unsafe.Pointer to the vm.Closure struct.
// The original interface is stored via keepAliveIface for Go-side reconstruction.
func VMClosureFunctionValue(p unsafe.Pointer, f interface{}) Value {
	if p == nil {
		return Value(valNil)
	}
	keepAliveIface(p, f)
	return Value(tagPtr | ptrSubVMClosure | (uint64(uintptr(p)) & ptrAddrMask))
}

// VMClosureFastValue stores a VM-owned closure pointer without recording a
// recoverable interface entry. Use this for VM/JIT-created bytecode closures
// that are recovered through VMClosurePointer rather than Value.Ptr().
func VMClosureFastValue(p unsafe.Pointer) Value {
	if p == nil {
		return Value(valNil)
	}
	keepAlive(p, nil)
	return Value(tagPtr | ptrSubVMClosure | (uint64(uintptr(p)) & ptrAddrMask))
}

// VMClosurePointer returns the raw pointer stored by a VM closure value.
//
// The runtime package cannot name internal/vm.Closure without creating an
// import cycle, so VM/JIT callers cast the returned pointer in their package.
// Values created by VMClosureFunctionValue can still be reconstructed through
// Ptr(); values created by VMClosureFastValue intentionally cannot.
func (v Value) VMClosurePointer() unsafe.Pointer {
	if uint64(v)&tagMask != tagPtr || v.ptrSubType() != ptrSubVMClosure {
		return nil
	}
	return v.ptrPayload()
}

func CoroutineValue(c *Coroutine) Value {
	if c == nil {
		return Value(valNil)
	}
	p := unsafe.Pointer(c)
	keepAlive(p, c)
	return Value(tagPtr | ptrSubCoroutine | (uint64(uintptr(p)) & ptrAddrMask))
}

// AnyCoroutineValue stores a coroutine value from an arbitrary pointer type
// (e.g. *VMCoroutine from the vm package).
func AnyCoroutineValue(c any) Value {
	if c == nil {
		return Value(valNil)
	}
	i := (*iface)(unsafe.Pointer(&c))
	p := i.data
	keepAliveIface(p, c) // store full interface for lookupIface
	return Value(tagPtr | ptrSubAnyCoro | (uint64(uintptr(p)) & ptrAddrMask))
}

// VMCoroutineValue stores a VM-owned coroutine pointer. Hot VM paths recover
// the raw pointer through AnyCoroutinePointer, while Ptr still works for callers
// that use the public Value API.
func VMCoroutineValue(p unsafe.Pointer, c any) Value {
	if p == nil {
		return Value(valNil)
	}
	keepAliveIface(p, c)
	return Value(tagPtr | ptrSubAnyCoro | (uint64(uintptr(p)) & ptrAddrMask))
}

func ChannelValue(ch *Channel) Value {
	if ch == nil {
		return Value(valNil)
	}
	p := unsafe.Pointer(ch)
	keepAlive(p, ch)
	return Value(tagPtr | ptrSubChannel | (uint64(uintptr(p)) & ptrAddrMask))
}

// ---------------------------------------------------------------------------
// Internal helpers for NaN-box decoding
// ---------------------------------------------------------------------------

// ptrPayload extracts the raw pointer from a pointer-tagged Value.
func (v Value) ptrPayload() unsafe.Pointer {
	return unsafe.Pointer(uintptr(uint64(v) & ptrAddrMask))
}

// ptrSubType extracts the pointer sub-type bits (44-47) from a pointer-tagged Value.
func (v Value) ptrSubType() uint64 {
	return uint64(v) & ptrSubMask
}

// ---------------------------------------------------------------------------
// In-place mutation (hot-loop optimization)
// ---------------------------------------------------------------------------

// SetInt updates a Value to an integer in place.
func (v *Value) SetInt(i int64) {
	if i > maxInt48 || i < minInt48 {
		*v = FloatValue(float64(i))
	} else {
		*v = Value(tagInt | (uint64(i) & payloadMask))
	}
}

// SetIntUnchecked updates a Value to an integer without range checking.
// Only safe when the caller guarantees |i| < 2^47 (e.g., FORLOOP counters).
func (v *Value) SetIntUnchecked(i int64) {
	*v = Value(tagInt | (uint64(i) & payloadMask))
}

// ---------------------------------------------------------------------------
// Pointer-receiver fast paths (avoid copies in VM hot loop)
// ---------------------------------------------------------------------------

func (v *Value) RawType() ValueType { return v.Type() }

func (v *Value) RawInt() int64 {
	// Branchless sign-extend 48-bit integer to 64-bit.
	// Arithmetic shift: (raw << 16) >> 16 sign-extends bit 47.
	return int64(uint64(*v)<<16) >> 16
}

func (v *Value) RawFloat() float64 { return math.Float64frombits(uint64(*v)) }

func AddInts(dst, a, b *Value) bool {
	if a.IsInt() && b.IsInt() {
		dst.SetInt(a.RawInt() + b.RawInt())
		return true
	}
	return false
}

// AddNums tries to add *a + *b as numbers (int or float), storing result in *dst.
func AddNums(dst, a, b *Value) bool {
	if a.IsInt() && b.IsInt() {
		dst.SetInt(a.RawInt() + b.RawInt())
		return true
	}
	if a.IsNumber() && b.IsNumber() {
		*dst = FloatValue(a.Number() + b.Number())
		return true
	}
	return false
}

func SubInts(dst, a, b *Value) bool {
	if a.IsInt() && b.IsInt() {
		dst.SetInt(a.RawInt() - b.RawInt())
		return true
	}
	return false
}

func SubNums(dst, a, b *Value) bool {
	if a.IsInt() && b.IsInt() {
		dst.SetInt(a.RawInt() - b.RawInt())
		return true
	}
	if a.IsNumber() && b.IsNumber() {
		*dst = FloatValue(a.Number() - b.Number())
		return true
	}
	return false
}

func MulInts(dst, a, b *Value) bool {
	if a.IsInt() && b.IsInt() {
		dst.SetInt(a.RawInt() * b.RawInt())
		return true
	}
	return false
}

func MulNums(dst, a, b *Value) bool {
	if a.IsInt() && b.IsInt() {
		dst.SetInt(a.RawInt() * b.RawInt())
		return true
	}
	if a.IsNumber() && b.IsNumber() {
		*dst = FloatValue(a.Number() * b.Number())
		return true
	}
	return false
}

func DivNums(dst, a, b *Value) bool {
	// DIV always returns float in Lua/GScript semantics (5/2 = 2.5).
	if a.IsInt() && b.IsInt() {
		*dst = FloatValue(float64(a.Int()) / float64(b.Int()))
		return true
	}
	if a.IsNumber() && b.IsNumber() {
		*dst = FloatValue(a.Number() / b.Number())
		return true
	}
	return false
}

func LTInts(a, b *Value) (bool, bool) {
	if a.IsInt() && b.IsInt() {
		return a.Int() < b.Int(), true
	}
	return false, false
}

func LEInts(a, b *Value) (bool, bool) {
	if a.IsInt() && b.IsInt() {
		return a.Int() <= b.Int(), true
	}
	return false, false
}

// ---------------------------------------------------------------------------
// Type checks
// ---------------------------------------------------------------------------

func (v Value) Type() ValueType {
	bits := uint64(v)

	// Float: bits 50-62 are NOT all set.
	if bits&nanBits != nanBits {
		return TypeFloat
	}

	// Tagged value: check tag bits.
	tag := bits & tagMask
	switch tag {
	case tagNil:
		return TypeNil
	case tagBool:
		return TypeBool
	case tagInt:
		return TypeInt
	case tagPtr:
		// Determine specific pointer type from sub-type bits.
		sub := bits & ptrSubMask
		switch sub {
		case ptrSubTable:
			return TypeTable
		case ptrSubString:
			return TypeString
		case ptrSubClosure, ptrSubGoFunction, ptrSubAnyFunction, ptrSubVMClosure:
			return TypeFunction
		case ptrSubCoroutine, ptrSubAnyCoro:
			return TypeCoroutine
		case ptrSubChannel:
			return TypeChannel
		default:
			return TypeTable // fallback
		}
	default:
		return TypeNil
	}
}

func (v Value) IsNil() bool    { return uint64(v) == valNil }
func (v Value) IsBool() bool   { return uint64(v)&tagMask == tagBool }
func (v Value) IsInt() bool    { return uint64(v)&tagMask == tagInt }
func (v Value) IsFloat() bool  { return uint64(v)&nanBits != nanBits }
func (v Value) IsNumber() bool { return v.IsFloat() || v.IsInt() }

func (v Value) IsString() bool {
	return uint64(v)&tagMask == tagPtr && v.ptrSubType() == ptrSubString
}

func (v Value) IsTable() bool {
	return uint64(v)&tagMask == tagPtr && v.ptrSubType() == ptrSubTable
}

func (v Value) IsFunction() bool {
	if uint64(v)&tagMask != tagPtr {
		return false
	}
	sub := v.ptrSubType()
	return sub == ptrSubClosure || sub == ptrSubGoFunction || sub == ptrSubAnyFunction || sub == ptrSubVMClosure
}

func (v Value) IsCoroutine() bool {
	if uint64(v)&tagMask != tagPtr {
		return false
	}
	sub := v.ptrSubType()
	return sub == ptrSubCoroutine || sub == ptrSubAnyCoro
}

// AnyCoroutinePointer returns the raw data pointer stored by AnyCoroutineValue.
// It is intentionally narrower than Ptr(): VM coroutine hot paths only need to
// recover their own concrete pointer and should not take the ifaceRoots mutex.
func (v Value) AnyCoroutinePointer() unsafe.Pointer {
	if uint64(v)&tagMask != tagPtr || v.ptrSubType() != ptrSubAnyCoro {
		return nil
	}
	return v.ptrPayload()
}

func (v Value) IsChannel() bool {
	return uint64(v)&tagMask == tagPtr && v.ptrSubType() == ptrSubChannel
}

// ---------------------------------------------------------------------------
// Value accessors
// ---------------------------------------------------------------------------

func (v Value) Bool() bool {
	return uint64(v)&1 != 0
}

func (v Value) Int() int64 {
	// Branchless sign-extend 48-bit integer to 64-bit.
	return int64(uint64(v)<<16) >> 16
}

func (v Value) Float() float64 {
	return math.Float64frombits(uint64(v))
}

func (v Value) Number() float64 {
	if v.IsInt() {
		return float64(v.Int())
	}
	return math.Float64frombits(uint64(v))
}

func (v Value) Str() string {
	if !v.IsString() {
		return ""
	}
	p := v.ptrPayload()
	if p == nil {
		return ""
	}
	return *(*string)(p)
}

func (v Value) Table() *Table {
	if !v.IsTable() {
		return nil
	}
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	return (*Table)(p)
}

// Closure returns the value as *runtime.Closure, or nil.
func (v Value) Closure() *Closure {
	if uint64(v)&tagMask != tagPtr {
		return nil
	}
	sub := v.ptrSubType()
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	switch sub {
	case ptrSubClosure:
		return (*Closure)(p)
	case ptrSubAnyFunction:
		// Recover from gcRoots and type-assert.
		if obj := lookupIface(p); obj != nil {
			c, _ := obj.(*Closure)
			return c
		}
		return nil
	default:
		return nil
	}
}

// GoFunction returns the value as *GoFunction, or nil.
func (v Value) GoFunction() *GoFunction {
	if uint64(v)&tagMask != tagPtr {
		return nil
	}
	sub := v.ptrSubType()
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	switch sub {
	case ptrSubGoFunction:
		return (*GoFunction)(p)
	case ptrSubAnyFunction:
		if obj := lookupIface(p); obj != nil {
			gf, _ := obj.(*GoFunction)
			return gf
		}
		return nil
	default:
		return nil
	}
}

// Ptr reconstructs the original interface{} value from the NaN-boxed pointer.
func (v Value) Ptr() any {
	if uint64(v)&tagMask != tagPtr {
		return nil
	}
	sub := v.ptrSubType()
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	switch sub {
	case ptrSubTable:
		return (*Table)(p)
	case ptrSubString:
		return *(*string)(p)
	case ptrSubClosure:
		return (*Closure)(p)
	case ptrSubGoFunction:
		return (*GoFunction)(p)
	case ptrSubCoroutine:
		return (*Coroutine)(p)
	case ptrSubChannel:
		return (*Channel)(p)
	case ptrSubAnyFunction, ptrSubAnyCoro, ptrSubVMClosure:
		// Recover the original interface from gcRoots.
		return lookupIface(p)
	default:
		return nil
	}
}

func (v Value) Coroutine() *Coroutine {
	if uint64(v)&tagMask != tagPtr {
		return nil
	}
	sub := v.ptrSubType()
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	switch sub {
	case ptrSubCoroutine:
		return (*Coroutine)(p)
	case ptrSubAnyCoro:
		if obj := lookupIface(p); obj != nil {
			c, _ := obj.(*Coroutine)
			return c
		}
		return nil
	default:
		return nil
	}
}

func (v Value) Channel() *Channel {
	if !v.IsChannel() {
		return nil
	}
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	return (*Channel)(p)
}

// ---------------------------------------------------------------------------
// TypeName, Truthiness, Equality
// ---------------------------------------------------------------------------

func (v Value) TypeName() string {
	switch v.Type() {
	case TypeNil:
		return "nil"
	case TypeBool:
		return "boolean"
	case TypeInt, TypeFloat:
		return "number"
	case TypeString:
		return "string"
	case TypeTable:
		return "table"
	case TypeFunction:
		return "function"
	case TypeCoroutine:
		return "coroutine"
	case TypeChannel:
		return "channel"
	default:
		return "unknown"
	}
}

func (v Value) Truthy() bool {
	return uint64(v) != valNil && uint64(v) != valFalse
}

func (v Value) Equal(other Value) bool {
	// Fast path: identical bit patterns.
	if uint64(v) == uint64(other) {
		return true
	}

	vt := v.Type()
	ot := other.Type()

	if vt != ot {
		// Cross-type number equality (int == float).
		if v.IsNumber() && other.IsNumber() {
			return v.Number() == other.Number()
		}
		return false
	}

	switch vt {
	case TypeNil:
		return true
	case TypeBool:
		return v.Bool() == other.Bool()
	case TypeInt:
		return v.Int() == other.Int()
	case TypeFloat:
		return v.Float() == other.Float()
	case TypeString:
		return v.Str() == other.Str()
	case TypeTable, TypeFunction, TypeCoroutine, TypeChannel:
		// Pointer identity: compare the raw address (strip sub-type bits).
		return v.ptrPayload() == other.ptrPayload()
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Arithmetic / conversion helpers
// ---------------------------------------------------------------------------

func (v Value) ToNumber() (Value, bool) {
	if v.IsInt() || v.IsFloat() {
		return v, true
	}
	if !v.IsString() {
		return NilValue(), false
	}
	s := strings.TrimSpace(v.Str())
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return IntValue(i), true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return FloatValue(f), true
	}
	return NilValue(), false
}

// ---------------------------------------------------------------------------
// fmt.Stringer
// ---------------------------------------------------------------------------

func (v Value) String() string {
	switch v.Type() {
	case TypeNil:
		return "nil"
	case TypeBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case TypeInt:
		return strconv.FormatInt(v.Int(), 10)
	case TypeFloat:
		f := v.Float()
		s := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.Contains(s, ".") && !strings.Contains(s, "e") && !strings.Contains(s, "E") && !strings.Contains(s, "Inf") && !strings.Contains(s, "NaN") {
			s += ".0"
		}
		return s
	case TypeString:
		return v.Str()
	case TypeTable:
		return fmt.Sprintf("table: %p", v.ptrPayload())
	case TypeFunction:
		if c := v.Closure(); c != nil {
			return fmt.Sprintf("function: %p", c)
		}
		if gf := v.GoFunction(); gf != nil {
			return fmt.Sprintf("function: %s", gf.Name)
		}
		return "function: <unknown>"
	case TypeCoroutine:
		return fmt.Sprintf("coroutine: %p", v.ptrPayload())
	case TypeChannel:
		return fmt.Sprintf("channel: %p", v.ptrPayload())
	default:
		return "unknown"
	}
}

func (v Value) hashKey() Value {
	return v
}

func (v Value) LessThan(other Value) (bool, bool) {
	if v.IsNumber() && other.IsNumber() {
		return v.Number() < other.Number(), true
	}
	if v.IsString() && other.IsString() {
		return v.Str() < other.Str(), true
	}
	return false, false
}

func floatIsInt(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	return f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64
}

// ---------------------------------------------------------------------------
// Raw access (for VM / JIT)
// ---------------------------------------------------------------------------

// Raw returns the underlying uint64 bits.
func (v Value) Raw() uint64 {
	return uint64(v)
}

// FromRaw constructs a Value from raw uint64 bits (no validation).
func FromRaw(bits uint64) Value {
	return Value(bits)
}

// ---------------------------------------------------------------------------
// NaN-boxing tag constants (exported for JIT / nanbox package)
// ---------------------------------------------------------------------------

const (
	NanBits     = nanBits
	TagNil      = tagNil
	TagBool     = tagBool
	TagInt      = tagInt
	TagPtr      = tagPtr
	TagMask     = tagMask
	PayloadMask = payloadMask
	ValNil      = valNil
	ValFalse    = valFalse
	ValTrue     = valTrue
)

// MakeNilSlice creates a []Value of length n with all elements set to NilValue().
// With NaN-boxing, Go's zero value (0) is float64(0.0), NOT nil.
// Use this instead of make([]Value, n) whenever uninitialized slots must read as nil.
func MakeNilSlice(n int) []Value {
	s := make([]Value, n)
	nv := NilValue()
	for i := range s {
		s[i] = nv
	}
	return s
}

// MakeNilSliceCap creates a []Value of length n and capacity cap with all elements set to NilValue().
func MakeNilSliceCap(n, cap int) []Value {
	s := make([]Value, n, cap)
	nv := NilValue()
	for i := range s {
		s[i] = nv
	}
	return s
}
