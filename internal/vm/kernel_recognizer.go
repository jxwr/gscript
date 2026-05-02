package vm

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
)

// KernelRoute identifies how a structural kernel is reached. Whole-call
// routes are probed at OP_CALL; driver-loop routes are probed at OP_FORPREP.
type KernelRoute string

const (
	KernelRouteWholeCallValue    KernelRoute = "whole_call_value"
	KernelRouteWholeCallNoResult KernelRoute = "whole_call_no_result"
	KernelRouteDriverLoop        KernelRoute = "driver_loop"
)

// KernelInfo is stable diagnostic metadata for structural VM kernels.
//
// Recognizers intentionally inspect only bytecode, constants, arity, and
// guarded callee shapes. FuncProto.Name and FuncProto.Source are debugging
// metadata and are not part of kernel admission.
type KernelInfo struct {
	Name    string
	Route   KernelRoute
	Arity   int
	Results int
}

// KernelDiagnostic reports whether one registered structural kernel recognizes
// a prototype and, if not, the broad fallback reason.
type KernelDiagnostic struct {
	Kernel     KernelInfo
	Recognized bool
	Reason     string
}

const (
	kernelReasonRecognized             = "recognized_structural_bytecode"
	kernelReasonNilProto               = "nil_proto"
	kernelReasonShapeMismatch          = "bytecode_or_constant_shape_mismatch"
	kernelReasonDriverRecognized       = "recognized_structural_driver_loop"
	kernelReasonDriverMismatch         = "bytecode_or_callee_shape_mismatch"
	kernelReasonMissingGlobalProtoMap  = "missing_global_proto_map"
	kernelUnknownDriverLoopArity       = -1
	kernelUnknownDriverLoopResultCount = -1
	kernelWholeCallInPlaceResultCount  = 0
	kernelWholeCallSingleResultCount   = 1
)

const (
	wholeCallKernelFannkuchRedux = iota
	wholeCallKernelSieveCount
	wholeCallKernelRecursiveTableBuilder
	wholeCallKernelRecursiveTableFold
	wholeCallKernelNestedMatmul
	wholeCallKernelIntArrayPartitionSort
	wholeCallKernelSpectralMultiplyAv
	wholeCallKernelSpectralMultiplyAtv
	wholeCallKernelSpectralAtAv
	wholeCallKernelNBodyAdvance
	wholeCallKernelMixedInventoryOrders
	wholeCallKernelTableArrayIntSum
	wholeCallKernelTableArrayFloatDot
	wholeCallKernelTableArraySwap
	wholeCallKernelTableArray2D
	wholeCallKernelCount
)

type wholeCallKernelFingerprint struct {
	numParams    int
	isVarArg     bool
	maxStack     int
	codeLen      int
	constLen     int
	protoLen     int
	tableCtorLen int
	hash         uint64
}

type wholeCallKernelProtoCache struct {
	fingerprint wholeCallKernelFingerprint
	recognized  uint64
}

type wholeCallValueKernelRunner func(*VM, *Closure, []runtime.Value) (bool, []runtime.Value, error)
type wholeCallNoResultKernelRunner func(*VM, *Closure, []runtime.Value) (bool, error)

type wholeCallKernelRecognizer struct {
	info           KernelInfo
	recognize      func(*FuncProto) bool
	runValue       wholeCallValueKernelRunner
	runNoResult    wholeCallNoResultKernelRunner
	recursiveTable bool
}

