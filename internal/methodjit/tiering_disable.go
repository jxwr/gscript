//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

type tier0DisableDecision struct {
	reason         string
	fallbackReason string
	callee         *vm.FuncProto
}

func (tm *TieringManager) disableJITForTier0Policy(proto *vm.FuncProto, d tier0DisableDecision) {
	proto.JITDisabled = true
	fields := map[string]any{
		"reason":     d.reason,
		"call_count": proto.CallCount,
	}
	tierFields := map[string]any{"reason": d.reason}
	fallbackFields := map[string]any{
		"reason": d.fallbackReason,
		"target": "interpreter",
	}
	if d.callee != nil {
		calleeName := "<anonymous>"
		if d.callee.Name != "" {
			calleeName = d.callee.Name
		}
		fields["callee"] = calleeName
		fields["callee_addr"] = fmt.Sprintf("%p", d.callee)
		tierFields["callee"] = calleeName
		fallbackFields["callee"] = calleeName
	}
	tm.traceEvent("runtime_disable", "jit", proto, fields)
	tm.traceEvent("tier1_skip", "tier1", proto, tierFields)
	tm.traceEvent("fallback", "tier0", proto, fallbackFields)
}
