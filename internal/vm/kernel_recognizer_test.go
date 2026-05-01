package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

func TestWholeCallKernelDiagnosticsIgnoreProtoNameAndSource(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
func local_prime_counter(n) {
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
`)
	defer vm.Close()
	if len(proto.Protos) != 1 {
		t.Fatalf("nested protos = %d, want 1", len(proto.Protos))
	}

	child := proto.Protos[0]
	child.Name = "fannkuch"
	child.Source = "benchmarks/suite/fannkuch.gs"

	infos := RecognizedWholeCallKernels(child)
	requireKernelInfo(t, infos, "sieve_count")
	rejectKernelInfo(t, infos, "fannkuch_redux")

	diag := requireKernelDiagnostic(t, DiagnoseWholeCallKernelProto(child), "sieve_count")
	if !diag.Recognized || diag.Reason != kernelReasonRecognized {
		t.Fatalf("sieve diagnostic = %+v, want recognized structural bytecode", diag)
	}
}

func TestWholeCallKernelCacheDispatchIgnoresProtoNameAndSource(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
func local_prime_counter(n) {
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
`)
	defer vm.Close()
	child := proto.Protos[0]
	requireKernelInfo(t, RecognizedWholeCallKernels(child), "sieve_count")

	child.Name = "fannkuch"
	child.Source = "benchmarks/suite/fannkuch.gs"
	child.LineDefined = 999
	child.LineInfo = append([]int(nil), child.LineInfo...)
	for i := range child.LineInfo {
		child.LineInfo[i] += 100
	}

	requireKernelInfo(t, RecognizedWholeCallKernels(child), "sieve_count")
	handled, results, err := vm.tryRunValueWholeCallKernel(NewClosure(child), []runtime.Value{runtime.IntValue(100)})
	if err != nil || !handled || len(results) != 1 || !results[0].IsInt() || results[0].Int() != 25 {
		t.Fatalf("renamed/source-changed sieve dispatch = handled=%v results=%v err=%v, want 25", handled, results, err)
	}
}

func TestWholeCallKernelCacheInvalidatesOnStructuralMutation(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
func local_prime_counter(n) {
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
`)
	defer vm.Close()
	child := proto.Protos[0]
	requireKernelInfo(t, RecognizedWholeCallKernels(child), "sieve_count")

	originalCode := append([]uint32(nil), child.Code...)
	child.Code = append([]uint32(nil), originalCode...)
	child.Code[7] = EncodeABC(OP_MOVE, 2, 0, 0)
	if infos := RecognizedWholeCallKernels(child); len(infos) != 0 {
		t.Fatalf("mutated sieve proto recognized as %+v", infos)
	}
	handled, results, err := vm.tryRunValueWholeCallKernel(NewClosure(child), []runtime.Value{runtime.IntValue(100)})
	if err != nil || handled || len(results) != 0 {
		t.Fatalf("mutated sieve dispatch = handled=%v results=%v err=%v, want exact fallback", handled, results, err)
	}

	child.Name = "sieve"
	child.Source = "benchmarks/suite/sieve.gs"
	if infos := RecognizedWholeCallKernels(child); len(infos) != 0 {
		t.Fatalf("benchmark metadata restored recognition for mutated proto: %+v", infos)
	}

	child.Code = originalCode
	requireKernelInfo(t, RecognizedWholeCallKernels(child), "sieve_count")
}

func TestCachedWholeCallKernelRecognizedUsesHotCache(t *testing.T) {
	proto := &FuncProto{
		WholeCallKernel: &wholeCallKernelProtoCache{
			fingerprint: wholeCallKernelFingerprint{codeLen: 123},
			recognized:  uint64(1) << uint(wholeCallKernelFannkuchRedux),
		},
	}
	if !cachedWholeCallKernelRecognized(proto, wholeCallKernelFannkuchRedux) {
		t.Fatal("cached hot dispatch guard recomputed structure instead of using cached bits")
	}
	if cachedWholeCallKernelRecognized(proto, wholeCallKernelSieveCount) {
		t.Fatal("cached hot dispatch guard reported an uncached kernel bit")
	}
}

func TestWholeCallKernelDiagnosticsRejectBenchmarkMetadataWithoutShape(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
func fannkuch(n) { return n }
func sieve(n) { return n }
func product(left, right, size) { return left }
func advance(dt) { return dt }
`)
	defer vm.Close()
	if len(proto.Protos) != 4 {
		t.Fatalf("nested protos = %d, want 4", len(proto.Protos))
	}

	sources := []string{
		"benchmarks/suite/fannkuch.gs",
		"benchmarks/suite/sieve.gs",
		"benchmarks/suite/matmul.gs",
		"benchmarks/suite/nbody.gs",
	}
	for i, child := range proto.Protos {
		child.Source = sources[i]
		if infos := RecognizedWholeCallKernels(child); len(infos) != 0 {
			t.Fatalf("metadata-only proto %q/%q recognized as %+v", child.Name, child.Source, infos)
		}
		for _, diag := range DiagnoseWholeCallKernelProto(child) {
			if diag.Recognized {
				t.Fatalf("metadata-only proto %q/%q recognized by diagnostic %+v", child.Name, child.Source, diag)
			}
			if diag.Reason != kernelReasonShapeMismatch {
				t.Fatalf("metadata-only diagnostic reason = %q, want %q", diag.Reason, kernelReasonShapeMismatch)
			}
		}
	}
}

