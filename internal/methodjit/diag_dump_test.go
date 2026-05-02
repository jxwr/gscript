//go:build darwin && arm64

// diag_dump_test.go is the Go-side of scripts/diag.sh. It is intentionally
// written as a test so it has access to the package-private compileTop,
// findProtoByName, and unsafeCodeSlice helpers without polluting the
// public API. The shell driver invokes it with -run=TestDiagDump -args
// <benchmark> <out_dir>; the test writes one bin/ir/stats.json set per
// Tier-2-promotable proto found in the benchmark.
//
// This is not run by default CI — it's gated behind the DIAG_BENCH env
// var. Running it blindly as part of `go test ./...` would emit files
// under diag/ which we do not want cluttering a normal run.

package methodjit

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/arch/arm64/arm64asm"
)

// diagProtoStats is the per-proto summary written into stats.json.
type diagProtoStats struct {
	Name                string               `json:"name"`
	NumParams           int                  `json:"num_params"`
	MaxStack            int                  `json:"max_stack"`
	InsnCount           int                  `json:"insn_count"`
	InsnHistogram       map[string]int       `json:"insn_histogram"`
	CodeBytes           int                  `json:"code_bytes"`
	SkipReason          string               `json:"skip_reason,omitempty"`
	OptimizationRemarks []OptimizationRemark `json:"optimization_remarks,omitempty"`
}

