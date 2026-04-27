//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestCallABIAnnotate_GCDBenchCallGetsDescriptor(t *testing.T) {
	src := `func gcd(a, b) {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}
func gcd_bench(n) {
	total := 0
	for i := 1; i <= n; i++ {
		for j := 1; j <= 3; j++ {
			total = total + gcd(i * 7 + 13, j * 11 + 3)
		}
	}
	return total
}`
	top := compileTop(t, src)
	gcd := findProtoByName(top, "gcd")
	caller := findProtoByName(top, "gcd_bench")
	if gcd == nil || caller == nil {
		t.Fatalf("missing protos: gcd=%v caller=%v", gcd != nil, caller != nil)
	}
	assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(gcd), 2)

	fn := BuildGraph(caller)
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals: map[string]*vm.FuncProto{"gcd": gcd},
		InlineMaxSize: 1,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(gcd_bench): %v", err)
	}

	call := singleCallTo(t, fn, "gcd", map[string]*vm.FuncProto{"gcd": gcd})
	desc, ok := fn.CallABIs[call.ID]
	if !ok {
		t.Fatalf("call %d missing CallABI descriptor\nIR:\n%s", call.ID, Print(fn))
	}
	if call.Type != TypeInt {
		t.Fatalf("call Type=%s, want int", call.Type)
	}
	if desc.Callee != gcd || desc.NumArgs != 2 || desc.NumRets != 1 || !desc.RawIntReturn {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}
	if len(desc.RawIntParams) != 2 || !desc.RawIntParams[0] || !desc.RawIntParams[1] {
		t.Fatalf("RawIntParams=%v, want [true true]", desc.RawIntParams)
	}
}

func TestCallABIAnnotate_StableGlobalWithoutInlineGlobals(t *testing.T) {
	src := `dummy := 1
func inc(n) { return n + 1 }
result := inc(41)`
	top := compileTop(t, src)
	inc := findProtoByName(top, "inc")
	if inc == nil {
		t.Fatal("inc proto not found")
	}

	fn := BuildGraph(top)
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{InlineMaxSize: 1})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(<main>): %v", err)
	}

	call := singleCallTo(t, fn, "inc", map[string]*vm.FuncProto{"inc": inc})
	if _, ok := fn.CallABIs[call.ID]; !ok {
		t.Fatalf("stable global call %d missing CallABI descriptor\nIR:\n%s", call.ID, Print(fn))
	}
	if call.Type != TypeInt {
		t.Fatalf("call Type=%s, want int", call.Type)
	}
}

func TestCallABIAnnotate_NegativeCases(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		caller string
		callee string
		mutate func(*Instr)
	}{
		{
			name: "unresolved",
			src: `func caller(x) {
	y := missing(x)
	return y + 1
}`,
			caller: "caller",
		},
		{
			name: "non int actual",
			src: `func inc(n) { return n + 1 }
func caller(x) {
	y := inc(1.5)
	return y + x
}`,
			caller: "caller",
			callee: "inc",
		},
		{
			name: "multiple returns",
			src: `func inc(n) { return n + 1 }
func caller(x) {
	y := inc(x)
	return y + 1
}`,
			caller: "caller",
			callee: "inc",
			mutate: func(call *Instr) {
				call.Aux2 = 3
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top := compileTop(t, tt.src)
			caller := findProtoByName(top, tt.caller)
			if caller == nil {
				t.Fatalf("caller %q not found", tt.caller)
			}
			globals := make(map[string]*vm.FuncProto)
			if tt.callee != "" {
				callee := findProtoByName(top, tt.callee)
				if callee == nil {
					t.Fatalf("callee %q not found", tt.callee)
				}
				globals[tt.callee] = callee
			}

			fn := BuildGraph(caller)
			var err error
			fn, err = TypeSpecializePass(fn)
			if err != nil {
				t.Fatalf("TypeSpecializePass: %v", err)
			}
			call := firstCall(t, fn)
			if tt.mutate != nil {
				tt.mutate(call)
			}
			fn = AnnotateCallABIs(fn, CallABIAnnotationConfig{Globals: globals})
			if len(fn.CallABIs) != 0 {
				t.Fatalf("unexpected descriptors: %+v\nIR:\n%s", fn.CallABIs, Print(fn))
			}
			if call.Type == TypeInt {
				t.Fatalf("negative call Type=%s, want non-int", call.Type)
			}
		})
	}
}

func firstCall(t *testing.T, fn *Function) *Instr {
	t.Helper()
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpCall {
				return instr
			}
		}
	}
	t.Fatal("no OpCall found")
	return nil
}

func singleCallTo(t *testing.T, fn *Function, name string, globals map[string]*vm.FuncProto) *Instr {
	t.Helper()
	var out *Instr
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			gotName, _ := resolveCallee(instr, fn, InlineConfig{Globals: globals})
			if gotName != name {
				continue
			}
			if out != nil {
				t.Fatalf("multiple calls to %s found", name)
			}
			out = instr
		}
	}
	if out == nil {
		t.Fatalf("no call to %s found\nIR:\n%s", name, Print(fn))
	}
	return out
}
