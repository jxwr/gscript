//go:build darwin && arm64

// emit_compile.go contains the Tier 2 compile pipeline for the Method JIT.
// It takes a Function with register allocation and produces executable ARM64 code.
// Includes the emitContext struct, slot assignment, prologue/epilogue generation,
// and block emission.

package methodjit

import (
	"fmt"
	"sort"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Suppress unused import warnings.
var _ runtime.Value
var _ *vm.FuncProto

// Compile takes a Function with register allocation and produces executable ARM64 code.
func Compile(fn *Function, alloc *RegAllocation) (*CompiledFunction, error) {
	if err := validateCompileInputs(fn, alloc); err != nil {
		return nil, err
	}

	// Check if any FPR allocations exist (to skip FPR save/restore).
	hasFPR := false
	for _, pr := range alloc.ValueRegs {
		if pr.IsFloat {
			hasFPR = true
			break
		}
	}

	li := computeLoopInfo(fn)
	crossBlockLive := computeCrossBlockLive(fn)
	blockLiveIn, blockLiveOut := computeBlockLiveness(fn)
	instrLiveAfter := computeInstrLiveAfter(fn, blockLiveOut)
	rawIntBlockCarry := enableSinglePredRawIntCarry(fn)
	rawIntCarryNoStore := map[int]bool(nil)
	if rawIntBlockCarry {
		rawIntCarryNoStore = computeSinglePredRawIntStoreElision(fn, alloc, blockLiveIn)
	}
	defs := make(map[int]*Instr)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defs[instr.ID] = instr
			}
		}
	}
	var headerRegs map[int]map[int]loopRegEntry
	var headerFPRegs map[int]map[int]loopFPRegEntry
	var safeHdrRegs map[int]map[int]loopRegEntry
	var safeHdrFPRegs map[int]map[int]loopFPRegEntry
	var loopInvariantGPRs map[int]map[int]loopRegEntry
	var loopInvariantFPRs map[int]map[int]loopFPRegEntry
	var phiOnlyArgs loopPhiArgSet
	var fpPhiOnlyArgs loopPhiArgSet
	exitBoxPhis := make(map[int]bool)
	exitBoxFPPhis := make(map[int]bool)
	exitStorePhis := make(map[int]bool)
	if li.hasLoops() {
		headerRegs = li.computeHeaderExitRegs(fn, alloc)
		headerFPRegs = li.computeHeaderExitFPRegs(fn, alloc)
		// Compute safe header regs: only registers NOT clobbered by any
		// non-header block in the loop body. These are used for both
		// block activation and loopPhiOnlyArgs checking.
		safeHdrRegs = computeSafeHeaderRegs(fn, li, alloc, headerRegs)
		safeHdrFPRegs = computeSafeHeaderFPRegs(fn, li, alloc, headerFPRegs)
		loopInvariantGPRs = computeSafeLoopInvariantGPRs(fn, li, alloc)
		loopInvariantFPRs = computeSafeLoopInvariantFPRs(fn, li, alloc)
		phiOnlyArgs = computeLoopPhiArgs(fn, li, alloc, safeHdrRegs)
		fpPhiOnlyArgs = computeLoopFPPhiArgs(fn, li, alloc, safeHdrFPRegs)

		// Identify loop header phis that need exit-time boxing:
		// cross-block live AND register survives through the ENTIRE loop body
		// (not just the header). If any non-header block in the loop has an
		// instruction allocated to the same GPR, the phi's register will be
		// clobbered, so we must write-through on every iteration.
		for headerID, phiIDs := range li.loopPhis {
			hdrRegs := headerRegs[headerID]
			bodyBlocks := li.headerBlocks[headerID]
			for _, phiID := range phiIDs {
				if !crossBlockLive[phiID] {
					continue
				}
				pr, ok := alloc.ValueRegs[phiID]
				if !ok || pr.IsFloat {
					continue
				}
				// Check if this phi's register still holds this phi at
				// end of its own header.
				entry, inRegs := hdrRegs[pr.Reg]
				if !inRegs || entry.ValueID != phiID || !entry.IsRawInt {
					continue
				}
				// Check that no non-header block in the loop body clobbers
				// this register. If clobbered, the phi value can't survive
				// in-register and must be written to memory.
				//
				// A "clobber" is any instruction whose allocated register
				// equals this phi's register. Nested loop header phis
				// count: their phi moves write the register at inner-loop
				// entry, overwriting the outer header's phi value.
				clobbered := false
				for _, block := range fn.Blocks {
					if block.ID == headerID || !bodyBlocks[block.ID] {
						continue
					}
					for _, instr := range block.Instrs {
						if instr.Op.IsTerminator() {
							continue
						}
						instrPR, ok := alloc.ValueRegs[instr.ID]
						if !ok || instrPR.IsFloat || instrPR.Reg != pr.Reg {
							continue
						}
						clobbered = true
						break
					}
					if clobbered {
						break
					}
				}
				if !clobbered {
					exitBoxPhis[phiID] = true
				}
			}
		}

		for headerID, phiIDs := range li.loopPhis {
			hdrRegs := headerRegs[headerID]
			bodyBlocks := li.headerBlocks[headerID]
			if loopBodyHasDirectDeopt(fn, bodyBlocks) {
				continue
			}
			for _, phiID := range phiIDs {
				if !crossBlockLive[phiID] {
					continue
				}
				phi := defs[phiID]
				if phi == nil || phi.Type == TypeInt {
					continue
				}
				pr, ok := alloc.ValueRegs[phiID]
				if !ok || pr.IsFloat {
					continue
				}
				entry, inRegs := hdrRegs[pr.Reg]
				if !inRegs || entry.ValueID != phiID || entry.IsRawInt {
					continue
				}
				clobbered := false
				for _, block := range fn.Blocks {
					if block.ID == headerID || !bodyBlocks[block.ID] {
						continue
					}
					for _, instr := range block.Instrs {
						if instr.Op.IsTerminator() {
							continue
						}
						instrPR, ok := alloc.ValueRegs[instr.ID]
						if ok && !instrPR.IsFloat && instrPR.Reg == pr.Reg {
							clobbered = true
							break
						}
					}
					if clobbered {
						break
					}
				}
				if !clobbered {
					exitStorePhis[phiID] = true
				}
			}
		}

		for headerID, phiIDs := range li.loopPhis {
			hdrFPRegs := headerFPRegs[headerID]
			bodyBlocks := li.headerBlocks[headerID]
			for _, phiID := range phiIDs {
				if !crossBlockLive[phiID] {
					continue
				}
				pr, ok := alloc.ValueRegs[phiID]
				if !ok || !pr.IsFloat {
					continue
				}
				entry, inRegs := hdrFPRegs[pr.Reg]
				if !inRegs || entry.ValueID != phiID {
					continue
				}
				clobbered := false
				for _, block := range fn.Blocks {
					if block.ID == headerID || !bodyBlocks[block.ID] {
						continue
					}
					for _, instr := range block.Instrs {
						if instr.Op.IsTerminator() {
							continue
						}
						instrPR, ok := alloc.ValueRegs[instr.ID]
						if !ok || !instrPR.IsFloat || instrPR.Reg != pr.Reg {
							continue
						}
						clobbered = true
						break
					}
					if clobbered {
						break
					}
				}
				if !clobbered {
					exitBoxFPPhis[phiID] = true
				}
			}
		}
	}

	// Build constant int/bool maps for immediate optimization, and IR type map for
	// resolveRawFloat to detect int-typed values that need SCVTF conversion.
	constInts := make(map[int]int64)
	constBools := make(map[int]int64)
	irTypes := make(map[int]Type)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpConstInt {
				constInts[instr.ID] = instr.Aux
			}
			if instr.Op == OpConstBool {
				constBools[instr.ID] = instr.Aux
			}
			irTypes[instr.ID] = instr.Type
		}
	}

	// Identify single-use comparisons that can be fused with their
	// immediately-following Branch. Fused pairs emit CMP/FCMP + B.cc
	// instead of CMP + CSET + ORR + TBNZ (saves 3 instructions).
	fusedCmps := computeFusedComparisons(fn)
	nativeCallReplaySafe := tier2NativeCallReplaySafe(fn)
	nativeCallCalleeResumeSafe := tier2NativeCallCalleeResumeSafe(fn)
	rawIntSelfABI := AnalyzeRawIntSelfABI(fn.Proto)
	typedSelfABI := AnalyzeTypedSelfABI(fn.Proto)

	ec := &emitContext{
		fn:                         fn,
		alloc:                      alloc,
		asm:                        jit.NewAssembler(),
		slotMap:                    make(map[int]int),
		nextSlot:                   fn.NumRegs,
		activeRegs:                 make(map[int]bool),
		valueReprs:                 make(map[int]valueRepr),
		rawIntRegs:                 make(map[int]bool),
		rawTablePtrRegs:            make(map[int]bool),
		activeFPRegs:               make(map[int]bool),
		shapeVerified:              make(map[int]uint32),
		tableVerified:              make(map[int]bool),
		kindVerified:               make(map[int]uint16),
		keysDirtyWritten:           make(map[int]bool),
		localNewTablesNoMetatable:  functionHasNoTableMetatableMutationSurface(fn),
		tableArrayBoundedKeys:      make(map[tableArrayBoundKey]bool),
		dmVerified:                 make(map[int]bool),
		blockOutShapes:             make(map[int]map[int]uint32),
		blockOutTables:             make(map[int]map[int]bool),
		blockOutKinds:              make(map[int]map[int]uint16),
		blockOutKeysDirty:          make(map[int]map[int]bool),
		blockOutRawIntRegs:         make(map[int]map[int]loopRegEntry),
		blockLiveIn:                blockLiveIn,
		blockLiveOut:               blockLiveOut,
		instrLiveAfter:             instrLiveAfter,
		rawIntBlockCarry:           rawIntBlockCarry,
		rawIntCarryNoStore:         rawIntCarryNoStore,
		crossBlockLive:             crossBlockLive,
		globalCacheConsts:          make([]int, 0),
		useFPR:                     hasFPR,
		loop:                       li,
		loopHeaderRegs:             headerRegs,
		loopHeaderFPRegs:           headerFPRegs,
		safeHeaderRegs:             safeHdrRegs,
		safeHeaderFPRegs:           safeHdrFPRegs,
		loopInvariantGPRs:          loopInvariantGPRs,
		loopInvariantFPRs:          loopInvariantFPRs,
		loopPhiOnlyArgs:            phiOnlyArgs,
		loopFPPhiOnlyArgs:          fpPhiOnlyArgs,
		loopExitBoxPhis:            exitBoxPhis,
		loopExitBoxFPPhis:          exitBoxFPPhis,
		loopExitStorePhis:          exitStorePhis,
		constInts:                  constInts,
		constBools:                 constBools,
		irTypes:                    irTypes,
		scratchFPRCache:            make(map[int]jit.FReg),
		fusedCmps:                  fusedCmps,
		tailCallInstrs:             computeTailCalls(fn),
		newTableCaches:             newTableCacheSlotsForFunction(fn),
		instrCodeRanges:            make([]InstrCodeRange, 0, fn.nextID),
		nativeCallReplaySafe:       nativeCallReplaySafe,
		nativeCallCalleeResumeSafe: nativeCallCalleeResumeSafe,
		rawIntSelfABI:              rawIntSelfABI,
		typedSelfABI:               typedSelfABI,
		entryShapeGuards:           fn.FixedShapeEntryGuards,
	}
	if exitResumeCheckEnabled() {
		ec.exitResumeCheck = newExitResumeCheckMetadata()
	}
	// R124/R126: numeric entry is emitted as pass-2 body inside this
	// Compile when the proto qualifies. numericParamCount tells the
	// post-epilogue dispatcher whether to run pass 2.
	if rawIntSelfABI.Eligible {
		ec.numericParamCount = rawIntSelfABI.NumParams
		ec.numericParamSlots = make(map[int]bool, rawIntSelfABI.NumParams)
		for i := 0; i < rawIntSelfABI.NumParams; i++ {
			ec.numericParamSlots[i] = true
		}
	}

	// Assign home slots for all SSA values.
	ec.assignSlots()

	shiftAddVersion, hasShiftAddVersion := detectShiftAddOverflowVersion(fn)
	ec.skipStandardDirectEntry = hasShiftAddVersion

	// Emit prologue.
	ec.emitPrologue()

	if hasShiftAddVersion {
		ec.emitShiftAddOverflowVersion(shiftAddVersion)
	} else {
		// Emit each basic block.
		for _, block := range fn.Blocks {
			ec.emitBlock(block)
		}
	}

	// Emit epilogue.
	ec.emitEpilogue()
	if hasShiftAddVersion {
		ec.emitShiftAddOverflowVersionDirect(shiftAddVersion)
	}

	// R129: emit pass-2 (numeric variant) body BEFORE deferredResumes so
	// pass-2's deopts/call-exits append to the same deferredResumes
	// list. emitDeferredResumes then emits both passes' resume entries
	// with properly-disambiguated labels (numericPass flag on each).
	if !hasShiftAddVersion {
		ec.emitNumericBody()
	}

	// Emit deferred resume entry points (after epilogue so they're separate
	// function entry points with their own prologue).
	ec.emitDeferredResumes()

	// Finalize: resolve labels.
	code, err := ec.asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("methodjit: finalize error: %w", err)
	}

	// Allocate executable memory and write code.
	cb, err := jit.AllocExec(len(code) + 1024) // extra space for safety
	if err != nil {
		return nil, fmt.Errorf("methodjit: alloc exec error: %w", err)
	}
	if err := cb.WriteCode(code); err != nil {
		cb.Free()
		return nil, fmt.Errorf("methodjit: write code error: %w", err)
	}

	// Resolve pass-specific resume addresses for exit-resume points.
	resumeAddrs := make(map[int]int)
	numericResumeAddrs := make(map[int]int)
	for _, dr := range ec.deferredResumes {
		label := callExitResumeLabelForPass(dr.instrID, dr.numericPass)
		off := ec.asm.LabelOffset(label)
		if off < 0 {
			continue
		}
		if dr.numericPass {
			numericResumeAddrs[dr.instrID] = off
		} else {
			resumeAddrs[dr.instrID] = off
		}
	}

	// Resolve direct entry offset for BLR callers.
	directEntryOff := ec.asm.LabelOffset("t2_direct_entry")
	numericEntryOff := 0
	if ec.numericParamCount > 0 {
		label := fmt.Sprintf("t2_numeric_self_entry_%d", ec.numericParamCount)
		if off := ec.asm.LabelOffset(label); off >= 0 {
			numericEntryOff = off
		}
	}
	typedEntryOff := 0
	if typedSelfABI.Eligible {
		if off := ec.asm.LabelOffset("t2_typed_self_entry"); off >= 0 {
			typedEntryOff = off
		}
	}

	// Allocate per-GetGlobal value cache if any GetGlobal instructions exist.
	var globalCache []uint64
	if ec.nextGlobalCacheIndex > 0 {
		globalCache = make([]uint64, ec.nextGlobalCacheIndex)
	}
	nativeSetGlobals := collectNativeSetGlobals(fn)

	// R108/R151: allocate per-OpCall monomorphic IC cache (4 uint64 per site).
	var callCache []uint64
	if ec.nextCallCacheIndex > 0 {
		callCache = make([]uint64, 4*ec.nextCallCacheIndex)
	}

	return &CompiledFunction{
		Code:                 cb,
		Proto:                fn.Proto,
		NumSpills:            alloc.NumSpillSlots,
		numRegs:              ec.nextSlot,
		ResumeAddrs:          resumeAddrs,
		NumericResumeAddrs:   numericResumeAddrs,
		DirectEntryOffset:    directEntryOff,
		DirectEntrySafe:      nativeCallReplaySafe,
		Tier2DirectEntrySafe: nativeCallCalleeResumeSafe,
		NumericParamCount:    rawIntSelfABI.NumParams,
		RawIntSelfABI:        rawIntSelfABI,
		NumericEntryOffset:   numericEntryOff,
		TypedSelfABI:         typedSelfABI,
		TypedEntryOffset:     typedEntryOff,
		GlobalCache:          globalCache,
		GlobalCacheConsts:    ec.globalCacheConsts,
		NativeSetGlobals:     nativeSetGlobals,
		CallCache:            callCache,
		NewTableCaches:       ec.newTableCaches,
		InstrCodeRanges:      ec.instrCodeRanges,
		ExitSites:            buildExitSiteMeta(fn),
		ExitResumeCheck:      ec.exitResumeCheck,
	}, nil
}