// TestDiagDump writes diagnostic artifacts for one benchmark to disk. It
// is skipped unless DIAG_BENCH is set. The shell driver passes the
// benchmark file name via DIAG_BENCH and the output directory via DIAG_OUT.
//
// DIAG_BENCH formats accepted:
//   "sieve.gs"                       — bare basename: defaults to suite/
//   "extended/json_table_walk.gs"    — explicit subdirectory under benchmarks/
//   "variants/ack_nested_shifted.gs" — likewise
func TestDiagDump(t *testing.T) {
	benchFile := os.Getenv("DIAG_BENCH")
	if benchFile == "" {
		t.Skip("DIAG_BENCH not set — run via scripts/diag.sh")
	}
	outDir := os.Getenv("DIAG_OUT")
	if outDir == "" {
		t.Fatal("DIAG_OUT must be set alongside DIAG_BENCH")
	}

	relPath := benchFile
	if !strings.Contains(relPath, "/") {
		relPath = "suite/" + relPath
	}
	src, err := os.ReadFile("../../benchmarks/" + relPath)
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	top := compileTop(t, string(src))

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	tm := NewTieringManager()
	arts := tm.ProtoTreeDiag(top)

	var stats []diagProtoStats

	for _, art := range arts {
		if art.CompileErr != nil {
			stats = append(stats, diagProtoStats{
				Name:                art.ProtoName,
				NumParams:           art.NumParams,
				MaxStack:            art.MaxStack,
				SkipReason:          art.CompileErr.Error(),
				OptimizationRemarks: art.OptimizationRemarks,
			})
			continue
		}
		base := sanitizeName(art.ProtoName)
		if base == "" {
			base = "_anon"
		}

		// Raw ARM64 bytes.
		binPath := filepath.Join(outDir, base+".bin")
		if err := os.WriteFile(binPath, art.CompiledCode, 0o644); err != nil {
			t.Fatalf("write %s: %v", binPath, err)
		}

		// Human-readable ARM64 disasm via golang.org/x/arch/arm64/arm64asm.
		// Self-contained — no external tools required.
		asmPath := filepath.Join(outDir, base+".asm.txt")
		if err := os.WriteFile(asmPath, []byte(disasmARM64(art.CompiledCode)), 0o644); err != nil {
			t.Fatalf("write %s: %v", asmPath, err)
		}

		mapPath := filepath.Join(outDir, base+".map.json")
		mapBytes, err := json.MarshalIndent(art.SourceMap, "", "  ")
		if err != nil {
			t.Fatalf("marshal %s: %v", mapPath, err)
		}
		if err := os.WriteFile(mapPath, append(mapBytes, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", mapPath, err)
		}

		annotatedASMPath := filepath.Join(outDir, base+".asm.annotated.txt")
		if err := os.WriteFile(annotatedASMPath, []byte(disasmARM64Annotated(art.CompiledCode, art.SourceMap)), 0o644); err != nil {
			t.Fatalf("write %s: %v", annotatedASMPath, err)
		}

		// IR text — post-RunTier2Pipeline, as Print(fn) formats it.
		irPath := filepath.Join(outDir, base+".ir.txt")
		irContent := fmt.Sprintf("=== %s (numParams=%d, maxStack=%d) ===\n\n", art.ProtoName, art.NumParams, art.MaxStack)
		irContent += "--- Register allocation ---\n" + art.RegAllocMap + "\n\n"
		if len(art.IntrinsicNotes) > 0 {
			irContent += "--- Intrinsic rewrite notes ---\n"
			for _, n := range art.IntrinsicNotes {
				irContent += "  " + n + "\n"
			}
			irContent += "\n"
		}
		irContent += "--- Optimization remarks ---\n" + formatOptimizationRemarks(art.OptimizationRemarks) + "\n"
		irContent += "--- IR (after full Tier 2 pipeline) ---\n" + art.IRAfter
		if err := os.WriteFile(irPath, []byte(irContent), 0o644); err != nil {
			t.Fatalf("write %s: %v", irPath, err)
		}

		stats = append(stats, diagProtoStats{
			Name:                art.ProtoName,
			NumParams:           art.NumParams,
			MaxStack:            art.MaxStack,
			InsnCount:           art.InsnCount,
			InsnHistogram:       art.InsnHistogram,
			CodeBytes:           len(art.CompiledCode),
			OptimizationRemarks: art.OptimizationRemarks,
		})
	}

	// Sort by InsnCount descending so the hottest function is first.
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].InsnCount > stats[j].InsnCount
	})

	statsPath := filepath.Join(outDir, "stats.json")
	f, err := os.Create(statsPath)
	if err != nil {
		t.Fatalf("create %s: %v", statsPath, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	payload := map[string]any{
		"benchmark": relPath,
		"protos":    stats,
	}
	if err := enc.Encode(payload); err != nil {
		f.Close()
		t.Fatalf("encode %s: %v", statsPath, err)
	}
	f.Close()

	nonSkipped := 0
	for _, p := range stats {
		if p.SkipReason == "" {
			nonSkipped++
		}
	}
	t.Logf("wrote %d artifacts (%d protos) to %s", 4*nonSkipped, len(stats), outDir)
}

// disasmARM64 decodes a flat ARM64 code region into a human-readable
// listing: one line per instruction, with byte offset, raw hex, and
// decoded Go-syntax assembly. Uses golang.org/x/arch/arm64/arm64asm.
func disasmARM64(code []byte) string {
	var b strings.Builder
	for i := 0; i+4 <= len(code); i += 4 {
		word := binary.LittleEndian.Uint32(code[i : i+4])
		inst, err := arm64asm.Decode(code[i : i+4])
		if err != nil {
			fmt.Fprintf(&b, "%04x  %08x  .word\n", i, word)
			continue
		}
		// GoSyntax returns an assembly-like form similar to Go's internal
		// assembler. Use it rather than GNUSyntax for consistency with
		// Go-side tooling; either is readable.
		fmt.Fprintf(&b, "%04x  %08x  %s\n", i, word, arm64asm.GoSyntax(inst, 0, nil, nil))
	}
	return b.String()
}

// disasmARM64Annotated emits the same listing as disasmARM64 with source/IR
// comments inserted at instruction ranges recorded by the Tier 2 emitter.
func disasmARM64Annotated(code []byte, sourceMap []IRASMMapEntry) string {
	byStart := make(map[int][]IRASMMapEntry)
	for _, entry := range sourceMap {
		if entry.CodeStart < 0 {
			continue
		}
		byStart[entry.CodeStart] = append(byStart[entry.CodeStart], entry)
	}
	for off := range byStart {
		sort.Slice(byStart[off], func(i, j int) bool {
			if byStart[off][i].InstrID != byStart[off][j].InstrID {
				return byStart[off][i].InstrID < byStart[off][j].InstrID
			}
			return byStart[off][i].Pass < byStart[off][j].Pass
		})
	}

	var b strings.Builder
	for i := 0; i+4 <= len(code); i += 4 {
		if entries := byStart[i]; len(entries) > 0 {
			for _, entry := range entries {
				fmt.Fprintf(&b, "; line=%d pc=%d bc=%s ir=v%d %s B%d pass=%s code=[0x%04x,0x%04x)\n",
					entry.SourceLine, entry.BytecodePC, entry.BytecodeOp,
					entry.InstrID, entry.IROp, entry.BlockID, entry.Pass,
					entry.CodeStart, entry.CodeEnd)
			}
		}
		word := binary.LittleEndian.Uint32(code[i : i+4])
		inst, err := arm64asm.Decode(code[i : i+4])
		if err != nil {
			fmt.Fprintf(&b, "%04x  %08x  .word\n", i, word)
			continue
		}
		fmt.Fprintf(&b, "%04x  %08x  %s\n", i, word, arm64asm.GoSyntax(inst, 0, nil, nil))
	}
	return b.String()
}

// sanitizeName turns a proto name into a filesystem-safe base name.
func sanitizeName(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.', r == '<', r == '>':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
