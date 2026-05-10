package methodjit

import "testing"

func TestTier2TableObjectPreparationModuleOrder(t *testing.T) {
	mods := tier2TableObjectPreparationModules(nil)
	got := make([]string, 0, len(mods))
	for _, mod := range mods {
		if mod.Phase != Tier2PhaseTableObjectPrep {
			t.Fatalf("module %s phase=%s want %s", mod.Name, mod.Phase, Tier2PhaseTableObjectPrep)
		}
		got = append(got, mod.Name)
	}
	want := []string{
		"TablePreallocHint",
		"TypeSpecialize (post-table-prealloc)",
		"FixedShapeTableFacts",
		"LoadElimination",
		"FieldLenFold",
		"EscapeAnalysis",
		"FixedTableConstructorLowering",
		"TablePreallocHint (post-fixed-table-lowering)",
		"EscapeAnalysis (post-fixed-table-lowering)",
		"RedundantGuardElimination",
	}
	if len(got) != len(want) {
		t.Fatalf("module count=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("module[%d]=%q want %q; all=%v", i, got[i], want[i], got)
		}
	}
}

func TestTier2TableNativeLoweringModuleOrder(t *testing.T) {
	arrayMods := tier2TableArrayNativeLoweringModules()
	wantArray := []string{"TableArrayLower", "TableArrayLoadTypeSpecialize", "TableArrayNestedLoad"}
	if len(arrayMods) != len(wantArray) {
		t.Fatalf("array module count=%d want %d", len(arrayMods), len(wantArray))
	}
	for i, want := range wantArray {
		if arrayMods[i].Phase != Tier2PhaseTableArrayLower || arrayMods[i].Name != want {
			t.Fatalf("array module[%d]=%s/%s want %s/%s", i, arrayMods[i].Phase, arrayMods[i].Name, Tier2PhaseTableArrayLower, want)
		}
	}

	fieldMods := tier2TableFieldNativeLoweringModules()
	wantField := []string{"FieldSvalsLower", "TableArrayStoreLower"}
	if len(fieldMods) != len(wantField) {
		t.Fatalf("field module count=%d want %d", len(fieldMods), len(wantField))
	}
	for i, want := range wantField {
		if fieldMods[i].Phase != Tier2PhaseTableFieldLower || fieldMods[i].Name != want {
			t.Fatalf("field module[%d]=%s/%s want %s/%s", i, fieldMods[i].Phase, fieldMods[i].Name, Tier2PhaseTableFieldLower, want)
		}
	}
}