func validateCompileInputs(fn *Function, alloc *RegAllocation) error {
	if fn == nil {
		return fmt.Errorf("methodjit: compile nil function")
	}
	if alloc == nil {
		return fmt.Errorf("methodjit: compile nil register allocation")
	}
	if fn.Entry == nil {
		return fmt.Errorf("methodjit: compile function has nil entry block")
	}
	if len(fn.Blocks) == 0 {
		return fmt.Errorf("methodjit: compile function has no blocks")
	}
	entryFound := false
	for _, block := range fn.Blocks {
		if block == fn.Entry {
			entryFound = true
			break
		}
	}
	if !entryFound {
		return fmt.Errorf("methodjit: compile entry block B%d is missing from block list", fn.Entry.ID)
	}
	return nil
}

func collectNativeSetGlobals(fn *Function) map[int]bool {
	out := make(map[int]bool)
	if !fnSupportsNativeSetGlobalProtocol(fn) {
		return out
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpSetGlobal {
				out[int(instr.Aux)] = true
			}
		}
	}
	return out
}

func fnSupportsIndexedGlobalProtocol(fn *Function) bool {
	return fn != nil && fn.Proto != nil
}

func protoSupportsIndexedGlobalProtocol(proto *vm.FuncProto) bool {
	return proto != nil
}

