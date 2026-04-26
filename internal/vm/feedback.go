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
	FBInt                            // only int seen
	FBFloat                          // only float seen
	FBString                         // only string seen
	FBBool                           // only bool seen
	FBTable                          // only table seen
	FBFunction                       // only function seen
	FBAny                            // multiple types seen (megamorphic)
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

// ArrayKind feedback encoding for TypeFeedback.Kind.
// 0 = unobserved, 1..4 = monomorphic (value = 1 + runtime.ArrayKind), 0xFF = polymorphic.
const (
	FBKindUnobserved  uint8 = 0
	FBKindMixed       uint8 = 1 // 1 + ArrayMixed(0)
	FBKindInt         uint8 = 2 // 1 + ArrayInt(1)
	FBKindFloat       uint8 = 3 // 1 + ArrayFloat(2)
	FBKindBool        uint8 = 4 // 1 + ArrayBool(3)
	FBKindPolymorphic uint8 = 0xFF
)

// TypeFeedback records observed types for one bytecode instruction.
// For arithmetic/comparison: Left = B operand, Right = C operand, Result = A destination.
// For table access: Left = table type, Right = key type, Result = value type.
// For calls: Left = callee type, Right/Result unused.
type TypeFeedback struct {
	Left   FeedbackType // type of left operand (B in ABC format)
	Right  FeedbackType // type of right operand (C in ABC format)
	Result FeedbackType // type of result (A in ABC format)
	Kind   uint8        // observed array kind for GETTABLE/SETTABLE (0=unobserved, 1+kind for stable, 0xFF=polymorphic)
}

// TableKeyFeedback records non-type table-access facts for one bytecode PC.
// It intentionally lives outside TypeFeedback so the 4-byte type/kind feedback
// stays compact for the hot arithmetic and guard-specialization path.
type TableKeyFeedback struct {
	MaxIntKey uint32
	HasIntKey bool
}

// ObserveKind records an array kind observation. Monotonic like Observe:
// Unobserved -> concrete kind -> Polymorphic.
func (tf *TypeFeedback) ObserveKind(arrayKind uint8) {
	encoded := arrayKind + 1 // shift so 0 means unobserved
	cur := tf.Kind
	if cur == FBKindPolymorphic {
		return
	}
	if cur == FBKindUnobserved {
		tf.Kind = encoded
		return
	}
	if cur != encoded {
		tf.Kind = FBKindPolymorphic
	}
}

// ObserveIntKey records the largest non-negative integer key observed at a
// table access site. Negative or non-int keys are ignored because they cannot
// drive array-part capacity hints.
func (tk *TableKeyFeedback) ObserveIntKey(key runtime.Value) {
	if key.Type() != runtime.TypeInt {
		return
	}
	n := key.Int()
	if n < 0 || n > int64(^uint32(0)) {
		return
	}
	u := uint32(n)
	if !tk.HasIntKey || u > tk.MaxIntKey {
		tk.MaxIntKey = u
		tk.HasIntKey = true
	}
}

// FeedbackVector is per-function type feedback, indexed by bytecode PC.
type FeedbackVector []TypeFeedback

// TableKeyFeedbackVector is per-function table key feedback, indexed by PC.
type TableKeyFeedbackVector []TableKeyFeedback

// NewFeedbackVector creates a zero-initialized feedback vector for a function.
// All entries start as FBUnobserved.
func NewFeedbackVector(codeLen int) FeedbackVector {
	return make(FeedbackVector, codeLen)
}

// NewTableKeyFeedbackVector creates a zero-initialized table key feedback vector.
func NewTableKeyFeedbackVector(codeLen int) TableKeyFeedbackVector {
	return make(TableKeyFeedbackVector, codeLen)
}
