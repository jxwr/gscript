//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Raw-int self-recursive ABI design matrix:
//   - eligible self recursion returns one raw int through the specialized path;
//   - nested self calls, especially ack(m-1, ack(m, n-1)), preserve the inner
//     result through the outer call argument protocol;
//   - exits from a raw-int callee fall back through the boxed resume path and
//     restore the caller's live values;
//   - deep recursive pressure trips the depth/fallback policy instead of
//     corrupting native frames;
//   - non-eligible protos stay on the boxed Tier 2 ABI.
//
// These are the red/green contract for the raw ABI entry guards, liveness,
// exit-resume, fallback materialization, and return convention.

func TestRawIntSelfABI_EligibleExecutionMatrix(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		fnName      string
		args        []runtime.Value
		wantParams  int
		compile     []string
		wantEntered []string
	}{
		{
			name:       "tail accumulator return",
			fnName:     "fact",
			wantParams: 2,
			src: `func fact(n, acc) {
	if n <= 1 { return acc }
	return fact(n - 1, acc * n)
}`,
			args:        []runtime.Value{runtime.IntValue(7), runtime.IntValue(1)},
			compile:     []string{"fact"},
			wantEntered: []string{"fact"},
		},
		{
			name:       "nested ack self calls",
			fnName:     "ack",
			wantParams: 2,
			src: `func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}`,
			args:        []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)},
			compile:     []string{"ack"},
			wantEntered: []string{"ack"},
		},
		{
			name:       "non-tail raw return plus live locals",
			fnName:     "fib",
			wantParams: 1,
			src: `func fib(n) {
	if n < 2 { return n }
	left := fib(n - 1)
	right := fib(n - 2)
	return left + right
}`,
			args:        []runtime.Value{runtime.IntValue(10)},
			compile:     []string{"fib"},
			wantEntered: []string{"fib"},
		},
		{
			name:       "native caller keeps live values around raw callee",
			fnName:     "caller",
			wantParams: 1,
			src: `func fib(n) {
	if n < 2 { return n }
	return fib(n - 1) + fib(n - 2)
}
func caller(n) {
	a := n + 11
	b := n * 100
	c := fib(n)
	d := a * 1000
	return d + b + c
}`,
			args:        []runtime.Value{runtime.IntValue(9)},
			compile:     []string{"fib", "caller"},
			wantEntered: []string{"fib", "caller"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := compileTop(t, tt.src)
			candidate := findProtoByName(top, tt.fnName)
			if candidate == nil {
				t.Fatalf("function %q not found", tt.fnName)
			}

			if tt.fnName == "caller" {
				fib := findProtoByName(top, "fib")
				if fib == nil {
					t.Fatal("function \"fib\" not found")
				}
				assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(fib), tt.wantParams)
				if abi := AnalyzeSpecializedABI(candidate); abi.Eligible {
					t.Fatalf("caller must not use raw self ABI, got %+v", abi)
				}
			} else {
				assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(candidate), tt.wantParams)
			}

			vmResults := runVMByName(t, tt.src, tt.fnName, tt.args)
			jitResults, entered := runForcedTier2ByName(t, top, tt.fnName, tt.compile, tt.args)
			assertRawIntSelfResultsEqual(t, tt.fnName, jitResults, vmResults)
			for _, name := range tt.wantEntered {
				if entered[name] == 0 {
					t.Fatalf("%s did not enter Tier 2", name)
				}
			}
		})
	}
}

func TestRawIntSelfABI_ExitResumeFallbackKeepsCallerLiveValues(t *testing.T) {
	src := `func grow(n, x) {
	if n == 0 { return x }
	return grow(n - 1, x + 100000000000000)
}
func caller(n, seed) {
	a := n + 17
	b := seed - 3
	c := n * 1000
	r := grow(n, seed)
	return r + a + b + c
}`
	top := compileTop(t, src)
	grow := findProtoByName(top, "grow")
	if grow == nil {
		t.Fatal("function \"grow\" not found")
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(grow), 2)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("function \"caller\" not found")
	}
	if abi := AnalyzeSpecializedABI(caller); abi.Eligible {
		t.Fatalf("caller must stay boxed, got %+v", abi)
	}

	// n must exceed the recursive inliner budget; otherwise caller can inline
	// all executed grow frames and the separate grow Tier2 entry is not reached.
	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(90000000000000)}
	vmResults := runVMByName(t, src, "caller", args)
	jitResults, entered := runForcedTier2ByName(t, top, "caller", []string{"grow", "caller"}, args)
	assertRawIntSelfResultsEqual(t, "caller", jitResults, vmResults)
	if entered["caller"] == 0 || entered["grow"] == 0 {
		t.Fatalf("expected caller and grow to enter Tier 2, entered=%v", entered)
	}
}

