//go:build darwin && arm64

package jit

import (
	"fmt"
	"runtime"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// JIT compilation threshold: compile after this many calls.
const DefaultHotThreshold = 10

// debugCallExit enables verbose logging of call-exit handling in the JIT.
const debugCallExit = false
// debugCrossCall enables verbose logging of cross-function call handling.
const debugCrossCall = false

// compiledEntry holds a compiled function.
type compiledEntry struct {
	cf           *CompiledFunc
	fn           func(uintptr) int64 // purego wrapper (kept for backward compat)
	ptr          uintptr              // direct code pointer for callJIT trampoline
	totalExits   int                  // cumulative call-exit count
	totalCalls   int                  // cumulative execution count
	demoted      bool                 // true if function has been demoted (too many exits)
}

// blacklistedEntry is a sentinel compiledEntry stored in FuncProto.JITEntry
// to indicate the function is blacklisted (not worth JIT compiling).
// We use a package-level variable so its address is stable and unique.
var blacklistedEntry = compiledEntry{demoted: true}

// CallHandler executes an external function call on behalf of JIT code.
// Provided by the VM so the JIT executor can handle OP_CALL without direct VM access.
type CallHandler func(fnVal rt.Value, args []rt.Value) ([]rt.Value, error)

// GlobalsAccessor provides safe access to VM globals and register state.
type GlobalsAccessor interface {
	GetGlobal(name string) rt.Value
	SetGlobal(name string, val rt.Value)
	Regs() []rt.Value // returns the current register slice (may change after calls)
}

// crossCallSlot holds a callee's code pointer and constants pointer for direct ARM64 BLR calls.
// Slots are allocated by the compiler and updated when the callee is compiled.
// The JIT code loads from these slots to make direct calls without exiting to Go.
type crossCallSlot struct {
	codePtr      uintptr // callee's compiled code entry point (0 if not compiled)
	constantsPtr uintptr // callee's constants[0] pointer
}

// Engine manages JIT compilation and execution.
type Engine struct {
	entries     map[*vm.FuncProto]*compiledEntry
	blacklist   map[*vm.FuncProto]bool // functions known to not benefit from JIT
	threshold   int
	globals     map[string]rt.Value // reference to VM globals for function inlining
	callHandler CallHandler         // executes external function calls
	globalsAcc  GlobalsAccessor     // safe globals/regs access

	// Cross-call infrastructure: slots hold callee code pointers for direct BLR.
	// Updated when functions are compiled. JIT code loads from these slots.
	crossCallSlots    []*crossCallSlot             // all allocated slots
	crossCallByName   map[string][]*crossCallSlot  // name → slots to update when compiled
}

// NewEngine creates a new JIT engine.
func NewEngine() *Engine {
	return &Engine{
		entries:         make(map[*vm.FuncProto]*compiledEntry),
		blacklist:       make(map[*vm.FuncProto]bool),
		threshold:       DefaultHotThreshold,
		crossCallByName: make(map[string][]*crossCallSlot),
	}
}

// allocCrossCallSlot creates a new cross-call slot for the given callee name.
// If the callee is already compiled, the slot is pre-filled with its code pointer.
func (e *Engine) allocCrossCallSlot(calleeName string, calleeProto *vm.FuncProto) *crossCallSlot {
	slot := &crossCallSlot{}
	// Check if the callee is already compiled.
	if calleeProto != nil {
		if entry, ok := e.entries[calleeProto]; ok {
			slot.codePtr = entry.ptr
			if len(calleeProto.Constants) > 0 {
				slot.constantsPtr = uintptr(unsafe.Pointer(&calleeProto.Constants[0]))
			}
		}
	}
	e.crossCallSlots = append(e.crossCallSlots, slot)
	e.crossCallByName[calleeName] = append(e.crossCallByName[calleeName], slot)
	return slot
}

// updateCrossCallSlots fills in code pointers for all slots waiting for the given name.
func (e *Engine) updateCrossCallSlots(name string, entry *compiledEntry, proto *vm.FuncProto) {
	slots := e.crossCallByName[name]
	for _, slot := range slots {
		slot.codePtr = entry.ptr
		if len(proto.Constants) > 0 {
			slot.constantsPtr = uintptr(unsafe.Pointer(&proto.Constants[0]))
		}
	}
}

// SetGlobals sets the globals map for function inlining.
func (e *Engine) SetGlobals(globals map[string]rt.Value) {
	e.globals = globals
}

// SetThreshold sets the call count threshold for JIT compilation.
func (e *Engine) SetThreshold(n int) {
	e.threshold = n
}

// SetCallHandler sets the function that executes external calls for the JIT.
func (e *Engine) SetCallHandler(handler CallHandler) {
	e.callHandler = handler
}

// SetGlobalsAccessor sets the globals/regs accessor for call-exit handling.
func (e *Engine) SetGlobalsAccessor(acc GlobalsAccessor) {
	e.globalsAcc = acc
}

// shouldCompile checks if a function is worth JIT compiling.
// With call-exit support, functions with external calls are now viable:
// the JIT exits at call sites, the executor handles the call, then re-enters JIT.
// Only TFORCALL (generic for iterator) is still a permanent side-exit concern.
func shouldCompile(proto *vm.FuncProto) bool {
	if len(proto.Code) == 0 {
		return false
	}
	hasLoop := false
	hasTForCall := false
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_FORLOOP {
			hasLoop = true
		}
		if op == vm.OP_TFORCALL {
			hasTForCall = true
		}
	}
	// TFORCALL (generic for) is still a permanent side-exit.
	// Only compile if there's a loop to offset the cost.
	if hasTForCall && !hasLoop {
		return false
	}
	return true
}

