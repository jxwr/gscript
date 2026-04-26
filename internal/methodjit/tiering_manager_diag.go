//go:build darwin && arm64

// tiering_manager_diag.go exposes a diagnostic entry point onto the exact
// same Tier 2 compile pipeline used in production. It exists so that tools
// under scripts/diag.sh can dump IR + ARM64 bytes + instruction stats
// without drifting from production behaviour — the drift that caused R31
// and R32 to waste rounds measuring a silently-divergent parallel pipeline.
//
// The entire dissertation is a single rule: diagnostics must call
// (*TieringManager).compileTier2Pipeline, which is the same function
// production compileTier2 calls. That is enforced by construction here
// (CompileForDiagnostics is a thin wrapper) and by a bit-identity parity
// test (TestDiag_ProductionParity) asserting the ARM64 Code bytes are equal
// regardless of whether the caller took the production path or the diag
// path.
//
// See CLAUDE.md rule 5 and docs-internal/workflow-v5-plan.md.

package methodjit

import (
	"encoding/binary"
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// Tier2Trace records intermediate artifacts produced during a Tier 2
// compile. It is optional — production compileTier2 passes nil. The
// diagnostic path passes a non-nil trace to capture the artifacts for
// subsequent inspection.
//
// IRBefore and IRAfter are rendered by Print(fn), which is the same
// renderer diagnose.go has used since the project's early diagnostic
// infrastructure. IntrinsicNotes records the rewrite log from IntrinsicPass
// and is non-nil only when the pass replaced ops that Tier 1 would execute
// differently.
type Tier2Trace struct {
	IRBefore            string
	IRAfter             string
	IntrinsicNotes      []string
	OptimizationRemarks []OptimizationRemark
	RegAllocMap         string
}

// DiagArtifact is the full diagnostic payload for one Tier 2 compile.
// CompiledCode is a defensive copy of the mmap'd code region — the caller
// can retain it after the underlying CompiledFunction is freed.
type DiagArtifact struct {
	ProtoName           string
	NumParams           int
	MaxStack            int
	IRBefore            string
	IRAfter             string
	IntrinsicNotes      []string
	OptimizationRemarks []OptimizationRemark
	RegAllocMap         string
	CompiledCode        []byte         // copy of cf.Code bytes
	InsnCount           int            // total ARM64 instructions
	InsnHistogram       map[string]int // class -> count
	DirectEntryOff      int
	NumSpills           int
	CompileErr          error
}

// CompileForDiagnostics runs the production Tier 2 compile pipeline on a
// single FuncProto and returns a full diagnostic artifact. It shares
// compileTier2Pipeline with the production compileTier2 path — there is no
// parallel pipeline, no alternate pass order, no skipped step. The only
// divergence is that production updates TieringManager bookkeeping
// (tier2Attempts, tier2FailReason, debug logging) while this method leaves
// those fields untouched.
//
// The returned DiagArtifact.CompiledCode is a fresh byte slice copied from
// the mmap'd region — safe to retain after the underlying CompiledFunction
// is freed by the caller via cf.Code.Free().
func (tm *TieringManager) CompileForDiagnostics(proto *vm.FuncProto) (*DiagArtifact, error) {
	trace := &Tier2Trace{}
	cf, err := tm.compileTier2Pipeline(proto, trace)

	art := &DiagArtifact{
		ProtoName:           proto.Name,
		NumParams:           proto.NumParams,
		MaxStack:            proto.MaxStack,
		IRBefore:            trace.IRBefore,
		IRAfter:             trace.IRAfter,
		IntrinsicNotes:      trace.IntrinsicNotes,
		OptimizationRemarks: trace.OptimizationRemarks,
		RegAllocMap:         trace.RegAllocMap,
		CompileErr:          err,
	}

	if cf == nil {
		return art, err
	}

	defer cf.Code.Free()

	size := cf.Code.Size()
	art.CompiledCode = make([]byte, size)
	src := unsafeCodeSlice(cf)
	copy(art.CompiledCode, src)
	art.DirectEntryOff = cf.DirectEntryOffset
	art.NumSpills = cf.NumSpills
	art.InsnCount, art.InsnHistogram = classifyARM64(art.CompiledCode)

	return art, nil
}

// unsafeCodeSlice exposes the mmap'd code region as a byte slice for
// copying. Defined in tiering_manager_diag_unsafe.go to keep unsafe out of
// the main file.

// classifyARM64 walks a flat ARM64 code region (4-byte little-endian
// instructions) and returns (total_count, class_histogram). The
// classification is coarse but enough to spot the obvious shapes: how many
// loads/stores vs arithmetic vs branches vs FP ops live in a function.
//
// We decode by top-level encoding class from the ARM architecture reference
// manual §C4.1 "Top-level encodings for A64":
//
//	bit 28-25:
//	  0000       Reserved / SVE (we ignore)
//	  100x/101x  Data processing - immediate
//	  101x       Branches, exception, system
//	  x1x0       Loads and stores
//	  x101       Data processing - register
//	  x111       Data processing - SIMD and floating point
//
// A full disassembler is out of scope; for human-readable text the
// diag shell script shells out to `otool -tV` on the code bytes dumped to
// /tmp.
func classifyARM64(code []byte) (int, map[string]int) {
	hist := make(map[string]int)
	count := 0
	for i := 0; i+4 <= len(code); i += 4 {
		insn := binary.LittleEndian.Uint32(code[i : i+4])
		count++
		class := arm64Class(insn)
		hist[class]++
	}
	return count, hist
}

// arm64Class returns a short class label for an ARM64 instruction word.
// Labels are stable strings meant to be aggregated into a histogram; they
// are coarser than a real disassembler but sufficient for "where is the
// cost" questions during diagnostics.
func arm64Class(insn uint32) string {
	op0 := (insn >> 25) & 0xF // bits 28..25
	switch {
	case op0&0b1110 == 0b1000:
		// 100x — Data processing - immediate
		return "dpi"
	case op0&0b1110 == 0b1010:
		// 101x — Branch, exception, system
		return "branch"
	case op0&0b0101 == 0b0100:
		// x1x0 — Loads and stores
		// Distinguish load vs store via bit 22 (L/S field in most load/store
		// encodings). This is correct for LDR/STR immediate, LDR/STR
		// register, LDP/STP, and load/store exclusive; approximate but
		// good enough for histogram purposes.
		if (insn>>22)&1 == 1 {
			return "load"
		}
		return "store"
	case op0&0b0111 == 0b0101:
		// x101 — Data processing - register
		return "dpr"
	case op0&0b0111 == 0b0111:
		// x111 — Data processing - SIMD and FP
		return "fp"
	default:
		return "other"
	}
}

// ProtoTreeDiag walks a FuncProto tree depth-first and returns a diagnostic
// artifact for every proto that can be compiled to Tier 2. Protos that
// fail compilation (unpromotable, validation error, staying-at-tier-1) are
// included with CompileErr set and other fields zero — the caller can
// report the reason without losing the proto from the list.
func (tm *TieringManager) ProtoTreeDiag(top *vm.FuncProto) []*DiagArtifact {
	var out []*DiagArtifact
	var walk func(p *vm.FuncProto)
	walk = func(p *vm.FuncProto) {
		art, _ := tm.CompileForDiagnostics(p)
		if art == nil {
			art = &DiagArtifact{ProtoName: p.Name, CompileErr: fmt.Errorf("nil artifact")}
		}
		out = append(out, art)
		for _, sub := range p.Protos {
			walk(sub)
		}
	}
	walk(top)
	return out
}