func fnSupportsNativeSetGlobalProtocol(fn *Function) bool {
	return fn != nil && protoSupportsNativeSetGlobalProtocol(fn.Proto)
}

func protoSupportsNativeSetGlobalProtocol(proto *vm.FuncProto) bool {
	return proto != nil && proto.Name == "<main>"
}

func loopBodyHasDirectDeopt(fn *Function, bodyBlocks map[int]bool) bool {
	for _, block := range fn.Blocks {
		if !bodyBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instrMayDirectDeoptWithoutFullFlush(instr) {
				return true
			}
		}
	}
	return false
}

func instrMayDirectDeoptWithoutFullFlush(instr *Instr) bool {
	if instr == nil {
		return false
	}
	switch instr.Op {
	case OpGuardType, OpGuardIntRange, OpNumToFloat, OpDivIntExact,
		OpGetFieldNumToFloat,
		OpMatrixGetF, OpMatrixSetF, OpMatrixFlat, OpMatrixStride,
		OpTableArrayHeader, OpTableArrayLoad, OpTableArrayStore, OpTableArrayNestedLoad:
		return true
	case OpGetField:
		return instr.Type == TypeFloat
	default:
		return false
	}
}

// emitContext holds transient state during code generation.
type emitContext struct {
	fn           *Function
	alloc        *RegAllocation
	asm          *jit.Assembler
	slotMap      map[int]int // SSA value ID -> home slot index in VM register file
	nextSlot     int         // next available temp slot
	labelCounter int         // counter for generating unique labels

	// activeRegs tracks which value IDs have their register allocation active
	// in the current block. Values from other blocks must be loaded from memory.
	// Reset at the start of each block.
	activeRegs map[int]bool

	// crossBlockLive is the set of value IDs that are used in blocks other than
	// where they're defined. These values need write-through to memory.
	// Values only used within their defining block skip the memory write.
	crossBlockLive map[int]bool

	// valueReprs is the explicit representation lattice for active allocated
	// registers. Boxed is the default absence value. rawIntRegs and
	// rawTablePtrRegs remain compatibility mirrors rebuilt from valueReprs.
	valueReprs map[int]valueRepr

	// rawIntRegs tracks which value IDs have RAW int64 (not NaN-boxed) content
	// in their allocated register. Compatibility mirror for valueReprs.
	rawIntRegs map[int]bool

	// rawTablePtrRegs tracks value IDs whose allocated GPR holds a raw
	// *runtime.Table pointer. Home slots always hold a boxed table Value so
	// exit-resume and GC-visible VM state never see raw pointers.
	// Compatibility mirror for valueReprs.
	rawTablePtrRegs map[int]bool

	// shapeVerified tracks table SSA value IDs whose shape has been verified
	// in the current block. Maps table value ID -> verified shapeID.
	// Reset at block boundaries and after calls.
	shapeVerified map[int]uint32

	// tableVerified tracks table SSA value IDs whose table identity
	// (type check, nil check, metatable check) has been verified in the
	// current block. Maps table value ID -> true.
	// Reset at block boundaries and after calls (same as shapeVerified).
	tableVerified map[int]bool

	// keysDirtyWritten tracks table SSA value IDs whose keysDirty byte
	// has already been written to 1 in the current block. Subsequent
	// SetTables on the same table elide the MOVimm16+STRB (2 insns).
	// The flag is idempotent (always set to 1), so consecutive writes
	// produce the same final state. Reset at block boundaries and after
	// calls (same as tableVerified).
	keysDirtyWritten map[int]bool

	// localNewTablesNoMetatable is true when this function has no call/op-exit
	// surface that can install a metatable on tables allocated by OpNewTable.
	localNewTablesNoMetatable bool

	// kindVerified tracks table SSA value IDs whose ArrayKind has been
	// guard-checked in the current block. Maps table value ID -> the
	// AKKind constant (jit.AKMixed/AKInt/AKFloat/AKBool) last verified.
	// When an emit path is about to emit a knownKind kind guard and the
	// map entry already equals that kind, the guard (LDRB+CMP+BCond+B)
	// is skipped — just emit the direct B to the matching label.
	// Reset at block boundaries and after calls (same as tableVerified).
	// Invalidated at the END of each GetTable/SetTable emission because
	// the deopt path can enter runtime code that may demote the array
	// kind (same conservative pattern as tableVerified).
	kindVerified map[int]uint16

	// dmVerified tracks table SSA value IDs that have been confirmed as
	// DenseMatrix outers (dmStride > 0) in the current block. Lets
	// subsequent matrix.getf/setf calls on the same m skip the stride
	// guard LDR+CBZ. Reset at block boundaries and after calls.
	// Populated by emitMatrixGetF/emitMatrixSetF (R44).
	dmVerified map[int]bool

	// fieldSvalsCache tracks X1 when it is known to hold Table.svals data for
	// a verified (table SSA value, shape) pair. It is scoped to consecutive
	// field ops in one emitted block because X1 is scratch everywhere else.
	fieldSvalsCacheValid   bool
	fieldSvalsCacheTableID int
	fieldSvalsCacheShapeID uint32

	// blockOutShapes saves the shapeVerified state at the END of each emitted block.
	// Used to seed single-predecessor blocks with their predecessor's verified shapes.
	blockOutShapes map[int]map[int]uint32

	// blockOutTables saves the tableVerified state at the END of each emitted block.
	blockOutTables map[int]map[int]bool

	// blockOutKinds saves the kindVerified state at the END of each emitted
	// block. Used to seed single-predecessor blocks with their predecessor's
	// verified kinds (mirrors blockOutTables).
	blockOutKinds map[int]map[int]uint16

	// blockOutKeysDirty saves the keysDirtyWritten state at end of block.
	blockOutKeysDirty map[int]map[int]bool

	// tableArrayBoundedKeys tracks instruction-local (table, key) pairs whose
	// immediately preceding TableArrayLoad has a live native-success flag in
	// X17. The flag is set on the native load success path and cleared on the
	// exit-resume path, so a following SetTable can branch around its redundant
	// bounds check without assuming the load succeeded after resume.
	tableArrayBoundedKeys map[tableArrayBoundKey]bool

	// blockOutRawIntRegs saves the raw-int GPR state at end of block, keyed
	// by block ID then physical register. Single-predecessor successors can
	// activate these values when liveness says they are live-in.
	blockOutRawIntRegs map[int]map[int]loopRegEntry

	// blockLiveIn is block-level SSA liveness used to bound raw-int carry.
	blockLiveIn map[int]map[int]bool

	// blockLiveOut and instrLiveAfter bound call spills and active-state
	// lifetime for values carried across block boundaries.
	blockLiveOut   map[int]map[int]bool
	instrLiveAfter map[int]map[int]bool

	rawIntBlockCarry bool

	// rawIntCarryNoStore marks raw-int values whose cross-block uses are
	// covered by immediate single-predecessor carry. Their boxed SSA homes are
	// materialized only on deopt/fallback while live.
	rawIntCarryNoStore map[int]bool

	// activeFPRegs tracks which value IDs have their FPR allocation active
	// in the current block. Mirrors activeRegs for FPR-allocated values.
	// Reset at the start of each block.
	activeFPRegs map[int]bool

	// useFPR is true if any values are allocated to FPR registers.
	// When false, FPR save/restore in prologue/epilogue is skipped.
	useFPR bool

	// callExitIDs tracks the instruction IDs of call-exit points.
	// After finalization, these are used to resolve resume label addresses.
	callExitIDs []int

	// deferredResumes tracks resume entry points to emit after the epilogue.
	deferredResumes []deferredResume

	// loop tracks loop structure for raw-int loop optimization.
	// When non-nil and a block is inside a loop, emitPhiMoves to loop
	// headers transfers raw ints, and loop header phis are marked rawInt.
	loop *loopInfo

	// loopHeaderRegs is the per-header register state at the end of each loop
	// header. Maps headerBlockID -> (register number -> entry). Non-header
	// loop blocks look up their innermost header to activate the right registers.
	loopHeaderRegs map[int]map[int]loopRegEntry

	// loopHeaderFPRegs is the per-header FPR register state at the end of
	// each loop header. Maps headerBlockID -> (FPR number -> entry).
	loopHeaderFPRegs map[int]map[int]loopFPRegEntry

	// safeHeaderRegs are the subset of loopHeaderRegs whose registers are
	// NOT clobbered by any non-header block in the loop body. Only these
	// values can safely be activated in non-header blocks.
	safeHeaderRegs   map[int]map[int]loopRegEntry
	safeHeaderFPRegs map[int]map[int]loopFPRegEntry

	// loopInvariantGPRs are selected loop-invariant GPR values (currently
	// typed-array len/data facts) whose registers are pinned by regalloc and
	// can be activated in every block of the owning loop.
	loopInvariantGPRs map[int]map[int]loopRegEntry
	// loopInvariantFPRs are selected loop-invariant float values whose
	// registers are pinned by regalloc and can be activated in every block of
	// the owning loop.
	loopInvariantFPRs map[int]map[int]loopFPRegEntry

	// loopPhiOnlyArgs is the set of value IDs that are ONLY used as phi args
	// to loop header phis. storeRawInt skips write-through for these values
	// since emitPhiMoveRawInt reads from the register directly.
	loopPhiOnlyArgs loopPhiArgSet
	// loopFPPhiOnlyArgs is the FPR equivalent for raw-float values.
	loopFPPhiOnlyArgs loopPhiArgSet

	// loopExitBoxPhis is the set of phi value IDs that need boxing at loop
	// exit. These are loop header phis that are cross-block live (used
	// outside the loop) but whose write-through is deferred to exit time.
	loopExitBoxPhis map[int]bool
	// loopExitBoxFPPhis is the FPR equivalent for raw-float header phis.
	loopExitBoxFPPhis map[int]bool
	// loopExitStorePhis is the NaN-boxed GPR equivalent: header phis whose
	// register already holds the boxed runtime Value can defer memory write-
	// through until leaving the loop.
	loopExitStorePhis map[int]bool

	// currentBlockID is the ID of the block currently being emitted.
	currentBlockID int

	// constInts maps value ID -> int64 for ConstInt instructions.
	// Used by emitRawIntBinOp to emit ADDimm/SUBimm for small constants.
	constInts map[int]int64

	// constBools maps value ID -> 0 (false) or 1 (true) for ConstBool instructions.
	// Used by emitSetTableNative to bypass runtime tag checks for constant bools.
	constBools map[int]int64

	// irTypes maps value ID -> IR Type from the defining instruction.
	// Used by resolveRawFloat to detect NaN-boxed ints that need SCVTF
	// conversion instead of FMOVtoFP.
	irTypes map[int]Type

	// nextGlobalCacheIndex is the next available cache slot index for
	// OpGetGlobal native cache. Each GetGlobal instruction gets a unique
	// index (0, 1, 2, ...) assigned at emission time.
	nextGlobalCacheIndex int
	globalCacheConsts    []int

	// nextCallCacheIndex (R108) assigns a unique IC slot to each OpCall
	// in the compiled function. 4 uint64 per slot (closure value,
	// direct-entry addr, proto ptr, direct-entry version). Incremented in
	// emitCallNative.
	nextCallCacheIndex int

	// scratchFPRCache maps value ID -> scratch FPR (D0-D3) currently holding
	// that value's raw float. Scoped to a SINGLE instruction's operand resolution
	// — cleared at the start of every emitInstr call. Enables dedup of same-value
	// operands within one instruction (e.g., v*v loads v only once).
	scratchFPRCache map[int]jit.FReg

	// fusedCmps is the set of comparison instruction IDs that will be fused
	// with their immediately-following Branch. These comparisons emit only
	// CMP/FCMP (no CSET+ORR bool materialization).
	fusedCmps map[int]bool

	// tailCallInstrs (R107) is the set of OpCall instruction IDs that are
	// in tail position: their result is consumed by the immediately-following
	// OpReturn in the same block. Populated by computeTailCalls at
	// emitContext construction. The tail-call emit does a BR to the
	// callee's direct entry on the fast path; the following OpReturn is
	// emitted as dead code (fast-path never falls through) but remains
	// live on the slow-path fallback (emitCallExitFallback produces a
	// normal return value that the Return then handles).
	tailCallInstrs map[int]bool

	// newTableCaches is owned by the eventual CompiledFunction but allocated
	// before emission so native NewTable fast paths can embed its backing address.
	newTableCaches []newTableCacheEntry

	// skipStandardDirectEntry lets a custom leaf emitter publish its own
	// t2_direct_entry without colliding with the standard full-frame entry.
	skipStandardDirectEntry bool

	// nativeCallReplaySafe is false when direct/native callers must not enter
	// this function because a callee exit would replay visible side effects.
	nativeCallReplaySafe bool

	// nativeCallCalleeResumeSafe is true when Tier 2 native callers can recover
	// from a callee exit by resuming the callee instead of replaying the call.
	nativeCallCalleeResumeSafe bool

	// rawIntSelfABI is the explicit private numeric self-recursive entry
	// descriptor for this compile. It is the source of truth for raw self
	// call emission during pass 2.
	rawIntSelfABI RawIntSelfABI

	// typedSelfABI describes the private typed self-recursive entry for
	// recursive table/int kernels that are not pure raw-int numeric kernels.
	typedSelfABI TypedSelfABI

	// entryShapeGuards are callee-entry table shape guards keyed by parameter
	// index. Every path that reaches the optimized body must either execute
	// these guards or fall back to the boxed VM call path first.
	entryShapeGuards map[int]FixedShapeTableFact

	// numericParamCount (R124) is set at emitContext construction when
	// the proto qualifies (qualifyForNumeric). Non-zero → Compile emits
	// an additional numeric body (pass 2) with the entry label
	// `t2_numeric_self_entry_N`.
	numericParamCount int

	// numericMode is set to true during pass 2 (numeric variant emit).
	// When true, block labels are prefixed "num_" (via blockLabelFor),
	// parameter LoadSlot reads raw ABI registers, Return branches through
	// num_epilogue with raw X0, and eligible recursive calls use the
	// raw-int self ABI.
	numericMode bool

	// numericParamSlots (R126) is the set of VM register slots that
	// correspond to function parameters. Populated when numericParamCount
	// > 0. In pass 2, LoadSlot for these slots reads X0..X(N-1) instead
	// of loading boxed VM slots.
	numericParamSlots map[int]bool

	// fusedCond holds the condition code from the last fused comparison.
	// Set by emitIntCmp/emitFloatCmp when the comparison is in fusedCmps.
	fusedCond jit.Cond

	// fusedActive is true when the preceding comparison was fused and
	// emitBranch should use fusedCond + B.cc instead of TBNZ.
	fusedActive bool

	// instrCodeRanges records the machine-code byte range emitted for each IR
	// instruction. It is diagnostic metadata only; offsets are relative to the
	// start of the compiled code block.
	instrCodeRanges []InstrCodeRange

	// exitResumeCheck carries debug-only site metadata and enables shadow
	// materialization writes when GSCRIPT_EXIT_RESUME_CHECK=1 at compile time.
	exitResumeCheck *exitResumeCheckMetadata
}

