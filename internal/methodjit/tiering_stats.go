//go:build darwin && arm64

package methodjit

import "sort"

// Tier2Count returns the number of functions compiled at Tier 2.
func (tm *TieringManager) Tier2Count() int {
	return len(tm.tier2Compiled)
}

// Tier1Count returns the number of functions compiled at Tier 1.
func (tm *TieringManager) Tier1Count() int {
	return tm.tier1.CompiledCount()
}

// Tier2Compiled returns the names of protos that successfully compiled at
// Tier 2, sorted alphabetically. Used by CLI diagnostics (-jit-stats).
func (tm *TieringManager) Tier2Compiled() []string {
	names := make([]string, 0, len(tm.tier2Compiled))
	for proto := range tm.tier2Compiled {
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
	names := make([]string, 0, len(tm.tier2Compiled))
	for proto := range tm.tier2Compiled {
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
	out := make(map[string]string, len(tm.tier2FailReason))
	for proto, reason := range tm.tier2FailReason {
		out[proto.Name] = reason
	}
	return out
}

// Tier2Attempted returns the total number of Tier 2 compilation attempts
// (both successes and failures).
func (tm *TieringManager) Tier2Attempted() int {
	return tm.tier2Attempts
}
