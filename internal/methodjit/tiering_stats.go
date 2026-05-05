//go:build darwin && arm64

package methodjit

import "sort"

// Tier2Count returns the number of functions compiled at Tier 2.
func (tm *TieringManager) Tier2Count() int {
	return len(tm.tier2CompiledSnapshot())
}

// Tier1Count returns the number of functions compiled at Tier 1.
func (tm *TieringManager) Tier1Count() int {
	return tm.tier1.CompiledCount()
}

// Tier2Compiled returns the names of protos that successfully compiled at
// Tier 2, sorted alphabetically. Used by CLI diagnostics (-jit-stats).
func (tm *TieringManager) Tier2Compiled() []string {
	compiled := tm.tier2CompiledSnapshot()
	names := make([]string, 0, len(compiled))
	for proto := range compiled {
		names = append(names, proto.Name)
	}
	sort.Strings(names)
	return names
}

// Tier2Entered returns the subset of Tier2Compiled() protos whose native
// prologue ran at least once (proto.EnteredTier2 != 0). Set by the
// emitTier2EntryMark sequence (R146). Used by CLI diagnostics
// (-jit-stats) and by bench harnesses to confirm that the hot function
// actually executed through Tier 2 native code — not just that it was
// compiled.
func (tm *TieringManager) Tier2Entered() []string {
	compiled := tm.tier2CompiledSnapshot()
	names := make([]string, 0, len(compiled))
	for proto := range compiled {
		if proto.EnteredTier2 != 0 {
			names = append(names, proto.Name)
		}
	}
	sort.Strings(names)
	return names
}

// Tier2Failed returns a map of proto name -> error message for Tier 2
// compilations that failed. Used by CLI diagnostics (-jit-stats).
func (tm *TieringManager) Tier2Failed() map[string]string {
	reasons := tm.tier2FailReasonSnapshot()
	out := make(map[string]string, len(reasons))
	for proto, reason := range reasons {
		out[proto.Name] = reason
	}
	return out
}

// Tier2Attempted returns the total number of Tier 2 compilation attempts
// (both successes and failures).
func (tm *TieringManager) Tier2Attempted() int {
	return tm.tier2Attempts
}
