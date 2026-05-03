//go:build darwin && arm64

package methodjit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestWarmDump_ProductionRunArtifacts(t *testing.T) {
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}

a := sum(10)
b := sum(20)
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	outDir := t.TempDir()
	if err := tm.EnableWarmDump(outDir, "sum"); err != nil {
		t.Fatalf("EnableWarmDump: %v", err)
	}
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := tm.WriteWarmDump(top); err != nil {
		t.Fatalf("WriteWarmDump: %v", err)
	}

	sumProto := findProtoByName(top, "sum")
	if sumProto == nil {
		t.Fatal("sum proto not found")
	}
	if sumProto.EnteredTier2 == 0 {
		t.Fatal("sum did not enter Tier 2; test did not exercise warm production dump")
	}

	required := []string{
		"manifest.json",
		"jit-symbols.txt",
		"sum.status.json",
		"sum.feedback.txt",
		"sum.ir.before.txt",
		"sum.ir.after.txt",
		"sum.regalloc.txt",
		"sum.loops.txt",
		"sum.bin",
		"sum.asm.txt",
		"sum.sourcemap.json",
		"sum.pcmap.json",
		"pcmap.json",
	}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected warm dump file %s: %v", name, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest warmDumpManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest.Protos) != 1 {
		t.Fatalf("manifest protos = %d, want 1", len(manifest.Protos))
	}
	got := manifest.Protos[0]
	if got.Name != "sum" || got.Status != "entered" || !got.Compiled || !got.Entered || got.Failed {
		t.Fatalf("unexpected status: %+v", got)
	}
	if got.Feedback.Slots == 0 || got.Files["feedback"] == "" {
		t.Fatalf("feedback summary missing: %+v", got.Feedback)
	}
	if got.InsnCount == 0 || got.CodeBytes == 0 {
		t.Fatalf("missing code stats: insns=%d bytes=%d", got.InsnCount, got.CodeBytes)
	}
	if len(got.LoopDiagnostics) == 0 || got.Files["loops"] == "" {
		t.Fatalf("loop diagnostics missing: %+v", got.LoopDiagnostics)
	}
	if got.CodeStart == "" || got.CodeEnd == "" || got.Files["sourcemap"] == "" || got.Files["pcmap"] == "" {
		t.Fatalf("PC/source map metadata missing: %+v", got)
	}

	pcMapData, err := os.ReadFile(filepath.Join(outDir, "pcmap.json"))
	if err != nil {
		t.Fatalf("read PC map: %v", err)
	}
	var pcMap warmDumpPCMap
	if err := json.Unmarshal(pcMapData, &pcMap); err != nil {
		t.Fatalf("unmarshal PC map: %v", err)
	}
	if pcMap.Version != 1 || len(pcMap.Functions) != 1 {
		t.Fatalf("unexpected PC map header: %+v", pcMap)
	}
	fn := pcMap.Functions[0]
	if fn.Name != "sum" || fn.CodeBase == "" || fn.CodeEnd == "" || fn.CodeBytes == 0 || len(fn.Ranges) == 0 {
		t.Fatalf("unexpected PC map function: %+v", fn)
	}
	foundIRRange := false
	for _, r := range fn.Ranges {
		if r.PCStart == "" || r.PCEnd == "" {
			t.Fatalf("PC range missing absolute address: %+v", r)
		}
		if r.CodeStart >= 0 && r.CodeEnd > r.CodeStart && r.InstrID > 0 && r.IROp != "" {
			foundIRRange = true
		}
	}
	if !foundIRRange {
		t.Fatalf("PC map has no usable IR ranges: %+v", fn.Ranges)
	}

	symbols, err := os.ReadFile(filepath.Join(outDir, "jit-symbols.txt"))
	if err != nil {
		t.Fatalf("read JIT symbols: %v", err)
	}
	if len(symbols) == 0 ||
		!strings.Contains(string(symbols), "gscript_jit::sum") ||
		!strings.Contains(string(symbols), "proto=sum") ||
		!strings.Contains(string(symbols), "ir=") ||
		!strings.Contains(string(symbols), "bcop=") {
		t.Fatalf("JIT symbols missing expected metadata:\n%s", string(symbols))
	}
}
