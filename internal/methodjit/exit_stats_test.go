//go:build darwin && arm64

package methodjit

import (
	"bytes"
	"strings"
	"testing"
	"time"

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

func TestTier2PerfStatsRecordsRowsAndText(t *testing.T) {
	tm := NewTieringManager()
	if tm.Tier2PerfStats().Enabled {
		t.Fatal("perf stats enabled by default")
	}

	tm.EnableTier2PerfStats()
	tm.perfStats.record(perfTier2OpExit, 10*time.Nanosecond)
	tm.perfStats.record(perfTier2OpExit, 30*time.Nanosecond)
	tm.perfStats.record(perfTier2ExitResume, 5*time.Nanosecond)

	snap := tm.Tier2PerfStats()
	if !snap.Enabled {
		t.Fatal("perf stats snapshot is not enabled")
	}
	opRow := tier2PerfRowByName(snap, perfTier2OpExit)
	if opRow.Count != 2 || opRow.Nanos != 40 || opRow.AvgNanos != 20 {
		t.Fatalf("op row=%#v, want count=2 nanos=40 avg=20", opRow)
	}
	resumeRow := tier2PerfRowByName(snap, perfTier2ExitResume)
	if resumeRow.Count != 1 || resumeRow.Nanos != 5 {
		t.Fatalf("resume row=%#v, want count=1 nanos=5", resumeRow)
	}

	var buf bytes.Buffer
	tm.WriteTier2PerfStatsText(&buf)
	text := buf.String()
	if !strings.Contains(text, "Tier 2 Performance Diagnostics:") ||
		!strings.Contains(text, "tier2_op_exit: count=2 total=40ns avg=20ns") {
		t.Fatalf("unexpected perf stats text:\n%s", text)
	}
}

func TestTier2PerfStatsIncludesCompiledBlockCounters(t *testing.T) {
	tm := NewTieringManager()
	tm.EnableTier2PerfStats()
	proto := &vm.FuncProto{Name: "hot_block"}
	tm.markTier2Compiled(proto, &CompiledFunction{
		Tier2BlockCounters: []uint64{0, 42},
		Tier2BlockCounterMeta: []Tier2BlockCounterMeta{
			{Proto: "hot_block", BlockID: 1, InstrIDs: []int{1}, Ops: []string{"LoadSlot"}},
			{Proto: "hot_block", BlockID: 2, InstrIDs: []int{2, 3}, Ops: []string{"AddInt", "Return"}},
		},
	})

	snap := tm.Tier2PerfStats()
	if len(snap.Blocks) != 1 {
		t.Fatalf("blocks=%#v, want one non-zero block row", snap.Blocks)
	}
	row := snap.Blocks[0]
	if row.Proto != "hot_block" || row.BlockID != 2 || row.Count != 42 ||
		len(row.Ops) != 2 || row.Ops[0] != "AddInt" || row.Ops[1] != "Return" {
		t.Fatalf("unexpected block row: %#v", row)
	}
}

func TestTier2PerfStatsRecordsRealTier2OpExit(t *testing.T) {
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
	tm.EnableTier2PerfStats()
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

	snap := tm.Tier2PerfStats()
	if row := tier2PerfRowByName(snap, perfTier2OpExit); row.Count != 1 {
		t.Fatalf("op-exit perf row=%#v, want count=1", row)
	}
	if row := tier2PerfRowByName(snap, perfTier2ExitResume); row.Count != 1 {
		t.Fatalf("exit-resume perf row=%#v, want count=1", row)
	}
	if row := tier2PerfRowByName(snap, perfTier2NativeExecution); row.Count < 2 {
		t.Fatalf("native execution perf row=%#v, want at least 2 calls", row)
	}
}

func tier2PerfRowByName(snap Tier2PerfStatsSnapshot, name string) Tier2PerfStatsRow {
	for _, row := range snap.Rows {
		if row.Name == name {
			return row
		}
	}
	return Tier2PerfStatsRow{}
}

func TestExitStatsAggregatesByProtoCodeSiteAndReason(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "hot", Code: make([]uint32, 4)}
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

func TestExitProfileQueuesRecompileWhenFeedbackMatures(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "hot", Code: make([]uint32, 4)}
	proto.EnsureFeedback()
	proto.TableKeyFeedback[3].Count = 2
	proto.TableKeyFeedback[3].ShapeID = 7
	proto.TableKeyFeedback[3].FieldIdx = 1
	proto.TableKeyFeedback[3].FieldIdxSeen = true
	proto.TableKeyFeedback[3].StringKey = "x"
	proto.TableKeyFeedback[3].StringKeySeen = true
	proto.TableKeyFeedback[3].ValueType = vm.FBInt
	proto.TableKeyFeedback[3].AccessKind = vm.TableAccessKindGet
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 0,
		},
		ExitSites: map[int]ExitSiteMeta{
			9: {PC: 3, Op: "GetTable", Reason: "GetTable"},
		},
	}
	tm.ensureTierStateStore()
	tm.tierState.markCompiled(proto, cf)
	proto.Tier2Promoted = true
	proto.DirectEntryPtr = 123

	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitTableExit, TableExitID: 9, TableOp: TableOpGetTable})
	if _, ok := tm.recompileQueue.take(proto); ok {
		t.Fatal("first exit should not queue recompile")
	}
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitTableExit, TableExitID: 9, TableOp: TableOpGetTable})
	req, ok := tm.recompileQueue.take(proto)
	if !ok {
		t.Fatal("second hot exit with matured feedback should queue recompile")
	}
	if req.Site.PC != 3 || req.Site.Count != 2 || req.Site.ExitName != "ExitTableExit" {
		t.Fatalf("unexpected queued request: %+v", req)
	}
	if !req.Site.QueuedRecompile || req.Site.RefreshVersionHash == "" || req.Site.RefreshGuardDelta <= 0 {
		t.Fatalf("queued request missing refresh diagnostics: %+v", req.Site)
	}
	if _, ok := tm.tier2CompiledFor(proto); ok {
		t.Fatal("queued recompile should clear stale compiled install")
	}
	if proto.Tier2Promoted || proto.DirectEntryPtr != 0 {
		t.Fatalf("queued recompile left stale entry visible: promoted=%v direct=%#x",
			proto.Tier2Promoted, proto.DirectEntryPtr)
	}
	snap := tm.exitProfile.snapshot()
	if len(snap.Sites) != 1 || !snap.Sites[0].QueuedRecompile || snap.Sites[0].RefreshGuardDelta <= 0 {
		t.Fatalf("exit profile missing refresh diagnostics: %+v", snap)
	}
}

