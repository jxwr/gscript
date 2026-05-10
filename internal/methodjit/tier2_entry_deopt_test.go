//go:build darwin && arm64

package methodjit

import "testing"

func TestTier2DeoptAtEntryRecognizesPreBytecodeGuard(t *testing.T) {
	tm := NewTieringManager()
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			7: {PC: -1, Op: "LoadSlot"},
		},
	}
	if !tm.tier2DeoptAtEntry(cf, &ExecContext{DeoptInstrID: 7}) {
		t.Fatal("expected entry LoadSlot guard deopt to be restartable in Tier1")
	}
}

func TestTier2DeoptAtEntryRejectsMidFunctionOrPreciseResume(t *testing.T) {
	tm := NewTieringManager()
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			7: {PC: 3, Op: "GuardType"},
		},
	}
	if tm.tier2DeoptAtEntry(cf, &ExecContext{DeoptInstrID: 7}) {
		t.Fatal("mid-function guard deopt must not restart Tier1 from entry")
	}
	cf.ExitSites[7] = ExitSiteMeta{PC: -1, Op: "LoadSlot"}
	if tm.tier2DeoptAtEntry(cf, &ExecContext{DeoptInstrID: 7, ExitResumePC: 4}) {
		t.Fatal("precise resume deopt should use ResumeFromPC instead")
	}
}
