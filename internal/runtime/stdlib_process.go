package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// buildProcessLib creates the "process" standard library table.
func buildProcessLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "process." + name,
			Fn:   fn,
		}))
	}

	// process.run(cmd [, opts]) -- run command, return {ok, stdout, stderr, code}
	// opts: {stdin=str, env={}, dir=str, timeout=seconds}
	// cmd can be a string (split by spaces) or a table of args
	set("run", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'process.run'")
		}

		var cmdArgs []string
		if args[0].IsString() {
			cmdArgs = strings.Fields(args[0].Str())
		} else if args[0].IsTable() {
			tbl := args[0].Table()
			length := tbl.Length()
			for i := int64(1); i <= int64(length); i++ {
				cmdArgs = append(cmdArgs, tbl.RawGet(IntValue(i)).String())
			}
		} else {
			return nil, fmt.Errorf("bad argument #1 to 'process.run' (string or table expected)")
		}

		if len(cmdArgs) == 0 {
			return nil, fmt.Errorf("process.run: empty command")
		}

		var stdinStr string
		var envVars []string
		var dir string
		var timeout time.Duration

		if len(args) >= 2 && args[1].IsTable() {
			opts := args[1].Table()
			if v := opts.RawGet(StringValue("stdin")); v.IsString() {
				stdinStr = v.Str()
			}
			if v := opts.RawGet(StringValue("dir")); v.IsString() {
				dir = v.Str()
			}
			if v := opts.RawGet(StringValue("timeout")); v.IsNumber() {
				timeout = time.Duration(toFloat(v) * float64(time.Second))
			}
			if v := opts.RawGet(StringValue("env")); v.IsTable() {
				envTbl := v.Table()
				k, val, ok := envTbl.Next(NilValue())
				for ok {
					envVars = append(envVars, k.String()+"="+val.String())
					k, val, ok = envTbl.Next(k)
				}
			}
		}

		var ctx context.Context
		var cancel context.CancelFunc
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), timeout)
			defer cancel()
		} else {
			ctx = context.Background()
		}

		cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if stdinStr != "" {
			cmd.Stdin = strings.NewReader(stdinStr)
		}
		if dir != "" {
			cmd.Dir = dir
		}
		if len(envVars) > 0 {
			cmd.Env = append(os.Environ(), envVars...)
		}

		err := cmd.Run()
		exitCode := 0
		ok := true
		if err != nil {
			ok = false
			if exitErr, isExit := err.(*exec.ExitError); isExit {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}

		result := NewTable()
		result.RawSet(StringValue("ok"), BoolValue(ok))
		result.RawSet(StringValue("stdout"), StringValue(stdout.String()))
		result.RawSet(StringValue("stderr"), StringValue(stderr.String()))
		result.RawSet(StringValue("code"), IntValue(int64(exitCode)))

		return []Value{TableValue(result)}, nil
	})

	// process.exec(cmd, ...) -- run command with args, return stdout string or nil,err
	set("exec", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'process.exec' (string expected)")
		}
		cmdArgs := make([]string, len(args))
		for i, a := range args {
			cmdArgs[i] = a.String()
		}
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		out, err := cmd.Output()
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{StringValue(string(out))}, nil
	})

	// process.shell(cmd) -- run via shell (/bin/sh -c), return {ok, stdout, stderr, code}
	set("shell", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'process.shell' (string expected)")
		}
		cmd := exec.Command("/bin/sh", "-c", args[0].Str())
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		exitCode := 0
		ok := true
		if err != nil {
			ok = false
			if exitErr, isExit := err.(*exec.ExitError); isExit {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}

		result := NewTable()
		result.RawSet(StringValue("ok"), BoolValue(ok))
		result.RawSet(StringValue("stdout"), StringValue(stdout.String()))
		result.RawSet(StringValue("stderr"), StringValue(stderr.String()))
		result.RawSet(StringValue("code"), IntValue(int64(exitCode)))

		return []Value{TableValue(result)}, nil
	})

	// process.which(name) -- find executable in PATH, return path or nil
	set("which", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'process.which' (string expected)")
		}
		path, err := exec.LookPath(args[0].Str())
		if err != nil {
			return []Value{NilValue()}, nil
		}
		return []Value{StringValue(path)}, nil
	})

	// process.pid() -- current process ID
	set("pid", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(os.Getpid()))}, nil
	})

	// process.env() -- return table of all environment variables
	set("env", func(args []Value) ([]Value, error) {
		tbl := NewTable()
		for _, e := range os.Environ() {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				tbl.RawSet(StringValue(parts[0]), StringValue(parts[1]))
			}
		}
		return []Value{TableValue(tbl)}, nil
	})

	return t
}
