package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

const sumPrimesKernelTestSource = `
func is_prime(n) {
    if n < 2 { return 0 }
    if n < 4 { return 1 }
    if n % 2 == 0 { return 0 }
    if n % 3 == 0 { return 0 }
    i := 5
    for i * i <= n {
        if n % i == 0 { return 0 }
        if n % (i + 2) == 0 { return 0 }
        i = i + 6
    }
    return 1
}

func sum_primes(limit) {
    sum := 0
    count := 0
    for i := 2; i <= limit; i++ {
        if is_prime(i) != 0 {
            sum = sum + i
            count = count + 1
        }
    }
    return {sum: sum, count: count}
}
`

func TestSumPrimesWholeCallKernelRecognizedAndMatchesVM(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, sumPrimesKernelTestSource+`
result := sum_primes(1000)
`)
	isPrime := proto.Protos[0]
	sumPrimes := proto.Protos[1]
	if !isTrialDivisionIsPrimeProto(isPrime) {
		t.Fatalf("is_prime kernel guard not recognized: code=%d maxstack=%d", len(isPrime.Code), isPrime.MaxStack)
	}
	if !isSumPrimesTrialDivisionProto(sumPrimes) {
		t.Fatalf("sum_primes kernel not recognized: code=%d const=%d maxstack=%d", len(sumPrimes.Code), len(sumPrimes.Constants), sumPrimes.MaxStack)
	}
	if !cachedWholeCallKernelRecognized(sumPrimes, wholeCallKernelSumPrimes) {
		t.Fatal("sum_primes kernel cache did not mark proto recognized")
	}
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("VM execute: %v", err)
	}
	want := vm.GetGlobal("result")
	handled, got, err := vm.runSumPrimesTrialDivisionWholeCallKernel(NewClosure(sumPrimes), []runtime.Value{runtime.IntValue(1000)})
	if err != nil {
		t.Fatalf("kernel error: %v", err)
	}
	if !handled || len(got) != 1 || !got[0].IsTable() {
		t.Fatalf("kernel result handled=%v got=%v", handled, got)
	}
	if got[0].Table().RawGetString("sum") != want.Table().RawGetString("sum") ||
		got[0].Table().RawGetString("count") != want.Table().RawGetString("count") {
		t.Fatalf("kernel result got sum=%v count=%v want sum=%v count=%v",
			got[0].Table().RawGetString("sum"), got[0].Table().RawGetString("count"),
			want.Table().RawGetString("sum"), want.Table().RawGetString("count"))
	}
}
