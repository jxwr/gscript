//go:build darwin && arm64

package methodjit

import (
	"testing"
	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/vm"
)

func TestDumpBytecodes2(t *testing.T) {
	programs := map[string]string{
		"add_one": `
func add_one(x) {
    return x + 1
}
result := 0
for i := 1; i <= 100000; i++ {
    result = add_one(result)
}
`,
	}

	for name, src := range programs {
		t.Run(name, func(t *testing.T) {
			tokens, err := lexer.New(src).Tokenize()
			if err != nil {
				t.Fatalf("lexer: %v", err)
			}
			prog, err := parser.New(tokens).Parse()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			proto, err := vm.Compile(prog)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			
			t.Logf("=== %s main ===", name)
			t.Log(vm.Disassemble(proto))
			
			for i, sub := range proto.Protos {
				t.Logf("=== %s sub[%d] ===", name, i)
				t.Log(vm.Disassemble(sub))
			}
		})
	}
}