func TestRawIntSelfABI_DepthPressureFallback(t *testing.T) {
	src := `func sumdown(n) {
	if n == 0 { return 0 }
	return n + sumdown(n - 1)
}`
	top := compileTop(t, src)
	sumdown := findProtoByName(top, "sumdown")
	if sumdown == nil {
		t.Fatal("function \"sumdown\" not found")
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(sumdown), 1)

	args := []runtime.Value{runtime.IntValue(200)}
	vmResults := runVMByName(t, src, "sumdown", args)
	jitResults, entered := runForcedTier2ByName(t, top, "sumdown", []string{"sumdown"}, args)
	assertRawIntSelfResultsEqual(t, "sumdown", jitResults, vmResults)
	if entered["sumdown"] == 0 {
		t.Fatal("sumdown did not enter Tier 2")
	}
}

func TestRawIntSelfABI_NonTailFourArgReturnConvention(t *testing.T) {
	src := `func mix(a, b, c, d) {
	if a == 0 { return b + c + d }
	inner := mix(a - 1, b + 1, c + 2, d + 3)
	return inner + d
}`
	top := compileTop(t, src)
	mix := findProtoByName(top, "mix")
	if mix == nil {
		t.Fatal("function \"mix\" not found")
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(mix), 4)

	args := []runtime.Value{runtime.IntValue(5), runtime.IntValue(7), runtime.IntValue(11), runtime.IntValue(13)}
	vmResults := runVMByName(t, src, "mix", args)
	jitResults, entered := runForcedTier2ByName(t, top, "mix", []string{"mix"}, args)
	assertRawIntSelfResultsEqual(t, "mix", jitResults, vmResults)
	if entered["mix"] == 0 {
		t.Fatal("mix did not enter Tier 2")
	}
}

