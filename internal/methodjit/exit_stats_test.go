//go:build darwin && arm64

package methodjit

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestExitStatsRecordsRealTier2OpExit(t *testing.T) {
	src := `
func bool_len(t) {
    return #t
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	boolLenProto := findProtoByName(top, "bool_len")
	if boolLenProto == nil {
		t.Fatal("bool_len proto not found")
	}
	if err := tm.CompileTier2(boolLenProto); err != nil {
		t.Fatalf("CompileTier2(bool_len): %v", err)
	}

	tbl := runtime.NewTable()
	tbl.RawSetInt(1, runtime.BoolValue(true))
	fn := v.GetGlobal("bool_len")
	results, err := v.CallValue(fn, []runtime.Value{runtime.TableValue(tbl)})
	if err != nil {
		t.Fatalf("CallValue(bool_len): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 1 {
		t.Fatalf("bool_len result=%v, want 1", results)
	}

	snap := tm.ExitStats()
	if snap.Total == 0 {
		t.Fatal("expected at least one Tier 2 exit")
	}
	if snap.ByExitCode["ExitOpExit"] == 0 {
		t.Fatalf("ExitOpExit count missing: %#v", snap.ByExitCode)
	}
	var found bool
	for _, site := range snap.Sites {
		if site.Proto == "bool_len" && site.ExitName == "ExitOpExit" && site.Reason == "Len" {
			found = true
			if site.Count != 1 {
				t.Fatalf("bool_len Len count=%d, want 1", site.Count)
			}
			if site.PC < 0 {
				t.Fatalf("bool_len Len pc=%d, want source PC", site.PC)
			}
		}
	}
	if !found {
		t.Fatalf("missing bool_len Len op-exit site in %#v", snap.Sites)
	}
}

func TestExitStatsAggregatesByProtoCodeSiteAndReason(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "hot"}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			7: {PC: 3, Op: "Len", Reason: "Len"},
			9: {PC: 5, Op: "GetField", Reason: "GetField"},
		},
	}

	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitOpExit, OpExitID: 7, OpExitOp: int64(OpLen)})
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitOpExit, OpExitID: 7, OpExitOp: int64(OpLen)})
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitTableExit, TableExitID: 9, TableOp: TableOpGetField})

	snap := tm.ExitStats()
	if snap.Total != 3 {
		t.Fatalf("total=%d, want 3", snap.Total)
	}
	if snap.ByExitCode["ExitOpExit"] != 2 || snap.ByExitCode["ExitTableExit"] != 1 {
		t.Fatalf("by_exit_code=%#v", snap.ByExitCode)
	}
	if len(snap.Sites) != 2 {
		t.Fatalf("sites=%d, want 2: %#v", len(snap.Sites), snap.Sites)
	}
	if snap.Sites[0].Count != 2 || snap.Sites[0].Proto != "hot" || snap.Sites[0].PC != 3 || snap.Sites[0].Reason != "Len" {
		t.Fatalf("top site=%#v", snap.Sites[0])
	}

	var buf bytes.Buffer
	tm.WriteExitStatsText(&buf)
	text := buf.String()
	if !strings.Contains(text, "Tier 2 Exit Profile:") || !strings.Contains(text, "proto=hot exit=ExitOpExit id=7 pc=3 reason=Len") {
		t.Fatalf("unexpected text output:\n%s", text)
	}
}
