package vm

import "github.com/gscript/gscript/internal/runtime"

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

type wholeCallKernelRecognizer struct {
	info      KernelInfo
	recognize func(*FuncProto) bool
}

var wholeCallKernelRegistry = [...]wholeCallKernelRecognizer{
	{
		info: KernelInfo{
			Name:    "fannkuch_redux",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: IsFannkuchReduxKernelProto,
	},
	{
		info: KernelInfo{
			Name:    "sieve_count",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: IsSieveKernelProto,
	},
	{
		info: KernelInfo{
			Name:    "recursive_table_builder",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: IsFixedRecursiveTableBuilderKernelProto,
	},
	{
		info: KernelInfo{
			Name:    "recursive_table_fold",
			Route:   KernelRouteWholeCallValue,
			Arity:   1,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: IsFixedRecursiveTableFoldKernelProto,
	},
	{
		info: KernelInfo{
			Name:    "nested_matmul",
			Route:   KernelRouteWholeCallValue,
			Arity:   3,
			Results: kernelWholeCallSingleResultCount,
		},
		recognize: IsNestedMatmulKernelProto,
	},
	{
		info: KernelInfo{
			Name:    "int_array_partition_sort",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   3,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize: isIntArrayPartitionSortProto,
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
	},
	{
		info: KernelInfo{
			Name:    "spectral_atav",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   3,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize: isSpectralAtAvProto,
	},
	{
		info: KernelInfo{
			Name:    "nbody_advance",
			Route:   KernelRouteWholeCallNoResult,
			Arity:   1,
			Results: kernelWholeCallInPlaceResultCount,
		},
		recognize: HasNBodyAdvanceWholeCallKernel,
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
	for _, entry := range wholeCallKernelRegistry {
		if entry.recognize(p) {
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
	for i, entry := range wholeCallKernelRegistry {
		recognized := p != nil && entry.recognize(p)
		out[i] = KernelDiagnostic{
			Kernel:     entry.info,
			Recognized: recognized,
			Reason:     wholeCallKernelReason(p, recognized),
		}
	}
	return out
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