// computeTailCalls (R107) scans the IR for the tail-call pattern:
// an OpCall immediately followed (within the same block, skipping OpNop)
// by an OpReturn whose single arg is the Call's result. Returns a set
// of matching OpCall IDs. The caller's emit path uses emitCallNativeTail
// for these and skips the following Return's emission.
func computeTailCalls(fn *Function) map[int]bool {
	out := make(map[int]bool)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			// Find the next non-nop instruction.
			j := i + 1
			for j < len(block.Instrs) && block.Instrs[j].Op == OpNop {
				j++
			}
			if j >= len(block.Instrs) {
				continue
			}
			next := block.Instrs[j]
			if next.Op != OpReturn {
				continue
			}
			if len(next.Args) != 1 || next.Args[0].ID != instr.ID {
				continue
			}
			out[instr.ID] = true
		}
	}
	return out
}

// isFusableComparison returns true for comparison ops that can be fused
// with an immediately-following Branch (emit CMP/FCMP + B.cc).
func isFusableComparison(op Op) bool {
	switch op {
	case OpEq, OpLtInt, OpLeInt, OpEqInt, OpModZeroInt, OpLtFloat, OpLeFloat:
		return true
	}
	return false
}

func computeFusedComparisons(fn *Function) map[int]bool {
	useCounts := computeUseCounts(fn)
	fusedCmps := make(map[int]bool)
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if !isFusableComparison(instr.Op) || useCounts[instr.ID] != 1 {
				continue
			}
			if i+1 < len(block.Instrs) {
				next := block.Instrs[i+1]
				if next.Op == OpBranch && len(next.Args) > 0 && next.Args[0].ID == instr.ID {
					fusedCmps[instr.ID] = true
				}
			}
		}
	}
	return fusedCmps
}