func TestRawIntSelfABI_NumericEntryUsesThinFrame(t *testing.T) {
	src := `func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}`
	top := compileTop(t, src)
	ack := findProtoByName(top, "ack")
	if ack == nil {
		t.Fatal("function \"ack\" not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(ack); err != nil {
		t.Fatalf("CompileTier2(ack): %v", err)
	}
	cf := tm.tier2Compiled[ack]
	if cf == nil {
		t.Fatal("ack did not compile to Tier 2")
	}
	if cf.NumericParamCount != 2 {
		t.Fatalf("NumericParamCount=%d, want 2", cf.NumericParamCount)
	}
	if cf.NumericEntryOffset <= 0 {
		t.Fatalf("NumericEntryOffset=%d, want positive offset", cf.NumericEntryOffset)
	}

	code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
	entry := cf.NumericEntryOffset
	if entry+16 > len(code) {
		t.Fatalf("numeric entry offset %d outside code size %d", entry, len(code))
	}

	assertThinFramePrologue(t, code, entry, "numeric entry")
	assertNumericEntryAvoidsCtxRegsReload(t, code, entry)

	if len(cf.NumericResumeAddrs) == 0 {
		t.Fatal("expected numeric resume entries for raw self-call fallback")
	}
	for _, resumeOff := range cf.NumericResumeAddrs {
		assertThinFramePrologue(t, code, resumeOff, "numeric resume")
		break
	}
}

func TestRawIntSelfABI_FastPathLeavesCallModeUntouched(t *testing.T) {
	src := `func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}`
	top := compileTop(t, src)
	ack := findProtoByName(top, "ack")
	if ack == nil {
		t.Fatal("function \"ack\" not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(ack); err != nil {
		t.Fatalf("CompileTier2(ack): %v", err)
	}
	cf := tm.tier2Compiled[ack]
	if cf == nil {
		t.Fatal("ack did not compile to Tier 2")
	}

	code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
	rawSelfShims := 0
	for pc := 0; pc+4 <= len(code); pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		if !isSubSPImm(word, rawSelfFrameSizeFor(2)) {
			continue
		}
		sawBL := false
		for scan := pc + 4; scan+4 <= len(code); scan += 4 {
			scanWord := binary.LittleEndian.Uint32(code[scan : scan+4])
			if isCtxCallModeAccess(scanWord) {
				t.Fatalf("raw self-call shim at %#x touches ctx.CallMode before BL at %#x", pc, scan)
			}
			if isBL(scanWord) {
				sawBL = true
				break
			}
			if isUnconditionalB(scanWord) || scan-pc > 200 {
				break
			}
		}
		if sawBL {
			rawSelfShims++
		}
	}
	if rawSelfShims == 0 {
		t.Fatal("expected at least one raw self-call shim")
	}
}

func TestRawIntSelfABI_FastPathUsesArgsOnlyFallbackFrame(t *testing.T) {
	tests := []struct {
		name             string
		src              string
		fnName           string
		wantParams       int
		wantArgSaveInsns int
	}{
		{
			name:       "fib one arg",
			fnName:     "fib",
			wantParams: 1,
			src: `func fib(n) {
	if n < 2 { return n }
	return fib(n - 1) + fib(n - 2)
}`,
			wantArgSaveInsns: 1,
		},
		{
			name:       "ack two args",
			fnName:     "ack",
			wantParams: 2,
			src: `func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}`,
			wantArgSaveInsns: 1,
		},
		{
			name:       "sum3 three args",
			fnName:     "sum3",
			wantParams: 3,
			src: `func sum3(a, b, c) {
	if a == 0 { return b + c }
	return sum3(a - 1, b + 1, c + 2) + c
}`,
			wantArgSaveInsns: 2,
		},
		{
			name:       "mix4 four args",
			fnName:     "mix4",
			wantParams: 4,
			src: `func mix4(a, b, c, d) {
	if a == 0 { return b + c + d }
	return mix4(a - 1, b + 1, c + 2, d + 3) + d
}`,
			wantArgSaveInsns: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := compileTop(t, tt.src)
			proto := findProtoByName(top, tt.fnName)
			if proto == nil {
				t.Fatalf("function %q not found", tt.fnName)
			}
			assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(proto), tt.wantParams)

			tm := NewTieringManager()
			if err := tm.CompileTier2(proto); err != nil {
				t.Fatalf("CompileTier2(%s): %v", tt.fnName, err)
			}
			cf := tm.tier2Compiled[proto]
			if cf == nil {
				t.Fatalf("%s did not compile to Tier 2", tt.fnName)
			}

			code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
			frameSize := rawSelfFrameSizeFor(tt.wantParams)
			rawSelfShims := 0
			for pc := 0; pc+4 <= len(code); pc += 4 {
				word := binary.LittleEndian.Uint32(code[pc : pc+4])
				if !isSubSPImm(word, frameSize) {
					continue
				}

				sawBL := false
				sawCallerBaseSave := false
				argSaveInsns := 0
				for scan := pc + 4; scan+4 <= len(code); scan += 4 {
					scanWord := binary.LittleEndian.Uint32(code[scan : scan+4])
					if isSTPToSPPair(scanWord, mRegRegs, jit.X0) {
						sawCallerBaseSave = true
					}
					if isSTRRegToSP(scanWord, mRegRegs) {
						sawCallerBaseSave = true
					}
					if isSTPToSPPair(scanWord, jit.X0, jit.X1) || isSTPToSPPair(scanWord, jit.X2, jit.X3) {
						argSaveInsns++
					}
					if isSTRArgToSP(scanWord) {
						argSaveInsns++
					}
					if isBL(scanWord) {
						sawBL = true
						break
					}
					if isUnconditionalB(scanWord) || scan-pc > 200 {
						break
					}
				}
				if !sawBL {
					continue
				}
				rawSelfShims++
				if sawCallerBaseSave {
					t.Fatalf("raw self-call shim at %#x still saves caller mRegRegs in the fallback arg frame", pc)
				}
				if argSaveInsns != tt.wantArgSaveInsns {
					t.Fatalf("raw self-call shim at %#x emitted %d arg-save insns, want %d", pc, argSaveInsns, tt.wantArgSaveInsns)
				}
			}
			if rawSelfShims == 0 {
				t.Fatal("expected at least one args-only raw self-call shim")
			}
		})
	}
}