func TestExitProfileUsesActiveSpeculationProfileForRefresh(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "suppressed_hot", Code: make([]uint32, 4)}
	proto.EnsureFeedback()
	proto.Feedback[3].Result = vm.FBInt
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 0,
		},
		ExitSites: map[int]ExitSiteMeta{
			9: {PC: 3, Op: "GetTable", Reason: "GetTable"},
		},
	}
	tm.suppressTier2GuardKind(proto, 3, "GuardType")

	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitTableExit, TableExitID: 9, TableOp: TableOpGetTable})
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitTableExit, TableExitID: 9, TableOp: TableOpGetTable})
	if _, ok := tm.recompileQueue.take(proto); ok {
		t.Fatal("suppressed guard should not queue refresh through inactive profile")
	}
}

func TestExitProfileMarksSuppressedGuardExit(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "guard_exit", Code: make([]uint32, 4)}
	proto.EnsureFeedback()
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 1,
		},
		ExitSites: map[int]ExitSiteMeta{
			9: {PC: 3, Op: "GuardTableKind", Reason: "GuardTableKind(2)"},
		},
	}
	tm.suppressTier2GuardKind(proto, 3, "GuardTableKind")

	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitDeopt, DeoptInstrID: 9})
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitDeopt, DeoptInstrID: 9})
	snap := tm.exitProfile.snapshot()
	if len(snap.Sites) != 1 || !snap.Sites[0].SuppressedGuard {
		t.Fatalf("suppressed guard exit not marked: %+v", snap)
	}
	if _, ok := tm.recompileQueue.take(proto); ok {
		t.Fatal("suppressed guard exit should not queue recompile")
	}
}

func TestPromotionPolicyForcesTier2ForQueuedFeedbackRefresh(t *testing.T) {
	proto := &vm.FuncProto{Name: "refresh"}
	proto.CallCount = BaselineCompileThreshold
	decision := PromotionPolicy{}.Decide(proto, FuncProfile{}, PromotionPolicyState{
		Manager:            NewTieringManager(),
		RecompileRequested: true,
	})
	if decision.Action != TieringActionPromoteTier2 || decision.Reason != PromotionReasonFeedbackRefresh {
		t.Fatalf("decision=%s reason=%s want promote feedback_refresh", decision.Action, decision.Reason)
	}
}

func TestExitProfileDoesNotQueueRecompileForOpExit(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "format", Code: make([]uint32, 4)}
	proto.EnsureFeedback()
	proto.Feedback[3].Result = vm.FBInt
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 0,
		},
		ExitSites: map[int]ExitSiteMeta{
			9: {PC: 3, Op: "StringFormat", Reason: "StringFormatConst"},
		},
	}

	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitOpExit, OpExitID: 9})
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitOpExit, OpExitID: 9})
	if _, ok := tm.recompileQueue.take(proto); ok {
		t.Fatal("op-exit feedback should not queue structural recompile")
	}
}