// assignSlots assigns a home slot for every SSA value.
// LoadSlot values keep their original VM slot; all others get temp slots.
func (ec *emitContext) assignSlots() {
	for _, block := range ec.fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			if instr.Op == OpLoadSlot {
				ec.slotMap[instr.ID] = int(instr.Aux)
			} else {
				ec.slotMap[instr.ID] = ec.nextSlot
				ec.nextSlot++
			}
		}
	}
}

// slotOffset returns the byte offset for a slot in the VM register file.
func slotOffset(slot int) int {
	return slot * jit.ValueSize
}

// loadValue loads a NaN-boxed value from its home slot into the given scratch register.
func (ec *emitContext) loadValue(dst jit.Reg, valueID int) {
	slot, ok := ec.slotMap[valueID]
	if !ok {
		return
	}
	ec.asm.LDR(dst, mRegRegs, slotOffset(slot))
}

// storeValue stores a NaN-boxed value from a scratch register to its home slot.
func (ec *emitContext) storeValue(src jit.Reg, valueID int) {
	slot, ok := ec.slotMap[valueID]
	if !ok {
		return
	}
	ec.asm.STR(src, mRegRegs, slotOffset(slot))
}

// blockLabel returns the assembler label name for a basic block.
// Numeric variant (pass 2) prefixes with "num_" to avoid label
// collision with the normal pass-1 body.
func blockLabel(b *Block) string {
	return fmt.Sprintf("B%d", b.ID)
}

func (ec *emitContext) entryBlockLabel() string {
	label, ok := ec.entryBlockLabelOK()
	if !ok {
		panic("methodjit: entry label requested without function entry")
	}
	return label
}

func (ec *emitContext) entryBlockLabelOK() (string, bool) {
	if ec == nil || ec.fn == nil || ec.fn.Entry == nil {
		return "", false
	}
	return ec.blockLabelFor(ec.fn.Entry), true
}

// emitNumericBody emits a second Tier 2 body under numericMode=true.
// The numeric entry label receives raw int args in X0..X(N-1), builds a thin
// FP/LR frame, and jumps to the pass-2 entry block. Raw callers pass the callee
// VM register base directly in the pinned mRegRegs register and spill/reload
// their own live allocated registers around the BL/BLR, so this entry does not
// save the full callee-saved register set used by the boxed public ABI.
func (ec *emitContext) emitNumericBody() {
	if ec.numericParamCount <= 0 {
		return
	}
	if ec.fn == nil || ec.fn.Proto == nil {
		return
	}

	asm := ec.asm
	prevNumericMode := ec.numericMode
	prevActiveRegs := ec.activeRegs
	prevReprs := ec.snapshotValueReprs()
	prevActiveFPRegs := ec.activeFPRegs
	prevShapeVerified := ec.shapeVerified
	prevTableVerified := ec.tableVerified
	prevKindVerified := ec.kindVerified
	prevKeysDirtyWritten := ec.keysDirtyWritten
	prevTableArrayBoundedKeys := ec.tableArrayBoundedKeys
	prevDMVerified := ec.dmVerified
	prevFieldSvalsCacheValid := ec.fieldSvalsCacheValid
	prevFieldSvalsCacheTableID := ec.fieldSvalsCacheTableID
	prevFieldSvalsCacheShapeID := ec.fieldSvalsCacheShapeID
	ec.numericMode = true
	entryLabel, ok := ec.entryBlockLabelOK()
	if !ok {
		ec.numericMode = prevNumericMode
		return
	}

	label := fmt.Sprintf("t2_numeric_self_entry_%d", ec.numericParamCount)
	asm.Label(label)
	asm.SUBimm(jit.SP, jit.SP, uint16(numericSelfEntryFrameSize))
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.B(entryLabel)

	ec.activeRegs = make(map[int]bool)
	ec.resetValueReprs()
	ec.activeFPRegs = make(map[int]bool)
	ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool)
	ec.shapeVerified = make(map[int]uint32)
	ec.tableVerified = make(map[int]bool)
	ec.kindVerified = make(map[int]uint16)
	ec.keysDirtyWritten = make(map[int]bool)
	ec.dmVerified = make(map[int]bool)
	ec.invalidateFieldSvalsCache()
	for _, block := range ec.fn.Blocks {
		ec.emitBlock(block)
	}
	ec.numericMode = prevNumericMode
	ec.activeRegs = prevActiveRegs
	ec.restoreValueReprSnapshot(prevReprs)
	ec.activeFPRegs = prevActiveFPRegs
	ec.shapeVerified = prevShapeVerified
	ec.tableVerified = prevTableVerified
	ec.kindVerified = prevKindVerified
	ec.keysDirtyWritten = prevKeysDirtyWritten
	ec.tableArrayBoundedKeys = prevTableArrayBoundedKeys
	ec.dmVerified = prevDMVerified
	ec.fieldSvalsCacheValid = prevFieldSvalsCacheValid
	ec.fieldSvalsCacheTableID = prevFieldSvalsCacheTableID
	ec.fieldSvalsCacheShapeID = prevFieldSvalsCacheShapeID
}

// blockLabelFor returns the label for block b in the given emit pass.
// When ec.numericMode is true, returns the prefixed variant.
func (ec *emitContext) blockLabelFor(b *Block) string {
	if ec.numericMode {
		return fmt.Sprintf("num_B%d", b.ID)
	}
	return blockLabel(b)
}

// passLabel (R128 label refactor) wraps a fixed label name with the
// current pass's suffix. Normal pass → unchanged; numeric pass →
// "_num" suffix. Used to disambiguate pass-1 vs pass-2 labels that
// would otherwise collide (call_continue_N, global_continue_N,
// op_continue_N, table_continue_N, call_resume_N).
func (ec *emitContext) passLabel(base string) string {
	if ec.numericMode {
		return base + "_num"
	}
	return base
}

// callExitResumeLabel returns the resume-label name for an instrID
// in the current pass. Free function version kept for backward compat
// in emitDeferredResumes which needs to re-derive the label per entry.
func callExitResumeLabelForPass(instrID int, numericMode bool) string {
	s := fmt.Sprintf("call_resume_%d", instrID)
	if numericMode {
		s += "_num"
	}
	return s
}

// frameSize is the stack frame size for callee-saved registers.
const frameSize = 128

const (
	callModeDirect    = 1
	callModeTypedSelf = 2
)

// numericSelfEntryFrameSize is the thin raw-int self-recursive frame. Raw
// callers preserve their own live allocated registers, so the numeric entry
// only needs FP/LR for the native BL/RET chain.
const numericSelfEntryFrameSize = 16

// emitTier2EntryMark writes 1 to proto.EnteredTier2 (one byte). It is
// called at the head of each Tier 2 entry point so that a single glance
// at proto.EnteredTier2 (e.g. through -jit-stats or the bench harness)
// answers "did native Tier 2 code actually run for this proto?". Uses
// X16/X17 — AAPCS scratch registers (IP0/IP1) — which are safe at entry
// before any callee-saved registers are live. Cost: ~6 insns per
// invocation (LoadImm64 up to 4 + MOVimm16 + STRB). For fib at ~1M
// entries per run this is ~1.5 ms out of 0.9 s (~0.17%, inside noise).
//
// The address of proto.EnteredTier2 is stable because Go's GC is
// non-moving for heap allocations; FuncProto is heap-allocated and is
// kept alive by the owning VM/Closure for the lifetime of the code.
func (ec *emitContext) emitTier2EntryMark() {
	if ec.fn == nil || ec.fn.Proto == nil {
		return
	}
	asm := ec.asm
	addr := int64(uintptr(unsafe.Pointer(&ec.fn.Proto.EnteredTier2)))
	asm.LoadImm64(jit.X16, addr)
	asm.MOVimm16(jit.X17, 1)
	asm.STRB(jit.X17, jit.X16, 0)
}

