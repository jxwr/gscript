// tier.go defines the multi-tier JIT compilation architecture.
//
// Tier 0: Interpreter (internal/vm/)
//   Executes all bytecodes. Collects type feedback. Always correct.
//   Promotes to Tier 1 at 2 calls (fast startup).
//
// Tier 1: Baseline JIT (internal/methodjit/tier1_*.go)
//   Compiles all ops natively using simple templates.
//   No optimization passes. Fast to compile. ~3-5x speedup.
//   Promotes to Tier 2 at 2 calls (via TieringManager).
//
// Tier 2: Optimizing JIT (current methodjit pipeline)
//   Full optimization: TypeSpec, ConstProp, DCE, Inline.
//   Slow to compile. ~10-30x speedup on numeric loops.
//   Uses exit-resume for complex ops.

package methodjit

const (
	Tier0Threshold = 0   // interpreter always available
	Tier1Threshold = 2   // baseline JIT after 2 calls (PLANNED)
	Tier2Threshold = 100 // optimizing JIT after 100 calls
)