var wholeCallKernelRegistry = [wholeCallKernelCount]wholeCallKernelRecognizer{
	{
		info: KernelInfo{
			Name:    "fannkuch_redux",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isFannkuchReduxKernelProto,
		runValue:  (*VM).runFannkuchReduxWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "sieve_count",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isSieveProto,
		runValue:  (*VM).runSieveWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "recursive_table_builder",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize:      IsFixedRecursiveTableBuilderKernelProto,
		runValue:       (*VM).tryRunRecursiveTableValueKernel,
		recursiveTable: true,
	},
	{
		info: KernelInfo{
			Name:    "recursive_table_fold",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize:      IsFixedRecursiveTableFoldKernelProto,
		runValue:       (*VM).tryRunRecursiveTableValueKernel,
		recursiveTable: true,
	},
	{
		info: KernelInfo{
			Name:    "nested_matmul",
			Route:   KernelRouteWholeCallValue,
			Arity:   3,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isNestedMatmulProto,
		runValue:  (*VM).runMatmulWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "int_array_partition_sort",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   3,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize:   isIntArrayPartitionSortProto,
		runNoResult: (*VM).runIntSortWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "spectral_multiply_av",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   3,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize: func(p *FuncProto) bool {
			return classifySpectralMultiplyProto(p) == spectralAv
		},
		runNoResult: (*VM).runSpectralWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "spectral_multiply_atv",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   3,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize: func(p *FuncProto) bool {
			return classifySpectralMultiplyProto(p) == spectralAtv
		},
		runNoResult: (*VM).runSpectralWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "spectral_atav",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   3,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize:   isSpectralAtAvProto,
		runNoResult: (*VM).runSpectralWholeCallKernel,
	},
	{
		info: KernelInfo{
			Name:    "nbody_advance",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   1,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize:   isNBodyAdvanceProto,
		runNoResult: (*VM).runNBodyAdvanceKernel,
	},
	{
		info: KernelInfo{
			Name:    "mixed_inventory_orders",
			Route:   KernelRouteWholeCallValue,
			Arity:   3,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isMixedInventoryOrdersProto,
		runValue:  (*VM).runMixedInventoryOrdersKernel,
	},
	{
		info: KernelInfo{
			Name:    "table_array_int_sum",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isTableArrayIntSumProto,
		runValue:  (*VM).runTableArrayIntSumKernel,
	},
	{
		info: KernelInfo{
			Name:    "table_array_float_dot",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isTableArrayFloatDotProto,
		runValue:  (*VM).runTableArrayFloatDotKernel,
	},
	{
		info: KernelInfo{
			Name:    "table_array_swap",
			Route:   KernelRouteWholeCallValue,
			Arity:   2,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isTableArraySwapProto,
		runValue:  (*VM).runTableArraySwapKernel,
	},
	{
		info: KernelInfo{
			Name:    "table_array_2d",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: isTableArray2DProto,
		runValue:  (*VM).runTableArray2DKernel,
	},
}

type driverLoopKernelRecognizer struct {
	info      KernelInfo
	recognize func(*FuncProto, map[string]*FuncProto) bool
}

var driverLoopKernelRegistry = [...]driverLoopKernelRecognizer{
	{
		info: KernelInfo{
			Name:    "nbody_advance_loop",
			Route:   KernelRouteDriverLoop,
			Arity:   kernelUnknownDriverLoopArity,
			Results: kernelUnknownDriverLoopResultCount,
		},
		recognize: HasNBodyAdvanceDriverLoopKernel,
	},
	{
		info: KernelInfo{
			Name:    "prime_predicate_sum_loop",
			Route:   KernelRouteDriverLoop,
			Arity:   kernelUnknownDriverLoopArity,
			Results: kernelUnknownDriverLoopResultCount,
		},
		recognize: HasPrimePredicateSumLoopKernel,
	},
}

// WholeCallKernelCatalog returns diagnostic metadata for OP_CALL structural
// kernels without probing any particular prototype.
func WholeCallKernelCatalog() []KernelInfo {
	out := make([]KernelInfo, len(wholeCallKernelRegistry))
	for i, entry := range wholeCallKernelRegistry {
		out[i] = entry.info
	}
	return out
}

// DriverLoopKernelCatalog returns diagnostic metadata for OP_FORPREP driver
// loop kernels without probing any particular prototype.
func DriverLoopKernelCatalog() []KernelInfo {
	out := make([]KernelInfo, len(driverLoopKernelRegistry))
	for i, entry := range driverLoopKernelRegistry {
		out[i] = entry.info
	}
	return out
}

// RecognizedWholeCallKernels returns every registered whole-call kernel whose
// structural recognizer accepts p. It does not inspect FuncProto.Name or Source.
func RecognizedWholeCallKernels(p *FuncProto) []KernelInfo {
	out := make([]KernelInfo, 0, 1)
	recognized := recognizedWholeCallKernelBits(p)
	for i, entry := range wholeCallKernelRegistry {
		if recognized&(uint64(1)<<uint(i)) != 0 {
			out = append(out, entry.info)
		}
	}
	return out
}

// DiagnoseWholeCallKernelProto reports structural recognizer results for every
// registered whole-call kernel. It is intended for tests and diagnostics, not
// hot dispatch.
func DiagnoseWholeCallKernelProto(p *FuncProto) []KernelDiagnostic {
	out := make([]KernelDiagnostic, len(wholeCallKernelRegistry))
	recognizedBits := recognizedWholeCallKernelBits(p)
	for i, entry := range wholeCallKernelRegistry {
		recognized := recognizedBits&(uint64(1)<<uint(i)) != 0
		out[i] = KernelDiagnostic{
			Kernel:     entry.info,
			Recognized: recognized,
			Reason:     wholeCallKernelReason(p, recognized),
		}
	}
	return out
}

func recognizedWholeCallKernelBits(proto *FuncProto) uint64 {
	if proto == nil {
		return 0
	}
	return wholeCallKernelCacheForProto(proto).recognized
}

// cachedWholeCallKernelBits is for OP_CALL hot dispatch. FuncProto bytecode is
// immutable after compilation, while diagnostics use recognizedWholeCallKernelBits
// to keep mutation-oriented tests exact.
func cachedWholeCallKernelBits(proto *FuncProto) uint64 {
	if proto == nil {
		return 0
	}
	if cache := proto.WholeCallKernel; cache != nil {
		return cache.recognized
	}
	return wholeCallKernelCacheForProto(proto).recognized
}

func cachedWholeCallKernelRecognized(proto *FuncProto, id int) bool {
	if id < 0 || id >= len(wholeCallKernelRegistry) {
		return false
	}
	return cachedWholeCallKernelBits(proto)&(uint64(1)<<uint(id)) != 0
}

func hotWholeCallKernelRecognized(proto *FuncProto, id int) bool {
	if id < 0 || id >= len(wholeCallKernelRegistry) {
		return false
	}
	return cachedWholeCallKernelBits(proto)&(uint64(1)<<uint(id)) != 0
}

func wholeCallKernelCacheForProto(proto *FuncProto) *wholeCallKernelProtoCache {
	fp := wholeCallKernelFingerprintForProto(proto)
	cache := proto.WholeCallKernel
	if cache != nil && cache.fingerprint == fp {
		return cache
	}
	cache = &wholeCallKernelProtoCache{fingerprint: fp}
	for i, entry := range wholeCallKernelRegistry {
		if entry.recognize(proto) {
			cache.recognized |= uint64(1) << uint(i)
		}
	}
	proto.WholeCallKernel = cache
	return cache
}

func wholeCallKernelFingerprintForProto(proto *FuncProto) wholeCallKernelFingerprint {
	var fp wholeCallKernelFingerprint
	if proto == nil {
		return fp
	}
	fp.numParams = proto.NumParams
	fp.isVarArg = proto.IsVarArg
	fp.maxStack = proto.MaxStack
	fp.codeLen = len(proto.Code)
	fp.constLen = len(proto.Constants)
	fp.protoLen = len(proto.Protos)
	fp.tableCtorLen = len(proto.TableCtors2)

	h := uint64(1469598103934665603)
	h = fnvMixUint64(h, uint64(fp.numParams))
	if fp.isVarArg {
		h = fnvMixUint64(h, 1)
	}
	h = fnvMixUint64(h, uint64(fp.maxStack))
	for _, inst := range proto.Code {
		h = fnvMixUint64(h, uint64(inst))
	}
	for _, c := range proto.Constants {
		h = fnvMixRuntimeValue(h, c)
	}
	for _, ctor := range proto.TableCtors2 {
		h = fnvMixInt(h, ctor.Key1Const)
		h = fnvMixInt(h, ctor.Key2Const)
		h = fnvMixString(h, ctor.Runtime.Key1)
		h = fnvMixString(h, ctor.Runtime.Key2)
	}
	fp.hash = h
	return fp
}

func fnvMixRuntimeValue(h uint64, v runtime.Value) uint64 {
	h = fnvMixUint64(h, uint64(v.Type()))
	switch {
	case v.IsString():
		return fnvMixString(h, v.Str())
	case v.IsInt():
		return fnvMixUint64(h, uint64(v.Int()))
	case v.IsFloat():
		return fnvMixUint64(h, math.Float64bits(v.Float()))
	case v.IsBool():
		if v.Bool() {
			return fnvMixUint64(h, 1)
		}
		return fnvMixUint64(h, 0)
	default:
		return h
	}
}

func fnvMixInt(h uint64, v int) uint64 {
	return fnvMixUint64(h, uint64(int64(v)))
}

func fnvMixString(h uint64, s string) uint64 {
	h = fnvMixUint64(h, uint64(len(s)))
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func fnvMixUint64(h uint64, v uint64) uint64 {
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(v))
		h *= 1099511628211
		v >>= 8
	}
	return h
}

// RecognizedDriverLoopKernels returns every registered driver-loop kernel whose
// structural recognizer accepts proto with the supplied global callee map.
func RecognizedDriverLoopKernels(proto *FuncProto, globals map[string]*FuncProto) []KernelInfo {
	out := make([]KernelInfo, 0, 1)
	for _, entry := range driverLoopKernelRegistry {
		if entry.recognize(proto, globals) {
			out = append(out, entry.info)
		}
	}
	return out
}

// DiagnoseDriverLoopKernels reports structural driver-loop recognizer results.
// The globals map should contain compile-time global function protos by name.
func DiagnoseDriverLoopKernels(proto *FuncProto, globals map[string]*FuncProto) []KernelDiagnostic {
	out := make([]KernelDiagnostic, len(driverLoopKernelRegistry))
	for i, entry := range driverLoopKernelRegistry {
		recognized := proto != nil && entry.recognize(proto, globals)
		out[i] = KernelDiagnostic{
			Kernel:     entry.info,
			Recognized: recognized,
			Reason:     driverLoopKernelReason(proto, globals, recognized),
		}
	}
	return out
}

func wholeCallKernelReason(proto *FuncProto, recognized bool) string {
	if proto == nil {
		return kernelReasonNilProto
	}
	if recognized {
		return kernelReasonRecognized
	}
	return kernelReasonShapeMismatch
}

func driverLoopKernelReason(proto *FuncProto, globals map[string]*FuncProto, recognized bool) string {
	if proto == nil {
		return kernelReasonNilProto
	}
	if recognized {
		return kernelReasonDriverRecognized
	}
	if len(globals) == 0 {
		return kernelReasonMissingGlobalProtoMap
	}
	return kernelReasonDriverMismatch
}

func codeEquals(got, want []uint32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func numberConst(v runtime.Value, want float64) bool {
	return v.IsNumber() && v.Number() == want
}

func stringConst(constants []runtime.Value, idx int) bool {
	return idx >= 0 && idx < len(constants) && constants[idx].IsString()
}

func addOperandsMatch(inst uint32, left int, right int) bool {
	b := DecodeB(inst)
	c := DecodeC(inst)
	return (b == left && c == right) || (b == right && c == left)
}
