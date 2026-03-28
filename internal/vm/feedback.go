// feedback.go implements per-instruction type feedback collection for the Method JIT.
// The interpreter records operand and result types for arithmetic, comparison,
// table access, and call instructions. The Method JIT reads this feedback to
// specialize operations (e.g., Add -> AddInt when operands are always integers).
//
// Type lattice: Unobserved -> {Int,Float,String,Bool,Table,Function} -> Any.
// Monotonic: once a slot observes a second distinct type, it becomes Any and
// stays there forever to prevent deopt-reopt cycles.
package vm

import "github.com/gscript/gscript/internal/runtime"

// FeedbackType is a monotonic type lattice for type profiling.
// Transitions: Unobserved -> concrete type -> Any. Never narrows.
type FeedbackType uint8

const (
	FBUnobserved FeedbackType = iota // no observations yet
	FBInt                             // only int seen
	FBFloat                           // only float seen
	FBString                          // only string seen
	FBBool                            // only bool seen
	FBTable                           // only table seen
	FBFunction                        // only function seen
	FBAny                             // multiple types seen (megamorphic)
)

// feedbackFromValueType maps runtime.ValueType to FeedbackType.
// This avoids a switch in the hot path.
var feedbackFromValueType = [9]FeedbackType{
	runtime.TypeNil:       FBAny, // nil is rare; treat as polymorphic
	runtime.TypeBool:      FBBool,
	runtime.TypeInt:       FBInt,
	runtime.TypeFloat:     FBFloat,
	runtime.TypeString:    FBString,
	runtime.TypeTable:     FBTable,
	runtime.TypeFunction:  FBFunction,
	runtime.TypeCoroutine: FBAny, // rare; treat as polymorphic
	runtime.TypeChannel:   FBAny, // rare; treat as polymorphic
}

// Observe records a new type observation. Monotonic: never narrows.
// If the FeedbackType is already FBAny, this is a no-op.
// If the new type matches the current type, no change.
// If the new type differs from the current concrete type, widens to FBAny.
func (ft *FeedbackType) Observe(vt runtime.ValueType) {
	cur := *ft
	if cur == FBAny {
		return
	}
	observed := feedbackFromValueType[vt]
	if cur == FBUnobserved {
		*ft = observed
		return
	}
	if cur != observed {
		*ft = FBAny
	}
}

// TypeFeedback records observed types for one bytecode instruction.
// For arithmetic/comparison: Left = B operand, Right = C operand, Result = A destination.
// For table access: Left = table type, Right = key type, Result = value type.
// For calls: Left = callee type, Right/Result unused.
type TypeFeedback struct {
	Left   FeedbackType // type of left operand (B in ABC format)
	Right  FeedbackType // type of right operand (C in ABC format)
	Result FeedbackType // type of result (A in ABC format)
}

// FeedbackVector is per-function type feedback, indexed by bytecode PC.
type FeedbackVector []TypeFeedback

// NewFeedbackVector creates a zero-initialized feedback vector for a function.
// All entries start as FBUnobserved.
func NewFeedbackVector(codeLen int) FeedbackVector {
	return make(FeedbackVector, codeLen)
}
