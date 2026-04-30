//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestBoolTableCountLoop_RewritesSieveFinalScan(t *testing.T) {
	src := `
func sieve_once(n) {
    is_prime := {}
    for i := 2; i <= n; i++ {
        is_prime[i] = true
    }
    i := 2
    for i * i <= n {
        if is_prime[i] {
            j := i * i
            for j <= n {
                is_prime[j] = false
                j = j + i
            }
        }
        i = i + 1
    }
    count := 0
    for i := 2; i <= n; i++ {
        if is_prime[i] { count = count + 1 }
    }
    return count
}
result := sieve_once(100)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "sieve_once")
	if proto == nil {
		t.Fatal("sieve_once proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(sieve_once): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableBoolArrayCount") {
		t.Fatalf("expected bool table count rewrite:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}

func TestBoolTableCountLoop_DoesNotRewriteUnboundedCountRange(t *testing.T) {
	src := `
func count_flags(n) {
    flags := {}
    for i := 1; i <= 4; i++ {
        flags[i] = true
    }
    count := 0
    for i := 1; i <= n; i++ {
        if flags[i] { count = count + 1 }
    }
    return count
}
result := count_flags(4)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "count_flags")
	if proto == nil {
		t.Fatal("count_flags proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(count_flags): %v", err)
	}
	if strings.Contains(art.IRAfter, "TableBoolArrayCount") {
		t.Fatalf("unbounded count range should not be rewritten:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}

func TestTableBoolArrayCountBackend_FastPath(t *testing.T) {
	cf := compileBoolCountBackendFixture(t, "bool_count_fast", 1, 5)
	defer cf.Code.Free()

	tbl := runtime.NewTableSizedKind(8, 0, runtime.ArrayBool)
	for i := int64(1); i <= 5; i++ {
		tbl.RawSetInt(i, runtime.BoolValue(i%2 == 1))
	}
	got := executeBoolCountFixture(t, cf, tbl)
	if got != 3 {
		t.Fatalf("bool count fast path = %d, want 3", got)
	}
}

func TestTableBoolArrayCountBackend_MixedKindFallback(t *testing.T) {
	withExitResumeCheck(t, func() {
		cf := compileBoolCountBackendFixture(t, "bool_count_mixed", 1, 3)
		defer cf.Code.Free()

		tbl := runtime.NewTable()
		tbl.RawSetInt(1, runtime.BoolValue(true))
		tbl.RawSetInt(2, runtime.BoolValue(false))
		tbl.RawSetInt(3, runtime.BoolValue(true))
		tbl.RawSetInt(0, runtime.StringValue("mixed"))
		got := executeBoolCountFixture(t, cf, tbl)
		if got != 2 {
			t.Fatalf("bool count mixed fallback = %d, want 2", got)
		}
		assertCompiledExitResumeCheckSite(t, cf, func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitTableExit && site.RequireTableInputs
		})
	})
}

func TestTableBoolArrayCountBackend_BoundsAndHashFallback(t *testing.T) {
	withExitResumeCheck(t, func() {
		cf := compileBoolCountBackendFixture(t, "bool_count_bounds", 1, 1025)
		defer cf.Code.Free()

		tbl := runtime.NewTableSizedKind(3, 0, runtime.ArrayBool)
		tbl.RawSetInt(1, runtime.BoolValue(true))
		tbl.RawSetInt(2, runtime.BoolValue(false))
		tbl.RawSetInt(3, runtime.BoolValue(true))
		tbl.RawSetInt(1025, runtime.BoolValue(true))
		got := executeBoolCountFixture(t, cf, tbl)
		if got != 3 {
			t.Fatalf("bool count bounds/hash fallback = %d, want 3", got)
		}
		assertCompiledExitResumeCheckSite(t, cf, func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitTableExit && site.RequireTableInputs
		})
	})
}

func TestTableBoolArrayCountBackend_MetatableFallback(t *testing.T) {
	withExitResumeCheck(t, func() {
		cf := compileBoolCountBackendFixture(t, "bool_count_metatable", 1, 3)
		defer cf.Code.Free()

		tbl := runtime.NewTableSizedKind(4, 0, runtime.ArrayBool)
		tbl.RawSetInt(1, runtime.BoolValue(true))
		tbl.RawSetInt(2, runtime.BoolValue(true))
		tbl.RawSetInt(3, runtime.BoolValue(false))
		tbl.SetMetatable(runtime.NewTable())
		got := executeBoolCountFixture(t, cf, tbl)
		if got != 2 {
			t.Fatalf("bool count metatable fallback = %d, want 2", got)
		}
		assertCompiledExitResumeCheckSite(t, cf, func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitTableExit && site.RequireTableInputs
		})
	})
}

func compileBoolCountBackendFixture(t *testing.T, name string, start, end int64) *CompiledFunction {
	t.Helper()
	fn := &Function{
		Proto:   &vm.FuncProto{Name: name, NumParams: 1},
		NumRegs: 4,
	}
	entry := newBlock(0)
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	startInstr := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: start, Block: entry}
	endInstr := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: end, Block: entry}
	count := &Instr{ID: fn.newValueID(), Op: OpTableBoolArrayCount, Type: TypeInt,
		Args: []*Value{tbl.Value(), startInstr.Value(), endInstr.Value()}, Block: entry}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{count.Value()}, Block: entry}
	entry.Instrs = []*Instr{tbl, startInstr, endInstr, count, ret}

	assertValidates(t, fn, name)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile(%s): %v", name, err)
	}
	return cf
}

func executeBoolCountFixture(t *testing.T, cf *CompiledFunction, tbl *runtime.Table) int64 {
	t.Helper()
	result, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)})
	if err != nil {
		t.Fatalf("Execute bool count fixture: %v", err)
	}
	if len(result) == 0 || !result[0].IsInt() {
		t.Fatalf("bool count result = %v, want int", result)
	}
	return result[0].Int()
}
