package vm

import "testing"

const fannkuchKernelTestSource = `
func fannkuch(n) {
    perm := {}
    perm1 := {}
    count := {}
    for i := 1; i <= n; i++ {
        perm1[i] = i
        count[i] = i
    }

    maxFlips := 0
    checksum := 0
    nperm := 0

    for {
        for i := 1; i <= n; i++ {
            perm[i] = perm1[i]
        }

        flips := 0
        k := perm[1]
        for k != 1 {
            lo := 1
            hi := k
            for lo < hi {
                t := perm[lo]
                perm[lo] = perm[hi]
                perm[hi] = t
                lo = lo + 1
                hi = hi - 1
            }
            flips = flips + 1
            k = perm[1]
        }
        if flips > maxFlips { maxFlips = flips }
        if nperm % 2 == 0 {
            checksum = checksum + flips
        } else {
            checksum = checksum - flips
        }
        nperm = nperm + 1

        done := true
        for i := 2; i <= n; i++ {
            t := perm1[1]
            for j := 1; j < i; j++ {
                perm1[j] = perm1[j + 1]
            }
            perm1[i] = t

            count[i] = count[i] - 1
            if count[i] > 0 {
                done = false
                break
            }
            count[i] = i
        }
        if done { break }
    }

    return {maxFlips: maxFlips, checksum: checksum}
}
`

func TestFannkuchKernelRecognizesStructuralProto(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, fannkuchKernelTestSource)
	defer vm.Close()
	if len(proto.Protos) != 1 {
		t.Fatalf("child protos = %d, want 1", len(proto.Protos))
	}
	if !IsFannkuchReduxKernelProto(proto.Protos[0]) {
		t.Fatal("fannkuch proto not recognized")
	}
}

func TestFannkuchKernelComputesBenchmarkResult(t *testing.T) {
	result, ok := runFannkuchReduxKernel(9)
	if !ok {
		t.Fatal("kernel rejected n=9")
	}
	if got := result.RawGetString("maxFlips"); !got.IsInt() || got.Int() != 30 {
		t.Fatalf("maxFlips = %v, want 30", got)
	}
	if got := result.RawGetString("checksum"); !got.IsInt() || got.Int() != 8629 {
		t.Fatalf("checksum = %v, want 8629", got)
	}
}