func TestRawIntPeerABI_FastPathDoesNotBoxArgsToCalleeWindow(t *testing.T) {
	src := `func dec(n) {
	return n - 1
}
func caller(n) {
	total := 0
	for i := 1; i <= n; i++ {
		for j := 1; j <= 2; j++ {
			total = total + dec(i + j)
		}
	}
	return total
}`
	top := compileTop(t, src)
	dec := findProtoByName(top, "dec")
	caller := findProtoByName(top, "caller")
	if dec == nil || caller == nil {
		t.Fatalf("missing protos: dec=%v caller=%v", dec != nil, caller != nil)
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(dec), 1)

	tm := NewTieringManager()
	if err := tm.CompileTier2(dec); err != nil {
		t.Fatalf("CompileTier2(dec): %v", err)
	}
	if dec.Tier2NumericEntryPtr == 0 {
		t.Fatal("dec did not publish a numeric entry")
	}
	fn := BuildGraph(caller)
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals: map[string]*vm.FuncProto{"dec": dec},
		InlineMaxSize: 1,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(caller): %v", err)
	}
	annotatedCall := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpCall {
				desc, ok := fn.CallABIs[instr.ID]
				if !ok {
					t.Fatalf("call %d missing raw-int CallABI descriptor", instr.ID)
				}
				if desc.Callee != dec || desc.NumArgs != 1 || desc.NumRets != 1 || !desc.RawIntReturn || len(desc.RawIntParams) != 1 || !desc.RawIntParams[0] {
					t.Fatalf("unexpected CallABI descriptor for call %d: %+v", instr.ID, desc)
				}
				if instr.Type != TypeInt {
					t.Fatalf("call %d Type=%s, want int", instr.ID, instr.Type)
				}
				annotatedCall = true
			}
		}
	}
	if !annotatedCall {
		t.Fatal("expected residual call to dec")
	}
	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile(caller): %v", err)
	}

	code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())
	rawPeerShims := 0
	for pc := 0; pc+4 <= len(code); pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		if !isSubSPImm(word, rawPeerFrameSize) {
			continue
		}
		rawPeerShims++
		for scan := pc + 4; scan+4 <= len(code); scan += 4 {
			scanWord := binary.LittleEndian.Uint32(code[scan : scan+4])
			if isSTRRegToSP(scanWord, mRegRegs) || isSTRRegToSP(scanWord, mRegConsts) {
				t.Fatalf("leaf raw peer-call shim at %#x saves caller context to stack before BLR at %#x", pc, scan)
			}
			if isSTRToMRegRegs(scanWord) {
				t.Fatalf("raw peer-call shim at %#x boxes/stores args to VM window before BLR at %#x", pc, scan)
			}
			if isBLR(scanWord) {
				break
			}
			if scan-pc > 240 {
				t.Fatalf("raw peer-call shim at %#x did not reach BLR within expected window", pc)
			}
		}
	}
	if rawPeerShims == 0 {
		t.Fatal("expected at least one raw peer-call shim")
	}
}

func TestRawIntSelfABI_NonEligibleStaysBoxed(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		fnName  string
		args    []runtime.Value
		compile []string
	}{
		{
			name:   "non self call",
			fnName: "f",
			src: `func helper(x) { return x + 1 }
func f(x) { return helper(x) + 10 }`,
			args:    []runtime.Value{runtime.IntValue(31)},
			compile: []string{"helper", "f"},
		},
		{
			name:   "float return",
			fnName: "f",
			src: `func f(x) {
	if x == 0 { return 1.5 }
	return f(x - 1)
}`,
			args:    []runtime.Value{runtime.IntValue(3)},
			compile: []string{"f"},
		},
		{
			name:   "upvalue capture",
			fnName: "inner",
			src: `func outer(base) {
	func inner(n) {
		if n == 0 { return base }
		return inner(n - 1)
	}
	return inner
}
inner := outer(44)`,
			args:    []runtime.Value{runtime.IntValue(3)},
			compile: []string{"inner"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := compileTop(t, tt.src)
			proto := findProtoByName(top, tt.fnName)
			if proto == nil {
				t.Fatalf("function %q not found", tt.fnName)
			}
			if abi := AnalyzeSpecializedABI(proto); abi.Eligible {
				t.Fatalf("expected %s to be raw-int ineligible, got %+v", tt.fnName, abi)
			}

			vmResults := runVMByName(t, tt.src, tt.fnName, tt.args)
			jitResults, entered := runForcedTier2ByName(t, top, tt.fnName, tt.compile, tt.args)
			assertRawIntSelfResultsEqual(t, tt.fnName, jitResults, vmResults)
			if entered[tt.fnName] == 0 {
				t.Fatalf("%s did not enter generic Tier 2", tt.fnName)
			}
		})
	}
}

func isUnconditionalB(word uint32) bool {
	return word&0xFC000000 == 0x14000000
}

func isBL(word uint32) bool {
	return word&0xFC000000 == 0x94000000
}

func isBLR(word uint32) bool {
	return word&0xFFFFFC1F == 0xD63F0000
}

func isSubSPImm(word uint32, imm int) bool {
	if imm < 0 || imm > 4095 {
		return false
	}
	encodedImm := uint32(imm) << 10
	return word == 0xD10003FF|encodedImm
}

func isGPRPairStoreToSP(word uint32) bool {
	return word&0xFFC00000 == 0xA9000000 && ((word>>5)&0x1F) == 31
}

