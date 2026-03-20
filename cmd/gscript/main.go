package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/pprof"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

func init() {
	goruntime.LockOSThread() // Required for GLFW/OpenGL on macOS
}

func main() {
	// Flags
	eval := flag.String("e", "", "execute string")
	useVM := flag.Bool("vm", false, "use bytecode VM without JIT")
	useJIT := flag.Bool("jit", true, "use bytecode VM with JIT compilation (default)")
	useTrace := flag.Bool("trace", false, "enable tracing JIT (implies -vm)")
	cpuprofile := flag.String("cpuprofile", "", "write CPU profile to file")
	memprofile := flag.String("memprofile", "", "write memory profile to file")
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not create CPU profile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	if *memprofile != "" {
		defer func() {
			f, err := os.Create(*memprofile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not create memory profile: %v\n", err)
				return
			}
			defer f.Close()
			goruntime.GC()
			pprof.WriteHeapProfile(f)
		}()
	}

	// Determine which flags were explicitly set by the user.
	vmExplicit := false
	jitExplicit := false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "vm":
			vmExplicit = true
		case "jit":
			jitExplicit = true
		}
	})

	// -vm without -jit means "VM only, no JIT".
	if vmExplicit && !jitExplicit {
		*useJIT = false
	}
	if *useJIT {
		*useVM = true
	}
	if *useTrace {
		*useVM = true
	}

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

	if *useVM {
		if err := runFileVM(interp, filename, *useJIT, *useTrace); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filename, err)
			os.Exit(1)
		}
	} else {
		if err := runFile(interp, filename); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filename, err)
			os.Exit(1)
		}
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

func runFileVM(interp *runtime.Interpreter, filename string, jit bool, trace bool) error {
	src, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return runStringVM(interp, string(src), jit, trace)
}

func runStringVM(interp *runtime.Interpreter, src string, jit bool, trace bool) error {
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		return fmt.Errorf("lexer error: %w", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}
	proto, err := bytecodevm.Compile(prog)
	if err != nil {
		return fmt.Errorf("compile error: %w", err)
	}
	globals := interp.ExportGlobals()
	bvm := bytecodevm.New(globals)
	bvm.SetStringMeta(interp.StringMeta())
	if jit {
		cliEnableJIT(bvm)
	}
	if trace {
		cliEnableTracing(bvm)
	}
	_, err = bvm.Execute(proto)
	return err
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
