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
	// P2: baseline values from authoritative-context.json (2026-04-12).
	type baseline struct {
		name     string
		totalIns int
		memIns   int
	}
	baselines := []baseline{
		{"create_and_sum", 1181, 558},
		{"transform_chain", 1572, 758},
		{"new_vec3", 208, 129},
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