func (ec *emitContext) emitSetRawSelfRegsEndFromMRegRegs() {
	if ec.numericParamCount < 2 {
		return
	}
	ec.emitSetRawSelfRegsEnd(mRegRegs, ec.nextSlot, jit.X16, jit.X17)
}

func (ec *emitContext) emitSetRawSelfRegsEnd(baseReg jit.Reg, numRegs int, scratchActual, scratchBudget jit.Reg) {
	if numRegs <= 0 {
		return
	}
	asm := ec.asm
	useActualLabel := ec.uniqueLabel("rawself_regsend_actual")
	doneLabel := ec.uniqueLabel("rawself_regsend_done")
	budgetBytes := numRegs * (maxRawSelfCallDepth + 1) * jit.ValueSize

	asm.LDR(scratchActual, mRegCtx, execCtxOffRegsEnd)
	if budgetBytes <= 4095 {
		asm.ADDimm(scratchBudget, baseReg, uint16(budgetBytes))
	} else {
		asm.LoadImm64(scratchBudget, int64(budgetBytes))
		asm.ADDreg(scratchBudget, baseReg, scratchBudget)
	}
	asm.CMPreg(scratchBudget, scratchActual)
	asm.BCond(jit.CondHI, useActualLabel)
	asm.STR(scratchBudget, mRegCtx, execCtxOffRawSelfRegsEnd)
	asm.B(doneLabel)
	asm.Label(useActualLabel)
	asm.STR(scratchActual, mRegCtx, execCtxOffRawSelfRegsEnd)
	asm.Label(doneLabel)
}

func (ec *emitContext) hasEntryShapeGuards() bool {
	return ec != nil && len(ec.entryShapeGuards) > 0
}

func (ec *emitContext) emitBoxedEntryShapeGuards() {
	if !ec.hasEntryShapeGuards() {
		return
	}
	params := make([]int, 0, len(ec.entryShapeGuards))
	for paramIdx, fact := range ec.entryShapeGuards {
		if fact.ShapeID != 0 {
			params = append(params, paramIdx)
		}
	}
	if len(params) == 0 {
		return
	}
	sort.Ints(params)
	failLabel := ec.uniqueLabel("entry_shape_deopt")
	doneLabel := ec.uniqueLabel("entry_shape_done")
	for _, paramIdx := range params {
		fact := ec.entryShapeGuards[paramIdx]
		ec.asm.LDR(jit.X0, mRegRegs, slotOffset(paramIdx))
		jit.EmitCheckIsTableFull(ec.asm, jit.X0, jit.X16, jit.X17, failLabel)
		jit.EmitExtractPtr(ec.asm, jit.X0, jit.X0)
		ec.asm.CBZ(jit.X0, failLabel)
		ec.asm.LDRW(jit.X16, jit.X0, jit.TableOffShapeID)
		ec.asm.LoadImm64(jit.X17, int64(fact.ShapeID))
		ec.asm.CMPreg(jit.X16, jit.X17)
		ec.asm.BCond(jit.CondNE, failLabel)
	}
	ec.asm.B(doneLabel)
	ec.asm.Label(failLabel)
	ec.emitDeopt(nil)
	ec.asm.Label(doneLabel)
}

func (ec *emitContext) seedEntryShapeGuardState(block *Block) {
	if !ec.hasEntryShapeGuards() || ec.fn == nil || block == nil || block != ec.fn.Entry {
		return
	}
	if len(block.Preds) != 0 {
		return
	}
	for _, instr := range block.Instrs {
		if instr.Op != OpLoadSlot {
			continue
		}
		fact, ok := ec.entryShapeGuards[int(instr.Aux)]
		if !ok || fact.ShapeID == 0 {
			continue
		}
		ec.shapeVerified[instr.ID] = fact.ShapeID
	}
}

func emitBoxTablePtr(asm *jit.Assembler, dst, ptr, scratch jit.Reg) {
	asm.UBFX(dst, ptr, 0, 44)
	asm.LoadImm64(scratch, nb64(jit.NB_TagPtr))
	asm.ORRreg(dst, dst, scratch)
}

func (ec *emitContext) typedSelfAfterParamsLabel() string {
	return "t2_typed_self_after_params"
}

func (ec *emitContext) typedSelfEntryParamLoads(block *Block) map[int]bool {
	if ec == nil || ec.numericMode || !ec.typedSelfABI.Eligible || ec.fn == nil || block == nil || block != ec.fn.Entry {
		return nil
	}
	remaining := make(map[int]bool, ec.typedSelfABI.NumParams)
	for i := 0; i < ec.typedSelfABI.NumParams; i++ {
		remaining[i] = true
	}
	if len(remaining) == 0 {
		return nil
	}
	pending := make(map[int]bool, len(remaining))
	for _, instr := range block.Instrs {
		if instr.Op != OpLoadSlot {
			break
		}
		slot := int(instr.Aux)
		if !remaining[slot] {
			return nil
		}
		pending[slot] = true
		delete(remaining, slot)
		if len(remaining) == 0 {
			return pending
		}
	}
	return nil
}

func (ec *emitContext) entryParamLoad(slot int) (*Instr, bool) {
	if ec == nil || ec.fn == nil || ec.fn.Entry == nil {
		return nil, false
	}
	for _, instr := range ec.fn.Entry.Instrs {
		if instr.Op == OpLoadSlot && int(instr.Aux) == slot {
			return instr, true
		}
	}
	return nil, false
}

func (ec *emitContext) emitTypedSelfEntry() {
	asm := ec.asm
	asm.Label("t2_typed_self_entry")
	entryParamLoads := ec.typedSelfEntryParamLoads(ec.fn.Entry)
	ec.emitTier2EntryMark()
	asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)
	if ec.useFPR {
		asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
		asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
	}

	for i, rep := range ec.typedSelfABI.Params {
		src := jit.Reg(int(jit.X0) + i)
		load, hasLoad := ec.entryParamLoad(i)
		hasLoad = hasLoad && entryParamLoads != nil && entryParamLoads[i]
		switch rep {
		case SpecializedABIParamRawInt:
			jit.EmitBoxIntFast(asm, jit.X16, src, mRegTagInt)
			asm.STR(jit.X16, mRegRegs, slotOffset(i))
			if hasLoad {
				if pr, ok := ec.alloc.ValueRegs[load.ID]; ok && !pr.IsFloat {
					dst := jit.Reg(pr.Reg)
					if load.Type == TypeInt {
						if src != dst {
							asm.MOVreg(dst, src)
						}
					} else if dst != jit.X16 {
						asm.MOVreg(dst, jit.X16)
					}
				}
			}
		case SpecializedABIParamRawTablePtr:
			emitBoxTablePtr(asm, jit.X16, src, jit.X17)
			asm.STR(jit.X16, mRegRegs, slotOffset(i))
			if hasLoad {
				if pr, ok := ec.alloc.ValueRegs[load.ID]; ok && !pr.IsFloat {
					dst := jit.Reg(pr.Reg)
					if dst != jit.X16 {
						asm.MOVreg(dst, jit.X16)
					}
				}
			}
		}
	}
	if entryParamLoads != nil {
		asm.B(ec.typedSelfAfterParamsLabel())
	} else {
		asm.B(ec.entryBlockLabel())
	}
}

func (ec *emitContext) emitTypedSelfRawIntReturnEpilogue() {
	ec.asm.Label("t2_typed_self_raw_int_epilogue")
	ec.asm.MOVimm16(jit.X16, 0)
	ec.asm.STR(jit.X16, mRegCtx, execCtxOffExitCode)
	ec.emitFullFrameRestoreAndReturn()
}

func (ec *emitContext) emitTypedSelfReturnEpilogue() {
	asm := ec.asm
	asm.Label("t2_typed_self_epilogue")
	failLabel := ec.uniqueLabel("typed_self_return_fail")
	doneLabel := ec.uniqueLabel("typed_self_return_done")

	switch ec.typedSelfABI.Return {
	case SpecializedABIReturnNone:
		// Zero-result typed self calls return only status; X0 is ignored by
		// the caller and CALL C=1 must not fabricate a result slot.
	case SpecializedABIReturnRawInt:
		emitCheckIsInt(asm, jit.X0, jit.X1)
		asm.BCond(jit.CondNE, failLabel)
		jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	case SpecializedABIReturnRawTablePtr:
		jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, failLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	default:
		asm.B(failLabel)
	}
	asm.MOVimm16(jit.X16, 0)
	asm.STR(jit.X16, mRegCtx, execCtxOffExitCode)
	asm.B(doneLabel)

	asm.Label(failLabel)
	asm.LoadImm64(jit.X16, ExitDeopt)
	asm.STR(jit.X16, mRegCtx, execCtxOffExitCode)

	asm.Label(doneLabel)
	ec.emitFullFrameRestoreAndReturn()
}

