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

// DenseMatrix feedback encoding for TableKeyFeedback.DenseMatrix.
const (
	FBDenseMatrixUnobserved  uint8 = 0
	FBDenseMatrixNo          uint8 = 1
	FBDenseMatrixYes         uint8 = 2
	FBDenseMatrixPolymorphic uint8 = 0xFF
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

// TableAccessFeedback flags. These facts are monotonic: once a site observes a
// conflicting shape/key/mutation form, it stays marked polymorphic.
const (
	TableAccessKeyPolymorphic uint16 = 1 << iota
	TableAccessShapePolymorphic
	TableAccessFieldPolymorphic
	TableAccessArrayKindPolymorphic
	TableAccessAppendSeen
	TableAccessOverwriteSeen
	TableAccessSparseSeen
	TableAccessMetatableSeen
)

const (
	TableAccessKindGet uint8 = 1
	TableAccessKindSet uint8 = 2
)

// TableKeyFeedback records non-type table-access facts for one bytecode PC.
// It intentionally lives outside TypeFeedback so the 4-byte type/kind feedback
// stays compact for the hot arithmetic and guard-specialization path. The
// historical int-key fields remain for table preallocation; newer fields are a
// general profile substrate for guarded table specialization.
type TableKeyFeedback struct {
	MaxIntKey uint32
	HasIntKey bool

	Count      uint32
	ShapeID    uint32
	FieldIdx   int
	Flags      uint16
	KeyType    FeedbackType
	ValueType  FeedbackType
	ArrayKind  uint8
	AccessKind uint8

	StringKey     string
	StringKeySeen bool
	FieldIdxSeen  bool
	DenseMatrix   uint8
}

const (
	FieldAccessShapePolymorphic uint8 = 1 << iota
	FieldAccessIndexPolymorphic
	FieldAccessInvalidSeen
)

// FieldAccessFeedback records stable table field facts observed at one
// GETFIELD/SETFIELD site. FieldCacheEntry remains the executable IC; this is the
// monotonic profile view used by guarded specialization.
type FieldAccessFeedback struct {
	Count      uint32
	ShapeID    uint32
	FieldIdx   int
	Flags      uint8
	ValueType  FeedbackType
	AccessKind uint8
}

const MaxCallSiteFeedbackArgs = 4

const (
	CallSiteCalleePolymorphic uint8 = 1 << iota
	CallSiteArityPolymorphic
)

// CallSiteFeedback records stable facts observed at one OP_CALL site. It is a
// low-level profile substrate: optimization passes can combine these facts into
// guards, while unstable sites naturally deopt/fallback or remain generic.
type CallSiteFeedback struct {
	Count            uint32
	NArgs            uint8
	ResultArity      uint8
	Flags            uint8
	CalleeType       FeedbackType
	CalleeNativeKind uint8
	CalleeNativeData uintptr
	CalleeVMProto    *FuncProto
	ArgTypes         [MaxCallSiteFeedbackArgs]FeedbackType
	StringArgMask    uint8
	StringArgPoly    uint8
	StringArgs       [MaxCallSiteFeedbackArgs]string
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

// ObserveTableAccess records a generic GETTABLE/SETTABLE observation after the
// operation has executed. The caller may pass beforeLen/beforeFieldIdx for
// SETTABLE classification; use -1 when unknown or for GETTABLE.
func (tk *TableKeyFeedback) ObserveTableAccess(tbl *runtime.Table, key, value runtime.Value, accessKind uint8, beforeLen, beforeFieldIdx int) {
	if tk == nil {
		return
	}
	tk.Count++
	tk.AccessKind = mergeTableAccessKind(tk.AccessKind, accessKind)
	tk.KeyType.Observe(key.Type())
	tk.ValueType.Observe(value.Type())
	tk.ObserveIntKey(key)
	tk.observeArrayKind(tbl)
	tk.ObserveDenseMatrix(tbl)
	if tbl == nil {
		return
	}
	if tbl.HasMetatable() {
		tk.Flags |= TableAccessMetatableSeen
	}
	tk.observeShape(tbl.ShapeID())
	if key.IsString() {
		keyStr := key.Str()
		tk.observeStringKey(keyStr)
		fieldIdx := tbl.FieldIndex(keyStr)
		tk.observeFieldIdx(fieldIdx)
		if accessKind == TableAccessKindSet {
			tk.observeStringMutation(beforeFieldIdx, fieldIdx, value)
		}
		return
	}
	if accessKind == TableAccessKindSet && key.IsInt() {
		tk.observeIntMutation(key.Int(), beforeLen, value)
	}
}

func (tk *TableKeyFeedback) StableStringShapeField() (key string, shapeID uint32, fieldIdx int, ok bool) {
	if tk.Count == 0 || tk.Flags&(TableAccessKeyPolymorphic|TableAccessShapePolymorphic|TableAccessFieldPolymorphic|TableAccessMetatableSeen) != 0 {
		return "", 0, 0, false
	}
	if !tk.StringKeySeen || tk.ShapeID == 0 || tk.FieldIdx < 0 {
		return "", 0, 0, false
	}
	return tk.StringKey, tk.ShapeID, tk.FieldIdx, true
}

func (tk *TableKeyFeedback) observeArrayKind(tbl *runtime.Table) {
	if tbl == nil {
		return
	}
	encoded := uint8(tbl.GetArrayKind()) + 1
	cur := tk.ArrayKind
	if cur == FBKindPolymorphic {
		return
	}
	if cur == FBKindUnobserved {
		tk.ArrayKind = encoded
		return
	}
	if cur != encoded {
		tk.ArrayKind = FBKindPolymorphic
		tk.Flags |= TableAccessArrayKindPolymorphic
	}
}

func (tk *TableKeyFeedback) observeShape(shapeID uint32) {
	if shapeID == 0 {
		return
	}
	if tk.ShapeID == 0 {
		tk.ShapeID = shapeID
		return
	}
	if tk.ShapeID != shapeID {
		tk.Flags |= TableAccessShapePolymorphic
	}
}

func (tk *TableKeyFeedback) observeFieldIdx(fieldIdx int) {
	if fieldIdx < 0 {
		return
	}
	if !tk.FieldIdxSeen {
		tk.FieldIdx = fieldIdx
		tk.FieldIdxSeen = true
		return
	}
	if tk.FieldIdx != fieldIdx {
		tk.Flags |= TableAccessFieldPolymorphic
	}
}

func (tk *TableKeyFeedback) observeStringKey(key string) {
	if !tk.StringKeySeen {
		tk.StringKey = key
		tk.StringKeySeen = true
		return
	}
	if tk.StringKey != key {
		tk.Flags |= TableAccessKeyPolymorphic
	}
}

func (tk *TableKeyFeedback) observeIntMutation(key int64, beforeLen int, value runtime.Value) {
	if value.IsNil() || beforeLen < 0 {
		return
	}
	switch {
	case key == int64(beforeLen+1):
		tk.Flags |= TableAccessAppendSeen
	case key >= 0 && key <= int64(beforeLen):
		tk.Flags |= TableAccessOverwriteSeen
	case key > int64(beforeLen+1):
		tk.Flags |= TableAccessSparseSeen
	}
}

func (tk *TableKeyFeedback) observeStringMutation(beforeFieldIdx, afterFieldIdx int, value runtime.Value) {
	if value.IsNil() {
		return
	}
	if beforeFieldIdx >= 0 && afterFieldIdx == beforeFieldIdx {
		tk.Flags |= TableAccessOverwriteSeen
	} else if beforeFieldIdx < 0 && afterFieldIdx >= 0 {
		tk.Flags |= TableAccessAppendSeen
	}
}

func mergeTableAccessKind(cur, next uint8) uint8 {
	if cur == 0 || cur == next {
		return next
	}
	return cur | next
}

// ObserveDenseMatrix records whether a table access receiver is a DenseMatrix.
// It stays monomorphic only while every observed receiver agrees.
func (tk *TableKeyFeedback) ObserveDenseMatrix(tbl *runtime.Table) {
	observed := FBDenseMatrixNo
	if tbl != nil && tbl.DMStride() > 0 {
		observed = FBDenseMatrixYes
	}
	cur := tk.DenseMatrix
	if cur == FBDenseMatrixPolymorphic {
		return
	}
	if cur == FBDenseMatrixUnobserved {
		tk.DenseMatrix = observed
		return
	}
	if cur != observed {
		tk.DenseMatrix = FBDenseMatrixPolymorphic
	}
}

// ObserveFieldCache records the current monomorphic field-cache fact for a
// table field access. Zero shape or negative index means the site did not
// resolve to a shaped small-string field and should not specialize.
func (ff *FieldAccessFeedback) ObserveFieldCache(cache runtime.FieldCacheEntry, value runtime.Value, accessKind uint8) {
	if ff == nil {
		return
	}
	if cache.ShapeID == 0 || cache.FieldIdx < 0 || value.IsNil() {
		ff.Count++
		ff.Flags |= FieldAccessInvalidSeen
		return
	}
	if ff.Count == 0 {
		ff.ShapeID = cache.ShapeID
		ff.FieldIdx = cache.FieldIdx
		ff.AccessKind = accessKind
	} else {
		if ff.ShapeID != cache.ShapeID {
			ff.Flags |= FieldAccessShapePolymorphic
		}
		if ff.FieldIdx != cache.FieldIdx {
			ff.Flags |= FieldAccessIndexPolymorphic
		}
	}
	ff.Count++
	ff.ValueType.Observe(value.Type())
}

func (ff FieldAccessFeedback) StableShapeField() (shapeID uint32, fieldIdx int, ok bool) {
	if ff.Count == 0 || ff.Flags&(FieldAccessShapePolymorphic|FieldAccessIndexPolymorphic|FieldAccessInvalidSeen) != 0 {
		return 0, 0, false
	}
	if ff.ShapeID == 0 || ff.FieldIdx < 0 {
		return 0, 0, false
	}
	return ff.ShapeID, ff.FieldIdx, true
}

// ObserveCall records a callsite observation. It is monotonic: once the callee
// identity or arity differs, the corresponding polymorphic bit stays set.
func (cf *CallSiteFeedback) ObserveCall(fn runtime.Value, args []runtime.Value, nArgs, resultArity int) {
	if cf == nil {
		return
	}
	if cf.Count == 0 {
		cf.NArgs = clampCallFeedbackUint8(nArgs)
		cf.ResultArity = clampCallFeedbackUint8(resultArity)
	} else {
		if cf.NArgs != clampCallFeedbackUint8(nArgs) || cf.ResultArity != clampCallFeedbackUint8(resultArity) {
			cf.Flags |= CallSiteArityPolymorphic
		}
	}
	cf.Count++
	cf.CalleeType.Observe(fn.Type())
	nativeKind, nativeData := callFeedbackNativeIdentity(fn)
	vmProto := callFeedbackVMProto(fn)
	if cf.Count == 1 {
		cf.CalleeNativeKind = nativeKind
		cf.CalleeNativeData = nativeData
		cf.CalleeVMProto = vmProto
	} else if cf.CalleeNativeKind != nativeKind || cf.CalleeNativeData != nativeData || cf.CalleeVMProto != vmProto {
		cf.Flags |= CallSiteCalleePolymorphic
	}
	limit := nArgs
	if limit > len(args) {
		limit = len(args)
	}
	if limit > MaxCallSiteFeedbackArgs {
		limit = MaxCallSiteFeedbackArgs
	}
	for i := 0; i < limit; i++ {
		arg := args[i]
		cf.ArgTypes[i].Observe(arg.Type())
		if !arg.IsString() {
			continue
		}
		bit := uint8(1 << i)
		s := arg.Str()
		if cf.StringArgMask&bit == 0 {
			cf.StringArgMask |= bit
			cf.StringArgs[i] = s
		} else if cf.StringArgs[i] != s {
			cf.StringArgPoly |= bit
		}
	}
}

func (cf CallSiteFeedback) StableCalleeNativeIdentity() (kind uint8, data uintptr, ok bool) {
	if cf.Count == 0 || cf.Flags&CallSiteCalleePolymorphic != 0 {
		return 0, 0, false
	}
	if cf.CalleeNativeKind == 0 && cf.CalleeNativeData == 0 {
		return 0, 0, false
	}
	return cf.CalleeNativeKind, cf.CalleeNativeData, true
}

func (cf CallSiteFeedback) StableCalleeVMProto() (*FuncProto, bool) {
	if cf.Count == 0 || cf.Flags&CallSiteCalleePolymorphic != 0 || cf.CalleeVMProto == nil {
		return nil, false
	}
	return cf.CalleeVMProto, true
}

func (cf CallSiteFeedback) StableStringArg(idx int) (string, bool) {
	if idx < 0 || idx >= MaxCallSiteFeedbackArgs {
		return "", false
	}
	bit := uint8(1 << idx)
	if cf.StringArgMask&bit == 0 || cf.StringArgPoly&bit != 0 {
		return "", false
	}
	return cf.StringArgs[idx], true
}

func callFeedbackNativeIdentity(fn runtime.Value) (uint8, uintptr) {
	gf := fn.GoFunction()
	if gf == nil {
		return 0, 0
	}
	return gf.NativeKind, uintptr(gf.NativeData)
}

func callFeedbackVMProto(fn runtime.Value) *FuncProto {
	cl, ok := closureFromValue(fn)
	if !ok || cl == nil {
		return nil
	}
	return cl.Proto
}

func clampCallFeedbackUint8(n int) uint8 {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return uint8(n)
}

// FeedbackVector is per-function type feedback, indexed by bytecode PC.
type FeedbackVector []TypeFeedback

// TableKeyFeedbackVector is per-function table key feedback, indexed by PC.
type TableKeyFeedbackVector []TableKeyFeedback

// FieldAccessFeedbackVector is per-function field access feedback, indexed by PC.
type FieldAccessFeedbackVector []FieldAccessFeedback

// CallSiteFeedbackVector is per-function call feedback, indexed by PC.
type CallSiteFeedbackVector []CallSiteFeedback

// NewFeedbackVector creates a zero-initialized feedback vector for a function.
// All entries start as FBUnobserved.
func NewFeedbackVector(codeLen int) FeedbackVector {
	return make(FeedbackVector, codeLen)
}

// NewTableKeyFeedbackVector creates a zero-initialized table key feedback vector.
func NewTableKeyFeedbackVector(codeLen int) TableKeyFeedbackVector {
	return make(TableKeyFeedbackVector, codeLen)
}

// NewFieldAccessFeedbackVector creates a zero-initialized field access feedback vector.
func NewFieldAccessFeedbackVector(codeLen int) FieldAccessFeedbackVector {
	return make(FieldAccessFeedbackVector, codeLen)
}

// NewCallSiteFeedbackVector creates a zero-initialized callsite feedback vector.
func NewCallSiteFeedbackVector(codeLen int) CallSiteFeedbackVector {
	return make(CallSiteFeedbackVector, codeLen)
}
