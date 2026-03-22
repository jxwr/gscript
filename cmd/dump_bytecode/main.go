package main

import (
	"fmt"
	"os"
	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/vm"
)

func main() {
	src, _ := os.ReadFile(os.Args[1])
	tokens, _ := lexer.New(string(src)).Tokenize()
	prog, _ := parser.New(tokens).Parse()
	proto, _ := vm.Compile(prog)
	for _, child := range proto.Protos {
		fmt.Printf("=== %s (params=%d, maxstack=%d) ===\n", child.Name, child.NumParams, child.MaxStack)
		fmt.Println(vm.Disassemble(child))
	}
}
