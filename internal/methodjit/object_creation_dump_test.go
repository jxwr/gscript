//go:build darwin && arm64

// Regression witness for R35; expected to shrink when R36 fix lands.
// Captures object_creation ARM64 instruction counts from the production
// Tier 2 pipeline (BuildGraph → RunTier2Pipeline w/ InlineGlobals →
// AllocateRegisters → Compile).
//
// Baseline values: new_vec3 from authoritative-context.json (2026-04-12,
// no inlining, matches production). create_and_sum and transform_chain
// measured from production pipeline WITH InlineGlobals (InlineMaxSize=80),
// which inlines new_vec3/vec3_add/vec3_scale callees and produces larger
// code than the non-inlined authoritative-context.json disasm_summary.

package methodjit

import (
	"math"
	"os"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

func TestObjectCreationDump(t *testing.T) {
	// Baseline values re-calibrated R77 after the int48 overflow check
	// was added to emitFloatBinOp's int fast path (emit_call.go fix for
	// tier2-intbinmod-correctness). Each ADD/SUB/MUL on untyped-int Values
	// now emits +3 insns (SBFX+CMP+BCond) plus a deopt prelude. Impact on
	// object_creation ~6% total, ~6% mem. Correctness trade is net positive.
	//
	// R146 re-calibration: each Tier 2 entry point now emits an
	// emitTier2EntryMark sequence (LoadImm64 heap addr + MOVimm16 + STRB,
	// ~6 insns per entry) at the head of emitPrologue and t2_direct_entry
	// so that proto.EnteredTier2 is observably set when native code runs.
	// Protos with t2_numeric_self_entry_N gain a third affected site
	// implicitly via shared prologue code; actual per-proto deltas observed:
	// create_and_sum +24 total / +8 mem, transform_chain +24/+8, new_vec3
	// +12/+2. Memory-op delta is the single STRB per entry path.
	type baseline struct {
		name     string
		totalIns int
		memIns   int
	}
	// R170: new_vec3 recalibrated after Tier2 call/return resume metadata
	// cleanup. The function still escapes its returned table, so EA behavior
	// is unchanged; only the emitted prologue/return bookkeeping moved.
	//
	// R161 EA: -61% on create_and_sum / -63% on transform_chain via
	// virtual-Phi scalar replacement that eliminates loop-carried
	// NewTable allocations entirely. new_vec3 unchanged because its
	// returned table escapes and EA correctly does not touch it.
	//
	// OverflowBoxing: create_and_sum / transform_chain lose redundant
	// raw-int overflow deopt bookkeeping on boxed numeric loop values.
	//
	// RedundantGuardElimination: create_and_sum / transform_chain drop
	// post-EA GuardType load/branch sequences once virtual field Phis keep
	// their stored value type.
	baselines := []baseline{
		{"create_and_sum", 472, 78},  // R161: was 1277/598
		{"transform_chain", 590, 85}, // R161: was 1701/816
		{"new_vec3", 228, 135},       // unchanged EA shape; codegen bookkeeping moved
	}

	// Load benchmark source.
	srcBytes, err := os.ReadFile("../../benchmarks/suite/object_creation.gs")
	if err != nil {
		t.Fatalf("read object_creation.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))

	// P3: build InlineGlobals from the proto tree so the inline pass
	// can resolve callees — matches the production tiering_manager path.
	globals := map[string]*vm.FuncProto{}
	var collectGlobals func(p *vm.FuncProto)
	collectGlobals = func(p *vm.FuncProto) {
		if p.Name != "" {
			globals[p.Name] = p
		}
		for _, sub := range p.Protos {
			collectGlobals(sub)
		}
	}
	collectGlobals(top)
	opts := &Tier2PipelineOpts{InlineGlobals: globals, InlineMaxSize: 80}

	const tolerance = 0.02

	for _, bl := range baselines {
		t.Run(bl.name, func(t *testing.T) {
			proto := findProtoByName(top, bl.name)
			if proto == nil {
				t.Fatalf("function %q not found in object_creation.gs", bl.name)
			}
			proto.EnsureFeedback()

			fn := BuildGraph(proto)
			fn, _, pipeErr := RunTier2Pipeline(fn, opts)
			if pipeErr != nil {
				t.Fatalf("pipeline error: %v", pipeErr)
			}
			fn.CarryPreheaderInvariants = true
			alloc := AllocateRegisters(fn)
			cf, compileErr := Compile(fn, alloc)
			if compileErr != nil {
				t.Fatalf("Compile error: %v", compileErr)
			}
			t.Cleanup(func() { cf.Code.Free() })

			totalInsns := cf.Code.Size() / 4

			// Count memory instructions: ARM64 Load/Store group has
			// bits[27]=1, bits[25]=0 → (insn>>25)&0x5 == 0x4.
			src := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
			memInsns := 0
			for i := 0; i+3 < len(src); i += 4 {
				insn := uint32(src[i]) | uint32(src[i+1])<<8 |
					uint32(src[i+2])<<16 | uint32(src[i+3])<<24
				op0 := (insn >> 25) & 0xF
				if (op0 & 0x5) == 0x4 {
					memInsns++
				}
			}

			t.Logf("%s: total=%d (baseline %d), mem=%d (baseline %d)",
				bl.name, totalInsns, bl.totalIns, memInsns, bl.memIns)

			// Assert ±2% tolerance on total instructions.
			if diff := math.Abs(float64(totalInsns - bl.totalIns)); diff/float64(bl.totalIns) > tolerance {
				t.Errorf("total insns %d outside ±2%% of baseline %d (diff %.1f%%)",
					totalInsns, bl.totalIns, diff/float64(bl.totalIns)*100)
			}
			// Assert ±2% tolerance on memory instructions.
			if diff := math.Abs(float64(memInsns - bl.memIns)); diff/float64(bl.memIns) > tolerance {
				t.Errorf("mem insns %d outside ±2%% of baseline %d (diff %.1f%%)",
					memInsns, bl.memIns, diff/float64(bl.memIns)*100)
			}
		})
	}
}
