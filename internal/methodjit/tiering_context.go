//go:build darwin && arm64

package methodjit

import (
	"sync"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

var tier2ExecContextPool = sync.Pool{
	New: func() any {
		return new(ExecContext)
	},
}

func getTier2ExecContext() *ExecContext {
	ctx := tier2ExecContextPool.Get().(*ExecContext)
	*ctx = ExecContext{}
	return ctx
}

func putTier2ExecContext(ctx *ExecContext) {
	if ctx == nil {
		return
	}
	*ctx = ExecContext{}
	tier2ExecContextPool.Put(ctx)
}

func (tm *TieringManager) acquireTier2ExecContext() (*ExecContext, bool) {
	if tm != nil && !tm.tier2CtxBusy {
		tm.tier2CtxBusy = true
		tm.tier2Ctx = ExecContext{}
		return &tm.tier2Ctx, false
	}
	return getTier2ExecContext(), true
}

func (tm *TieringManager) releaseTier2ExecContext(ctx *ExecContext, pooled bool) {
	if pooled {
		putTier2ExecContext(ctx)
		return
	}
	if tm != nil && ctx == &tm.tier2Ctx {
		tm.tier2CtxBusy = false
	}
}

func (tm *TieringManager) setTier2FieldCacheContext(ctx *ExecContext, proto *vm.FuncProto) {
	setTier2ProtoCacheContext(ctx, proto)
}

func setTier2ProtoCacheContext(ctx *ExecContext, proto *vm.FuncProto) {
	if ctx == nil {
		return
	}
	ctx.BaselineFieldCache = 0
	ctx.BaselineFieldPolyCache = 0
	ctx.BaselineTableStringKeyCache = 0
	if proto == nil {
		return
	}
	if len(proto.FieldCache) > 0 {
		ctx.BaselineFieldCache = uintptr(unsafe.Pointer(&proto.FieldCache[0]))
	}
	if len(proto.FieldPolyCache) > 0 {
		ctx.BaselineFieldPolyCache = uintptr(unsafe.Pointer(&proto.FieldPolyCache[0]))
	}
	if len(proto.TableStringKeyCache) > 0 {
		ctx.BaselineTableStringKeyCache = uintptr(unsafe.Pointer(&proto.TableStringKeyCache[0]))
	}
}

func (tm *TieringManager) ensureTier2RegisterBudget(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto) []runtime.Value {
	if cf == nil || proto == nil || tm.callVM == nil {
		return regs
	}
	if cf.numRegs <= 0 {
		return regs
	}

	depthBudget := 0
	if cf.NumericParamCount > 0 && proto.HasSelfCalls {
		depthBudget = maxRawSelfCallDepth + 2
	} else if cf.TypedSelfABI.Eligible || cf.TypedPeerABI.Eligible {
		depthBudget = maxNativeCallDepth + 2
	}
	if depthBudget == 0 {
		return regs
	}

	// Raw and typed self recursion advance mRegRegs in native code instead
	// of pushing VM frames. Pre-grow the shared VM register file to cover the
	// bounded native recursion budget; otherwise the hot self-call path
	// repeatedly falls through ExitCallExit solely to let the VM grow this
	// slice. Typed entries still publish parameter homes so callee exits have
	// a complete VM frame inside the pre-grown window.
	needed := base + cf.numRegs*depthBudget + 1
	if needed <= len(regs) {
		return regs
	}
	return tm.callVM.EnsureRegs(needed)
}
