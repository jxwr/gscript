package main

import (
	"fmt"
	"os"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/vm"
)

func main() {
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	l := lexer.New(string(src))
	tokens, err := l.Tokenize()
	if err != nil {
		panic(err)
	}
	p := parser.New(tokens)
	prog, err := p.Parse()
	if err != nil {
		panic(err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		panic(err)
	}
	name := os.Args[2]
	for _, child := range proto.Protos {
		if child.Name == name {
			fmt.Println(vm.Disassemble(child))
			return
		}
	}
	fmt.Println(name, "not found")
}