func (ec *emitContext) emitFullFrameRestoreAndReturn() {
	asm := ec.asm
	if ec.useFPR {
		asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
		asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
	}
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	asm.RET()
}

func (ec *emitContext) emitPrologue() {
	asm := ec.asm

	// R146: mark native entry before anything else (AAPCS scratch only).
	ec.emitTier2EntryMark()

	// Allocate stack frame.
	asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
	// Save FP, LR.
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	// Set FP = SP.
	asm.ADDimm(jit.X29, jit.SP, 0)
	// Save callee-saved GPRs.
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)
	// Save callee-saved FPRs only if float values are register-allocated.
	if ec.useFPR {
		asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
		asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
	}

	// Set up pinned registers.
	// X0 holds ExecContext pointer (from callJIT trampoline).
	asm.MOVreg(mRegCtx, jit.X0)                       // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)        // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))    // X24 = 0xFFFE000000000000
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))  // X25 = 0xFFFD000000000000
	ec.emitSetRawSelfRegsEndFromMRegRegs()
	ec.emitBoxedEntryShapeGuards()
	if ec.fn != nil && ec.fn.Entry != nil && len(ec.fn.Blocks) > 0 && ec.fn.Blocks[0] != ec.fn.Entry {
		asm.B(ec.entryBlockLabel())
	}
}

func (ec *emitContext) emitEpilogue() {
	asm := ec.asm

	asm.Label("epilogue")

	// Store exit code 0 (normal return) to ExecContext.
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	// Shared register restore and return (used by both normal and deopt paths).
	asm.Label("deopt_epilogue")

	// Restore callee-saved FPRs only if they were saved.
	if ec.useFPR {
		asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
		asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
	}
	// Restore callee-saved GPRs.
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	// Restore FP, LR.
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	// Deallocate stack frame.
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	// Return.
	asm.RET()

	if !ec.skipStandardDirectEntry {
		// --- Direct entry point for BLR callers (Tier 1 native call) ---
		// Uses the FULL frame (same as normal entry) because Tier 2 may use
		// callee-saved GPRs (X20-X23) for register allocation. The Tier 1
		// caller expects callee-saved registers to be preserved across BLR.
		// Caller has set: X0=ctx, ctx.Regs=callee regs base,
		// ctx.Constants=callee constants, CallMode=1.
		asm.Label("t2_direct_entry")
		// R146: mark native entry (BLR-from-Tier-1 path).
		ec.emitTier2EntryMark()
		asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
		asm.STP(jit.X29, jit.X30, jit.SP, 0)
		asm.ADDimm(jit.X29, jit.SP, 0)
		asm.STP(jit.X19, jit.X20, jit.SP, 16)
		asm.STP(jit.X21, jit.X22, jit.SP, 32)
		asm.STP(jit.X23, jit.X24, jit.SP, 48)
		asm.STP(jit.X25, jit.X26, jit.SP, 64)
		asm.STP(jit.X27, jit.X28, jit.SP, 80)
		if ec.useFPR {
			asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
			asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
		}
		asm.MOVreg(mRegCtx, jit.X0)                       // X19 = ctx
		asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)        // X26 = ctx.Regs
		asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
		asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))    // X24
		asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))  // X25
		ec.emitSetRawSelfRegsEndFromMRegRegs()
		ec.emitBoxedEntryShapeGuards()
		asm.B(ec.entryBlockLabel())
	}

	// --- Self-call entry point (R40) ---
	// Only emitted when the function has self-calls AND the Tier 2 emit
	// will generate BL "t2_self_entry". Gated on ec.fn.Proto.HasSelfCalls.
	// This keeps insn count unchanged for non-self-recursive functions.
	//
	// Lightweight entry for proven-self Tier 2 calls. Caller has already
	// set up: ctx (unchanged), ctx.Regs (advanced), ctx.Constants
	// (unchanged, same proto), tag constants X24/X25 (unchanged).
	// Skip: MOVreg mRegCtx, LDR mRegConsts, LoadImm64 X24/X25.
	// Keep: frame allocation, callee-saved regs save (ARM64 ABI),
	//       LDR mRegRegs from ctx.Regs (caller advanced it).
	//
	// Savings: 4 setup insns per self-call (MOVreg + LDR X27 +
	//          2×LoadImm64). Blast radius: small; correctness argument:
	//          self-call means same proto, same ctx, tags are
	//          invariant globals.
	if ec.fn != nil && ec.fn.Proto != nil && ec.fn.Proto.HasSelfCalls {
		asm.Label("t2_self_entry")
		asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
		asm.STP(jit.X29, jit.X30, jit.SP, 0)
		asm.ADDimm(jit.X29, jit.SP, 0)
		asm.STP(jit.X19, jit.X20, jit.SP, 16)
		asm.STP(jit.X21, jit.X22, jit.SP, 32)
		asm.STP(jit.X23, jit.X24, jit.SP, 48)
		asm.STP(jit.X25, jit.X26, jit.SP, 64)
		asm.STP(jit.X27, jit.X28, jit.SP, 80)
		if ec.useFPR {
			asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
			asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
		}
		// Skip MOVreg mRegCtx, X0  (mRegCtx unchanged in self-call)
		asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)
		ec.emitBoxedEntryShapeGuards()
		asm.B(ec.entryBlockLabel())
	}

	// R129: numeric entry + pass-2 body are emitted AFTER epilogue +
	// deferredResumes via emitNumericBody() (called from Compile).

	// --- Direct epilogue for BLR callers ---
	// Return path when CallMode == 1 in emitReturn. Uses the same frame
	// restore as normal epilogue since the direct entry uses a full frame.
	asm.Label("t2_direct_epilogue")
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.useFPR {
		asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
		asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
	}
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	asm.RET()

	if ec.typedSelfABI.Eligible {
		if ec.typedSelfABI.Return == SpecializedABIReturnRawInt {
			ec.emitTypedSelfRawIntReturnEpilogue()
		}
		ec.emitTypedSelfReturnEpilogue()
		ec.emitTypedSelfEntry()
	}

	if ec.numericParamCount > 0 && ec.fn != nil && ec.fn.Proto != nil {
		asm.Label("num_epilogue")
		asm.MOVimm16(jit.X16, 0)
		asm.LDP(jit.X29, jit.X30, jit.SP, 0)
		asm.ADDimm(jit.SP, jit.SP, uint16(numericSelfEntryFrameSize))
		asm.RET()

		asm.Label("num_deopt_epilogue")
		asm.LDR(jit.X16, mRegCtx, execCtxOffExitCode)
		asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
		asm.LDP(jit.X29, jit.X30, jit.SP, 0)
		asm.ADDimm(jit.SP, jit.SP, uint16(numericSelfEntryFrameSize))
		asm.RET()
	}
}