func isFPRPairStoreToSP(word uint32) bool {
	return word&0xFFC00000 == 0x6D000000 && ((word>>5)&0x1F) == 31
}

func isLDRCtxRegsToMRegRegs(word uint32) bool {
	pimm := uint32(execCtxOffRegs >> 3)
	return word == 0xF9400000|((pimm&0xFFF)<<10)|uint32(mRegCtx)<<5|uint32(mRegRegs)
}

func isSTRToMRegRegs(word uint32) bool {
	return word&0xFFC003E0 == 0xF9000000|uint32(mRegRegs)<<5
}

func isSTPToSPPair(word uint32, rt1, rt2 jit.Reg) bool {
	if word&0xFFC00000 != 0xA9000000 {
		return false
	}
	return word&0x1F == uint32(rt1) &&
		(word>>10)&0x1F == uint32(rt2) &&
		(word>>5)&0x1F == uint32(jit.SP)
}

func isSTRArgToSP(word uint32) bool {
	if word&0xFFC00000 != 0xF9000000 || (word>>5)&0x1F != uint32(jit.SP) {
		return false
	}
	rt := word & 0x1F
	return rt >= uint32(jit.X0) && rt <= uint32(jit.X3)
}

func isSTRRegToSP(word uint32, reg jit.Reg) bool {
	return word&0xFFC00000 == 0xF9000000 &&
		(word>>5)&0x1F == uint32(jit.SP) &&
		word&0x1F == uint32(reg)
}

func isCtxCallModeAccess(word uint32) bool {
	if word&0xFFC003E0 != 0xF9400000|uint32(mRegCtx)<<5 &&
		word&0xFFC003E0 != 0xF9000000|uint32(mRegCtx)<<5 {
		return false
	}
	return ((word >> 10) & 0xFFF) == uint32(execCtxOffCallMode>>3)
}

func assertThinFramePrologue(t *testing.T, code []byte, off int, label string) {
	t.Helper()
	if off+16 > len(code) {
		t.Fatalf("%s offset %d outside code size %d", label, off, len(code))
	}
	gprPairStores := 0
	fprPairStores := 0
	for pc := off; pc+4 <= len(code); pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		if isUnconditionalB(word) {
			break
		}
		if isGPRPairStoreToSP(word) {
			gprPairStores++
		}
		if isFPRPairStoreToSP(word) {
			fprPairStores++
		}
		if pc-off > 96 {
			t.Fatalf("%s did not branch within expected prologue window", label)
		}
	}
	if gprPairStores != 1 {
		t.Fatalf("%s should save only FP/LR before body branch, got %d GPR pair stores", label, gprPairStores)
	}
	if fprPairStores != 0 {
		t.Fatalf("%s should not save FPR pairs, got %d FPR pair stores", label, fprPairStores)
	}
}

func assertNumericEntryAvoidsCtxRegsReload(t *testing.T, code []byte, off int) {
	t.Helper()
	for pc := off; pc+4 <= len(code); pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		if isUnconditionalB(word) {
			return
		}
		if isLDRCtxRegsToMRegRegs(word) {
			t.Fatal("numeric entry should receive mRegRegs from the raw caller, not reload ctx.Regs")
		}
		if pc-off > 96 {
			t.Fatal("numeric entry did not branch within expected prologue window")
		}
	}
	t.Fatal("numeric entry prologue scan reached end of code")
}

func runForcedTier2ByName(t *testing.T, top *vm.FuncProto, fnName string, compileNames []string, args []runtime.Value) ([]runtime.Value, map[string]byte) {
	t.Helper()

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	for _, name := range compileNames {
		proto := findProtoByName(top, name)
		if proto == nil {
			t.Fatalf("compile target %q not found", name)
		}
		if err := tm.CompileTier2(proto); err != nil {
			t.Fatalf("CompileTier2(%s): %v", name, err)
		}
	}

	fn := v.GetGlobal(fnName)
	if fn.IsNil() {
		t.Fatalf("function %q not found in globals", fnName)
	}
	results, err := v.CallValue(fn, args)
	if err != nil {
		t.Fatalf("CallValue(%s): %v", fnName, err)
	}

	entered := make(map[string]byte, len(compileNames))
	for _, name := range compileNames {
		proto := findProtoByName(top, name)
		entered[name] = proto.EnteredTier2
	}
	return results, entered
}

func assertRawIntSelfResultsEqual(t *testing.T, label string, got, want []runtime.Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s result count=%d want %d; got=%v want=%v", label, len(got), len(want), got, want)
	}
	for i := range got {
		assertValuesEqual(t, label, got[i], want[i])
	}
}
