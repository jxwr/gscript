package main

import (
	"fmt"
	"os"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/vm"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dump_bytecode <file.gs>")
		os.Exit(1)
	}
	src, _ := os.ReadFile(os.Args[1])
	tokens, _ := lexer.New(string(src)).Tokenize()
	prog, _ := parser.New(tokens).Parse()
	proto, _ := vm.Compile(prog)
	fmt.Println("=== <main> ===")
	fmt.Println(vm.Disassemble(proto))
	for _, child := range proto.Protos {
		fmt.Printf("=== %s (params=%d, maxstack=%d) ===\n", child.Name, child.NumParams, child.MaxStack)
		fmt.Println(vm.Disassemble(child))
	}
}