// emitBlock emits ARM64 code for one basic block.
func (ec *emitContext) emitBlock(block *Block) {
	ec.asm.Label(ec.blockLabelFor(block))
	ec.currentBlockID = block.ID
	typedParamLoads := ec.typedSelfEntryParamLoads(block)
	typedParamLabelEmitted := false
	if typedParamLoads != nil && len(typedParamLoads) == 0 {
		ec.asm.Label(ec.typedSelfAfterParamsLabel())
		typedParamLabelEmitted = true
	}

	isLoopBlock := ec.loop != nil && ec.loop.loopBlocks[block.ID]
	isHeader := ec.loop != nil && ec.loop.loopHeaders[block.ID]

	// Reset active register set for this block.
	ec.activeRegs = make(map[int]bool)
	ec.resetValueReprs()
	ec.activeFPRegs = make(map[int]bool)
	// Seed shape/table verification from the sole predecessor's outgoing state.
	// Only safe when the block has exactly one predecessor — at merge points
	// (multiple preds), different paths may have different table mutations,
	// so we conservatively reset. Loop headers also reset (back-edge may
	// have mutated tables). Single-pred propagation captures the main win:
	// pre-header → body and sequential blocks within a loop.
	// R100: restrict multi-pred merge (R95) to single-pred only — the
	// multi-pred merge showed no measurable benefit and may have
	// contributed to the sort regression (though that's unconfirmed).
	if !isHeader && len(block.Preds) == 1 {
		predID := block.Preds[0].ID
		// Seed from the single predecessor's out-state.
		if m, ok := ec.blockOutShapes[predID]; ok {
			ec.shapeVerified = make(map[int]uint32, len(m))
			for k, v := range m {
				ec.shapeVerified[k] = v
			}
		} else {
			ec.shapeVerified = make(map[int]uint32)
		}
		if m, ok := ec.blockOutTables[predID]; ok {
			ec.tableVerified = make(map[int]bool, len(m))
			for k, v := range m {
				ec.tableVerified[k] = v
			}
		} else {
			ec.tableVerified = make(map[int]bool)
		}
		if m, ok := ec.blockOutKinds[predID]; ok {
			ec.kindVerified = make(map[int]uint16, len(m))
			for k, v := range m {
				ec.kindVerified[k] = v
			}
		} else {
			ec.kindVerified = make(map[int]uint16)
		}
		if m, ok := ec.blockOutKeysDirty[predID]; ok {
			ec.keysDirtyWritten = make(map[int]bool, len(m))
			for k, v := range m {
				ec.keysDirtyWritten[k] = v
			}
		} else {
			ec.keysDirtyWritten = make(map[int]bool)
		}
	} else {
		ec.shapeVerified = make(map[int]uint32)
		ec.tableVerified = make(map[int]bool)
		ec.kindVerified = make(map[int]uint16)
		ec.keysDirtyWritten = make(map[int]bool)
	}
	ec.tableArrayBoundedKeys = make(map[tableArrayBoundKey]bool)
	ec.seedEntryShapeGuardState(block)
	// R44: reset DenseMatrix verification at every block boundary. Cross-
	// block propagation isn't critical for matmul's inner-k loop (k-loop
	// body is one block) and complicates merge semantics; conservatively
	// reset.
	ec.dmVerified = make(map[int]bool)
	ec.invalidateFieldSvalsCache()

	if isLoopBlock && !isHeader && ec.safeHeaderRegs != nil {
		// Non-header loop block: activate SAFE registers from the innermost
		// enclosing loop header. Only registers that are NOT clobbered by
		// any non-header block in the loop body are activated. This prevents
		// stale register assumptions in nested or complex loop bodies.
		if innerHeader, ok := ec.loop.blockInnerHeader[block.ID]; ok {
			if hdrRegs, ok := ec.safeHeaderRegs[innerHeader]; ok {
				for _, entry := range hdrRegs {
					ec.activeRegs[entry.ValueID] = true
					if entry.IsRawInt {
						ec.setValueRepr(entry.ValueID, valueReprRawInt)
					}
					if entry.IsRawTablePtr {
						ec.setValueRepr(entry.ValueID, valueReprRawTablePtr)
					}
					if entry.IsRawDataPtr {
						ec.setValueRepr(entry.ValueID, valueReprRawDataPtr)
					}
				}
			}
		}
	}
	if isLoopBlock && ec.safeHeaderFPRegs != nil {
		// Activate every safe enclosing loop-header FPR value whose register
		// allocation is region-pinned across this block. This extends the old
		// innermost-only model to nested numeric regions without assuming a
		// global register allocator.
		ec.activateLoopHeaderFPRs(block.ID)
	}
	if ec.rawIntBlockCarry && !isHeader && len(block.Preds) == 1 {
		ec.seedSinglePredRawIntRegs(block)
	}
	if isLoopBlock && ec.loopInvariantGPRs != nil {
		ec.activateLoopInvariantGPRs(block.ID)
	}
	if isLoopBlock && ec.loopInvariantFPRs != nil {
		ec.activateLoopInvariantFPRs(block.ID)
	}

	// Phi values are active at block entry (their registers were loaded
	// by emitPhiMoves from the predecessor). When a phi's register
	// conflicts with a loopHeaderRegs value, invalidate the header value.
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if pr, ok := ec.alloc.ValueRegs[instr.ID]; ok {
			if pr.IsFloat {
				// FPR phi: activated by emitPhiMoves which delivers raw float.
				ec.invalidateFPReg(pr.Reg, instr.ID)
				ec.activeFPRegs[instr.ID] = true
				ec.setValueRepr(instr.ID, valueReprRawFloat)
			} else {
				// Invalidate any header reg value that shares this register.
				ec.invalidateReg(pr.Reg, instr.ID)
				ec.activeRegs[instr.ID] = true
				// Loop header phis: mark int-typed phis as raw int.
				// emitPhiMoves delivers raw ints to loop header phis from
				// both the initial entry (unboxing) and back-edge (raw transfer).
				if isHeader && instr.Type == TypeInt {
					ec.setValueRepr(instr.ID, valueReprRawInt)
				}
			}
		}
	}

	for _, instr := range block.Instrs {
		ec.emitInstr(instr, block)
		ec.deactivateDeadAfter(instr)
		if typedParamLoads != nil && !typedParamLabelEmitted && instr.Op == OpLoadSlot {
			delete(typedParamLoads, int(instr.Aux))
			if len(typedParamLoads) == 0 {
				ec.asm.Label(ec.typedSelfAfterParamsLabel())
				typedParamLabelEmitted = true
			}
		}
	}

	// Save outgoing shape/table state for single-predecessor propagation.
	outShapes := make(map[int]uint32, len(ec.shapeVerified))
	for k, v := range ec.shapeVerified {
		outShapes[k] = v
	}
	ec.blockOutShapes[block.ID] = outShapes
	outTables := make(map[int]bool, len(ec.tableVerified))
	for k, v := range ec.tableVerified {
		outTables[k] = v
	}
	ec.blockOutTables[block.ID] = outTables
	outKinds := make(map[int]uint16, len(ec.kindVerified))
	for k, v := range ec.kindVerified {
		outKinds[k] = v
	}
	ec.blockOutKinds[block.ID] = outKinds
	outKD := make(map[int]bool, len(ec.keysDirtyWritten))
	for k, v := range ec.keysDirtyWritten {
		outKD[k] = v
	}
	ec.blockOutKeysDirty[block.ID] = outKD

	outRaw := make(map[int]loopRegEntry)
	for valueID := range ec.activeRegs {
		repr := ec.valueReprOf(valueID)
		if repr != valueReprRawInt && repr != valueReprRawTablePtr && repr != valueReprRawDataPtr {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		outRaw[pr.Reg] = loopRegEntry{
			ValueID:       valueID,
			IsRawInt:      repr == valueReprRawInt,
			IsRawTablePtr: repr == valueReprRawTablePtr,
			IsRawDataPtr:  repr == valueReprRawDataPtr,
		}
	}
	ec.blockOutRawIntRegs[block.ID] = outRaw
}

func (ec *emitContext) seedSinglePredRawIntRegs(block *Block) {
	if ec == nil || block == nil || len(block.Preds) != 1 {
		return
	}
	predID := block.Preds[0].ID
	predOut := ec.blockOutRawIntRegs[predID]
	if len(predOut) == 0 {
		return
	}
	liveIn := ec.blockLiveIn[block.ID]
	if len(liveIn) == 0 {
		return
	}
	regs := make([]int, 0, len(predOut))
	for reg := range predOut {
		regs = append(regs, reg)
	}
	sort.Ints(regs)
	for _, reg := range regs {
		entry := predOut[reg]
		if (!entry.IsRawInt && !entry.IsRawTablePtr && !entry.IsRawDataPtr) || !liveIn[entry.ValueID] {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[entry.ValueID]
		if !ok || pr.IsFloat || pr.Reg != reg {
			continue
		}
		ec.invalidateReg(reg, entry.ValueID)
		ec.activeRegs[entry.ValueID] = true
		if entry.IsRawInt {
			ec.setValueRepr(entry.ValueID, valueReprRawInt)
		}
		if entry.IsRawTablePtr {
			ec.setValueRepr(entry.ValueID, valueReprRawTablePtr)
		}
		if entry.IsRawDataPtr {
			ec.setValueRepr(entry.ValueID, valueReprRawDataPtr)
		}
	}
}

func (ec *emitContext) deactivateDeadAfter(instr *Instr) {
	if ec == nil || instr == nil {
		return
	}
	liveAfter := ec.instrLiveAfter[instr.ID]
	for valueID := range ec.activeRegs {
		if !liveAfter[valueID] {
			delete(ec.activeRegs, valueID)
			ec.clearValueRepr(valueID)
		}
	}
	for valueID := range ec.activeFPRegs {
		if !liveAfter[valueID] {
			delete(ec.activeFPRegs, valueID)
			ec.clearValueRepr(valueID)
		}
	}
}

// merge helpers moved to emit_merge.go (R96, file-size hygiene).
