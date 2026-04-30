package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestSieveKernelRecognizesStructuralProto(t *testing.T) {
	proto, _ := compileSpectralKernelTestProgram(t, `
func local_name(n) {
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
result := local_name(100)
`)
	if len(proto.Protos) != 1 {
		t.Fatalf("nested protos = %d, want 1", len(proto.Protos))
	}
	if !IsSieveKernelProto(proto.Protos[0]) {
		t.Fatal("sieve structural proto not recognized")
	}
}

func TestSieveKernelWholeCallCorrectness(t *testing.T) {
	proto, v := compileSpectralKernelTestProgram(t, `
func sieve_like(n) {
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
result := sieve_like(100)
`)
	handled, results, err := v.tryRunValueWholeCallKernel(NewClosure(proto.Protos[0]), []runtime.Value{runtime.IntValue(100)})
	if err != nil {
		t.Fatalf("sieve kernel error: %v", err)
	}
	if !handled {
		t.Fatal("sieve kernel did not handle structural proto")
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 25 {
		t.Fatalf("sieve kernel result = %v, want 25", results)
	}
}

func TestSieveKernelFallsBackForNonStructuralProto(t *testing.T) {
	proto, v := compileSpectralKernelTestProgram(t, `
func almost_sieve(n) {
    is_prime := {}
    for i := 2; i <= n; i++ {
        is_prime[i] = true
    }
    return is_prime[n]
}
result := almost_sieve(10)
`)
	handled, _, err := v.tryRunValueWholeCallKernel(NewClosure(proto.Protos[0]), []runtime.Value{runtime.IntValue(10)})
	if err != nil {
		t.Fatalf("fallback check error: %v", err)
	}
	if handled {
		t.Fatal("non-structural proto should fall back")
	}
}
