package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestBoolTableStrikeCountKernelRecognizesStructuralProto(t *testing.T) {
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
	if !IsBoolTableStrikeCountKernelProto(proto.Protos[0]) {
		t.Fatal("bool-table strike/count structural proto not recognized")
	}
}

func TestBoolTableStrikeCountKernelRecognizerAllowsStackMetadataSlack(t *testing.T) {
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
`)
	child := *proto.Protos[0]
	child.MaxStack += 4
	if !IsBoolTableStrikeCountKernelProto(&child) {
		t.Fatal("sieve recognizer should ignore non-semantic MaxStack slack")
	}
}

func TestBoolTableStrikeCountKernelWholeCallCorrectness(t *testing.T) {
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
		t.Fatalf("bool-table strike/count kernel error: %v", err)
	}
	if !handled {
		t.Fatal("bool-table strike/count kernel did not handle structural proto")
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 25 {
		t.Fatalf("bool-table strike/count kernel result = %v, want 25", results)
	}
}

func TestBoolTableStrikeCountKernelFallsBackForNonStructuralProto(t *testing.T) {
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
