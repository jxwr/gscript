package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

func init() {
	goruntime.LockOSThread() // Required for GLFW/OpenGL on macOS
}

func main() {
	// Flags
	eval := flag.String("e", "", "execute string")
	flag.Parse()

	interp := runtime.New()

	if *eval != "" {
		// Execute string
		if err := runString(interp, *eval); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		// REPL mode
		runREPL(interp)
		return
	}

	// Execute file
	filename := args[0]

	// Set os.args global
	osArgs := runtime.NewTable()
	osArgs.RawSet(runtime.IntValue(0), runtime.StringValue(filename))
	for i, arg := range args[1:] {
		osArgs.RawSet(runtime.IntValue(int64(i+1)), runtime.StringValue(arg))
	}
	interp.SetGlobal("arg", runtime.TableValue(osArgs))

	// Set script directory for require
	absPath, _ := filepath.Abs(filename)
	interp.SetScriptDir(filepath.Dir(absPath))

	if err := runFile(interp, filename); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filename, err)
		os.Exit(1)
	}
}

func runFile(interp *runtime.Interpreter, filename string) error {
	src, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return runString(interp, string(src))
}

func runString(interp *runtime.Interpreter, src string) error {
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return fmt.Errorf("lexer error: %w", err)
	}

	prog, err := parser.New(tokens).Parse()
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	return interp.Exec(prog)
}

func runREPL(interp *runtime.Interpreter) {
	fmt.Println("GScript REPL (type 'exit' to quit)")
	scanner := bufio.NewScanner(os.Stdin)
	buf := ""

	for {
		if buf == "" {
			fmt.Print("> ")
		} else {
			fmt.Print(">> ")
		}

		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if line == "exit" || line == "quit" {
			break
		}

		buf += line + "\n"

		// Try to execute
		err := runString(interp, buf)
		if err != nil {
			// Show error and reset buffer
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		buf = ""
	}
}
