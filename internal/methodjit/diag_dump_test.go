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
	Name          string         `json:"name"`
	NumParams     int            `json:"num_params"`
	MaxStack      int            `json:"max_stack"`
	InsnCount     int            `json:"insn_count"`
	InsnHistogram map[string]int `json:"insn_histogram"`
	CodeBytes     int            `json:"code_bytes"`
	SkipReason    string         `json:"skip_reason,omitempty"`
}

// TestDiagDump writes diagnostic artifacts for one benchmark to disk. It
// is skipped unless DIAG_BENCH is set. The shell driver passes the
// benchmark file name (e.g. "sieve.gs") via DIAG_BENCH and the output
// directory via DIAG_OUT.
func TestDiagDump(t *testing.T) {
	benchFile := os.Getenv("DIAG_BENCH")
	if benchFile == "" {
		t.Skip("DIAG_BENCH not set — run via scripts/diag.sh")
	}
	outDir := os.Getenv("DIAG_OUT")
	if outDir == "" {
		t.Fatal("DIAG_OUT must be set alongside DIAG_BENCH")
	}

	src, err := os.ReadFile("../../benchmarks/suite/" + benchFile)
	if err != nil {
		t.Fatalf("read %s: %v", benchFile, err)
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
				Name:       art.ProtoName,
				NumParams:  art.NumParams,
				MaxStack:   art.MaxStack,
				SkipReason: art.CompileErr.Error(),
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
		irContent += "--- IR (after full Tier 2 pipeline) ---\n" + art.IRAfter
		if err := os.WriteFile(irPath, []byte(irContent), 0o644); err != nil {
			t.Fatalf("write %s: %v", irPath, err)
		}

		stats = append(stats, diagProtoStats{
			Name:          art.ProtoName,
			NumParams:     art.NumParams,
			MaxStack:      art.MaxStack,
			InsnCount:     art.InsnCount,
			InsnHistogram: art.InsnHistogram,
			CodeBytes:     len(art.CompiledCode),
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
		"benchmark": benchFile,
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
	t.Logf("wrote %d artifacts (%d protos) to %s", 2*nonSkipped, len(stats), outDir)
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
