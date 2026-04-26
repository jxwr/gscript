package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/pprof"
	"sort"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	bytecodevm "github.com/gscript/gscript/internal/vm"
)

// jitStatsReporter is implemented by the platform-specific JIT engine wrapper
// so the CLI can print tier statistics after execution.
type jitStatsReporter interface {
	PrintStats(w *os.File)
	PrintExitStats(w *os.File)
	PrintExitStatsJSON(w *os.File) error
	Close() error
}

type jitCLIOptions struct {
	TimelinePath      string
	TimelineFormat    string
	WarmDumpDir       string
	WarmDumpProto     string
	ShowExitStats     bool
	ShowExitStatsJSON bool
}

type jitWarmDumpController interface {
	EnableWarmDump(dir, protoName string) error
	WriteWarmDump(top *bytecodevm.FuncProto) error
}

// sortStrings is a tiny helper shared with platform files to keep them from
// each importing "sort".
func sortStrings(s []string) { sort.Strings(s) }

func init() {
	goruntime.LockOSThread() // Required for GLFW/OpenGL on macOS
}

func main() {
	// Flags
	eval := flag.String("e", "", "execute string")
	useVM := flag.Bool("vm", false, "use bytecode VM without JIT")
	useJIT := flag.Bool("jit", true, "use bytecode VM with JIT compilation (default)")
	cpuprofile := flag.String("cpuprofile", "", "write CPU profile to file")
	memprofile := flag.String("memprofile", "", "write memory profile to file")
	jitStats := flag.Bool("jit-stats", false, "print JIT tier statistics after execution")
	jitTimeline := flag.String("jit-timeline", "", "write production JIT event timeline to file ('-' for stderr)")
	jitTimelineFormat := flag.String("jit-timeline-format", "jsonl", "JIT timeline format: jsonl or json")
	jitDumpWarm := flag.String("jit-dump-warm", "", "write warm production Tier 2 diagnostic dump to directory")
	jitDumpProto := flag.String("jit-dump-proto", "", "limit -jit-dump-warm to a proto name")
	exitStats := flag.Bool("exit-stats", false, "print Tier 2 exit/deopt profile after execution")
	exitStatsJSON := flag.Bool("exit-stats-json", false, "print Tier 2 exit/deopt profile as JSON after execution")
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
		if err := runFileVM(interp, filename, *useJIT, *jitStats, jitCLIOptions{
			TimelinePath:      *jitTimeline,
			TimelineFormat:    *jitTimelineFormat,
			WarmDumpDir:       *jitDumpWarm,
			WarmDumpProto:     *jitDumpProto,
			ShowExitStats:     *exitStats,
			ShowExitStatsJSON: *exitStatsJSON,
		}); err != nil {
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

func runFileVM(interp *runtime.Interpreter, filename string, jit bool, showJITStats bool, jitOpts jitCLIOptions) error {
	src, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return runStringVM(interp, string(src), jit, showJITStats, jitOpts)
}

func runStringVM(interp *runtime.Interpreter, src string, jit bool, showJITStats bool, jitOpts jitCLIOptions) error {
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
	var reporter jitStatsReporter
	if !jit && jitOpts.TimelinePath != "" {
		return fmt.Errorf("JIT timeline requires JIT to be enabled")
	}
	if !jit && jitOpts.WarmDumpDir != "" {
		return fmt.Errorf("-jit-dump-warm requires -jit")
	}
	var warmDumper jitWarmDumpController
	if jit {
		reporter, err = cliEnableJIT(bvm, jitOpts)
		if err != nil {
			return err
		}
		if reporter != nil {
			warmDumper, _ = reporter.(jitWarmDumpController)
		}
	}
	if jitOpts.WarmDumpDir != "" {
		if warmDumper == nil {
			return fmt.Errorf("-jit-dump-warm requires the darwin/arm64 method JIT")
		}
		if err := warmDumper.EnableWarmDump(jitOpts.WarmDumpDir, jitOpts.WarmDumpProto); err != nil {
			return err
		}
	}
	_, err = bvm.Execute(proto)
	var dumpErr error
	if jitOpts.WarmDumpDir != "" && warmDumper != nil {
		dumpErr = warmDumper.WriteWarmDump(proto)
	}
	var closeErr error
	if reporter != nil {
		closeErr = reporter.Close()
	}
	if showJITStats {
		if reporter != nil {
			reporter.PrintStats(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr, "JIT Statistics: JIT disabled or unavailable on this platform")
		}
	}
	var statsErr error
	if jitOpts.ShowExitStats {
		if reporter != nil {
			reporter.PrintExitStats(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr, "Tier 2 Exit Profile: JIT disabled or unavailable on this platform")
		}
	}
	if jitOpts.ShowExitStatsJSON {
		if reporter != nil {
			statsErr = reporter.PrintExitStatsJSON(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr, `{"error":"JIT disabled or unavailable on this platform"}`)
		}
	}
	if err != nil {
		if dumpErr != nil {
			return fmt.Errorf("%w; warm dump failed: %v", err, dumpErr)
		}
		if closeErr != nil {
			return fmt.Errorf("%w; JIT close failed: %v", err, closeErr)
		}
		if statsErr != nil {
			return fmt.Errorf("%w; exit stats failed: %v", err, statsErr)
		}
		return err
	}
	if dumpErr != nil {
		return dumpErr
	}
	if closeErr != nil {
		return closeErr
	}
	if statsErr != nil {
		return statsErr
	}
	return nil
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