func BenchmarkWholeCallKernelDispatchCachedSieve(b *testing.B) {
	proto, vm := compileWholeCallKernelBenchmarkProgram(b, `
func local_prime_counter(n) {
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
`)
	defer vm.Close()
	cl := NewClosure(proto.Protos[0])
	args := []runtime.Value{runtime.IntValue(10)}
	handled, results, err := vm.tryRunValueWholeCallKernel(cl, args)
	if err != nil || !handled || len(results) != 1 || !results[0].IsInt() || results[0].Int() != 4 {
		b.Fatalf("sieve dispatch preflight = handled=%v results=%v err=%v, want 4", handled, results, err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handled, results, err = vm.tryRunValueWholeCallKernel(cl, args)
		if err != nil || !handled || len(results) != 1 {
			b.Fatalf("sieve dispatch = handled=%v results=%v err=%v", handled, results, err)
		}
	}
}

func compileWholeCallKernelBenchmarkProgram(b *testing.B, src string) (*FuncProto, *VM) {
	b.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		b.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		b.Fatalf("parse error: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		b.Fatalf("compile error: %v", err)
	}
	return proto, New(runtime.NewInterpreterGlobals())
}

func TestWholeCallKernelDiagnosticsIncludeRecursiveTableProtocols(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, recursiveTableKernelProgram)
	defer vm.Close()
	if len(proto.Protos) != 2 {
		t.Fatalf("nested protos = %d, want 2", len(proto.Protos))
	}

	builder := proto.Protos[0]
	fold := proto.Protos[1]
	builder.Name = "not_the_recursive_binding"
	builder.Source = "benchmarks/suite/binary_trees.gs"
	fold.Name = "also_not_the_recursive_binding"
	fold.Source = "benchmarks/suite/binary_trees.gs"

	requireKernelInfo(t, RecognizedWholeCallKernels(builder), "recursive_table_builder")
	requireKernelInfo(t, RecognizedWholeCallKernels(fold), "recursive_table_fold")
	rejectKernelInfo(t, RecognizedWholeCallKernels(builder), "recursive_table_fold")
	rejectKernelInfo(t, RecognizedWholeCallKernels(fold), "recursive_table_builder")
}

func TestDriverLoopKernelDiagnosticsRecognizeStructuralLoops(t *testing.T) {
	primeTop, primeVM := compileSpectralKernelTestProgram(t, trialDivisionPrimePredicateSource+`
limit := 2000
total := 0
hits := 0
for candidate := 2; candidate <= limit; candidate++ {
    if check(candidate) {
        total = total + candidate
        hits = hits + 1
    }
}
`)
	defer primeVM.Close()
	if len(primeTop.Protos) != 1 {
		t.Fatalf("prime nested protos = %d, want 1", len(primeTop.Protos))
	}
	primeTop.Name = "sum_primes"
	primeTop.Source = "benchmarks/suite/sum_primes.gs"
	requireKernelInfo(t, RecognizedDriverLoopKernels(primeTop, map[string]*FuncProto{
		"check": primeTop.Protos[0],
	}), "prime_predicate_sum_loop")

	nbodyTop, nbodyVM := compileSpectralKernelTestProgram(t, nbodyKernelTestProgram+`
N := 2000
dt := 0.01
for i := 1; i <= N; i++ { advance(dt) }
`)
	defer nbodyVM.Close()
	if len(nbodyTop.Protos) != 1 {
		t.Fatalf("nbody nested protos = %d, want 1", len(nbodyTop.Protos))
	}
	nbodyTop.Name = "nbody"
	nbodyTop.Source = "benchmarks/suite/nbody.gs"
	requireKernelInfo(t, RecognizedDriverLoopKernels(nbodyTop, map[string]*FuncProto{
		"advance": nbodyTop.Protos[0],
	}), "nbody_advance_loop")
}

func TestDriverLoopKernelDiagnosticsReportFallbackReasons(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, trialDivisionPrimePredicateSource+`
limit := 30
total := 0
hits := 0
for candidate := 2; candidate <= limit; candidate++ {
    if check(candidate) {
        total = total + candidate
        hits = hits + 1
    }
}
`)
	defer vm.Close()

	missingMap := requireKernelDiagnostic(t, DiagnoseDriverLoopKernels(proto, nil), "prime_predicate_sum_loop")
	if missingMap.Recognized || missingMap.Reason != kernelReasonMissingGlobalProtoMap {
		t.Fatalf("missing map diagnostic = %+v, want missing global proto map", missingMap)
	}

	recognized := requireKernelDiagnostic(t, DiagnoseDriverLoopKernels(proto, map[string]*FuncProto{
		"check": proto.Protos[0],
	}), "prime_predicate_sum_loop")
	if !recognized.Recognized || recognized.Reason != kernelReasonDriverRecognized {
		t.Fatalf("recognized diagnostic = %+v, want structural driver loop", recognized)
	}
}

func requireKernelInfo(t *testing.T, infos []KernelInfo, name string) {
	t.Helper()
	if !hasKernelInfo(infos, name) {
		t.Fatalf("kernel %q not found in %+v", name, infos)
	}
}

func rejectKernelInfo(t *testing.T, infos []KernelInfo, name string) {
	t.Helper()
	if hasKernelInfo(infos, name) {
		t.Fatalf("kernel %q unexpectedly found in %+v", name, infos)
	}
}

func hasKernelInfo(infos []KernelInfo, name string) bool {
	for _, info := range infos {
		if info.Name == name {
			return true
		}
	}
	return false
}

func requireKernelDiagnostic(t *testing.T, diagnostics []KernelDiagnostic, name string) KernelDiagnostic {
	t.Helper()
	for _, diag := range diagnostics {
		if diag.Kernel.Name == name {
			return diag
		}
	}
	t.Fatalf("diagnostic for %q not found in %+v", name, diagnostics)
	return KernelDiagnostic{}
}