// isSelfCall checks whether the CALL at callPC is a self-recursive call
// by looking backward for a GETGLOBAL that loads the function's own name.
func isSelfCall(proto *vm.FuncProto, callPC int) bool {
	if proto.Name == "" {
		return false
	}
	callA := vm.DecodeA(proto.Code[callPC])
	for pc := callPC - 1; pc >= 0; pc-- {
		inst := proto.Code[pc]
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		if op == vm.OP_GETGLOBAL && a == callA {
			bx := vm.DecodeBx(inst)
			if bx < len(proto.Constants) {
				return proto.Constants[bx].Str() == proto.Name
			}
			return false
		}
		// If the register is written by another instruction, stop searching.
		if a == callA && op != vm.OP_EQ && op != vm.OP_LT && op != vm.OP_LE && op != vm.OP_TEST {
			return false
		}
	}
	return false
}

// TryExecute attempts to JIT-execute a function.
// Returns (results, resumePC, ok).
// If ok is true, the function completed (results contains return values).
// If ok is false, the JIT bailed out at resumePC and the interpreter should take over.
func (e *Engine) TryExecute(proto *vm.FuncProto, regs []rt.Value, base int, callCount int) (results []rt.Value, resumePC int, ok bool) {
	// Fast path: check cached JIT entry on the FuncProto (avoids map lookups).
	var entry *compiledEntry
	if proto.JITEntry != nil {
		entry = (*compiledEntry)(proto.JITEntry)
		// blacklistedEntry sentinel or demoted: bail out immediately.
		if entry.demoted {
			return nil, 0, false
		}
	} else {
		// Cold path: not yet looked up. Check if hot enough to compile.
		if callCount < e.threshold {
			return nil, 0, false
		}
		// Check the entries map (needed for first compilation).
		var compiled bool
		entry, compiled = e.entries[proto]
		if !compiled {
			// Check if worth compiling.
			if !shouldCompile(proto) {
				e.blacklistProto(proto)
				return nil, 0, false
			}
			// Compile the function.
			cf, err := CompileWithEngine(proto, e)
			if err != nil {
				e.blacklistProto(proto)
				return nil, 0, false
			}
			entry = &compiledEntry{cf: cf, ptr: uintptr(cf.Code.Ptr())}
			e.entries[proto] = entry
			// Cache in FuncProto for future fast-path lookups.
			proto.JITEntry = unsafe.Pointer(entry)
			// Detect self-recursive calls for trace activation guard.
			for callPC := 0; callPC < len(proto.Code); callPC++ {
				if vm.DecodeOp(proto.Code[callPC]) == vm.OP_CALL && isSelfCall(proto, callPC) {
					proto.HasSelfCalls = true
					break
				}
			}
			// Update cross-call slots that were waiting for this function.
			if proto.Name != "" {
				e.updateCrossCallSlots(proto.Name, entry, proto)
			}
		} else {
			// Already compiled but JITEntry wasn't cached yet (shouldn't normally happen,
			// but handle gracefully).
			proto.JITEntry = unsafe.Pointer(entry)
			if entry.demoted {
				return nil, 0, false
			}
		}
	}

	// Prepare JIT context (reuse stack-local, only set needed fields).
	var ctx JITContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Exit-resume loop: JIT runs until normal return (0), permanent side-exit (1),
	// or call-exit (2). On call-exit, the executor handles the instruction and
	// re-enters JIT at the next PC.
	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	exitCount := 0
	for {
		// GC safe point: all register writes from previous iteration are complete.
		rt.CheckGC()

		exitCode := callJIT(entry.ptr, ctxPtr)
		runtime.KeepAlive(ctx)

		if debugCallExit {
			op := vm.OpName(vm.DecodeOp(proto.Code[ctx.ExitPC]))
			fmt.Printf("[JIT] %s: exit=%d pc=%d(%s) resume=%d ptr=0x%x ctx=0x%x\n",
				proto.Name, exitCode, ctx.ExitPC, op, ctx.ResumePC, entry.ptr, ctxPtr)
		}

		switch exitCode {
		case 0:
			// Normal return. Update exit stats.
			entry.totalCalls++
			entry.totalExits += exitCount
			// After enough samples, demote if exit ratio is too high.
			// A high exit/call ratio means JIT is spending more time in
			// exit/re-enter overhead than executing native code.
			if entry.totalCalls >= 8 && entry.totalExits > entry.totalCalls*20 {
				entry.demoted = true
				// Demoted → Trace JIT should optimize this function's loops
				proto.JITSideExited = true
			}

			retBase := int(ctx.RetBase)
			retCount := int(ctx.RetCount)
			if retCount == 0 {
				return nil, 0, true
			}
			ret := make([]rt.Value, retCount)
			for i := 0; i < retCount; i++ {
				ret[i] = regs[base+retBase+i]
			}
			return ret, 0, true

		case 1:
			// Permanent side exit — interpreter takes over.
			proto.JITSideExited = true
			return nil, int(ctx.ExitPC), false

		case 2:
			// Call-exit: handle the instruction in Go, then re-enter JIT.
			exitCount++
			newRegs, nextPC, err := e.handleCallExit(proto, regs, base, &ctx)
			if err != nil {
				// Call-exit failed — demote this function so the trace JIT
				// can optimize its loops instead of retrying Method JIT forever.
				entry.demoted = true
				proto.JITSideExited = true
				return nil, int(ctx.ExitPC), false
			}
			if newRegs != nil {
				regs = newRegs
			}

			// Batch consecutive call-exit opcodes to avoid repeated JIT exit/re-entry.
			// Common patterns: GETFIELD→GETTABLE→GETFIELD in chess AI hot paths.
			lastExitOp := vm.DecodeOp(proto.Code[int(ctx.ExitPC)])
			for nextPC < len(proto.Code) {
				nextOp := vm.DecodeOp(proto.Code[nextPC])
				if !isCallExitOp(nextOp) {
					break
				}
				// Don't batch comparison ops (they have special resume dispatch)
				if nextOp == vm.OP_EQ || nextOp == vm.OP_LT || nextOp == vm.OP_LE {
					break
				}
				// Don't batch OP_CALL (may change register file pointer)
				if nextOp == vm.OP_CALL {
					break
				}
				ctx.ExitPC = int64(nextPC)
				newRegs2, nextPC2, err2 := e.handleCallExit(proto, regs, base, &ctx)
				if err2 != nil {
					break
				}
				if newRegs2 != nil {
					regs = newRegs2
				}
				exitCount++
				lastExitOp = nextOp
				nextPC = nextPC2
			}

			// Set ResumePC for JIT dispatch table.
			if lastExitOp == vm.OP_EQ || lastExitOp == vm.OP_LT || lastExitOp == vm.OP_LE {
				ctx.ResumePC = int64(nextPC | 0x8000)
			} else {
				ctx.ResumePC = int64(nextPC)
			}
			ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
			ctxPtr = uintptr(unsafe.Pointer(&ctx))
			if debugCallExit {
				fmt.Printf("[JIT] %s: re-enter at ResumePC=%d (ctx.ResumePC=%d)\n",
					proto.Name, nextPC, ctx.ResumePC)
			}
			continue

		default:
			return nil, int(ctx.ExitPC), false
		}
	}
}

// blacklistProto marks a function as not worth JIT compiling.
// Sets the sentinel on both the map and the FuncProto cache.
func (e *Engine) blacklistProto(proto *vm.FuncProto) {
	e.blacklist[proto] = true
	proto.JITEntry = unsafe.Pointer(&blacklistedEntry)
}

// Free releases compiled code owned by this engine.
// Clears JITEntry pointers on FuncProtos to avoid dangling references.
func (e *Engine) Free() {
	for proto, entry := range e.entries {
		proto.JITEntry = nil
		if entry != nil && entry.cf != nil {
			entry.cf.Code.Free()
		}
	}
	// Also clear blacklisted entries' JITEntry pointers.
	for proto := range e.blacklist {
		proto.JITEntry = nil
	}
	e.entries = nil
}
