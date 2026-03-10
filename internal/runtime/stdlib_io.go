package runtime

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// buildIOLib creates the "io" standard library table.
func buildIOLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "io." + name,
			Fn:   fn,
		}))
	}

	// io.write(...) -- write to stdout (no newline)
	set("write", func(args []Value) ([]Value, error) {
		for _, a := range args {
			fmt.Print(a.String())
		}
		return nil, nil
	})

	// io.read([fmt]) -- read from stdin
	// "*l" (or "l") → read a line (default)
	// "*n" (or "n") → read a number
	// "*a" (or "a") → read all remaining input
	set("read", func(args []Value) ([]Value, error) {
		format := "*l"
		if len(args) >= 1 && args[0].IsString() {
			format = args[0].Str()
		}

		reader := bufio.NewReader(os.Stdin)

		switch format {
		case "*l", "l":
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, err
			}
			line = strings.TrimRight(line, "\n\r")
			return []Value{StringValue(line)}, nil
		case "*n", "n":
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, err
			}
			line = strings.TrimSpace(line)
			if i, err := strconv.ParseInt(line, 10, 64); err == nil {
				return []Value{IntValue(i)}, nil
			}
			if f, err := strconv.ParseFloat(line, 64); err == nil {
				return []Value{FloatValue(f)}, nil
			}
			return []Value{NilValue()}, nil
		case "*a", "a":
			data, err := io.ReadAll(reader)
			if err != nil {
				return nil, err
			}
			return []Value{StringValue(string(data))}, nil
		default:
			return nil, fmt.Errorf("bad argument to 'io.read' (invalid format '%s')", format)
		}
	})

	// io.lines([filename]) -> iterator
	set("lines", func(args []Value) ([]Value, error) {
		var scanner *bufio.Scanner
		var file *os.File

		if len(args) >= 1 && args[0].IsString() {
			var err error
			file, err = os.Open(args[0].Str())
			if err != nil {
				return nil, fmt.Errorf("cannot open '%s': %s", args[0].Str(), err)
			}
			scanner = bufio.NewScanner(file)
		} else {
			scanner = bufio.NewScanner(os.Stdin)
		}

		iter := &GoFunction{
			Name: "io.lines_iterator",
			Fn: func(_ []Value) ([]Value, error) {
				if scanner.Scan() {
					return []Value{StringValue(scanner.Text())}, nil
				}
				if file != nil {
					file.Close()
				}
				if err := scanner.Err(); err != nil {
					return nil, err
				}
				return []Value{NilValue()}, nil
			},
		}
		return []Value{FunctionValue(iter)}, nil
	})

	// io.open(filename, mode) -> file table, err
	set("open", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'io.open' (string expected)")
		}
		filename := args[0].Str()
		mode := "r"
		if len(args) >= 2 && args[1].IsString() {
			mode = args[1].Str()
		}

		var flag int
		switch mode {
		case "r":
			flag = os.O_RDONLY
		case "w":
			flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		case "a":
			flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		case "r+":
			flag = os.O_RDWR
		case "w+":
			flag = os.O_RDWR | os.O_CREATE | os.O_TRUNC
		case "a+":
			flag = os.O_RDWR | os.O_CREATE | os.O_APPEND
		default:
			return []Value{NilValue(), StringValue(fmt.Sprintf("invalid mode: %s", mode))}, nil
		}

		file, err := os.OpenFile(filename, flag, 0644)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}

		ft := buildFileTable(file)
		return []Value{TableValue(ft)}, nil
	})

	return t
}

// buildFileTable creates a table representing a file object with read/write/close/lines methods.
func buildFileTable(file *os.File) *Table {
	ft := NewTable()
	reader := bufio.NewReader(file)

	ft.RawSet(StringValue("read"), FunctionValue(&GoFunction{
		Name: "file:read",
		Fn: func(args []Value) ([]Value, error) {
			// First arg is self (the file table), second is format
			format := "*l"
			if len(args) >= 2 && args[1].IsString() {
				format = args[1].Str()
			}
			switch format {
			case "*l", "l":
				line, err := reader.ReadString('\n')
				if err != nil && err != io.EOF {
					return nil, err
				}
				if len(line) == 0 && err == io.EOF {
					return []Value{NilValue()}, nil
				}
				line = strings.TrimRight(line, "\n\r")
				return []Value{StringValue(line)}, nil
			case "*a", "a":
				data, err := io.ReadAll(reader)
				if err != nil {
					return nil, err
				}
				return []Value{StringValue(string(data))}, nil
			case "*n", "n":
				line, err := reader.ReadString('\n')
				if err != nil && err != io.EOF {
					return nil, err
				}
				line = strings.TrimSpace(line)
				if i, err := strconv.ParseInt(line, 10, 64); err == nil {
					return []Value{IntValue(i)}, nil
				}
				if f, err := strconv.ParseFloat(line, 64); err == nil {
					return []Value{FloatValue(f)}, nil
				}
				return []Value{NilValue()}, nil
			default:
				return nil, fmt.Errorf("invalid format: %s", format)
			}
		},
	}))

	ft.RawSet(StringValue("write"), FunctionValue(&GoFunction{
		Name: "file:write",
		Fn: func(args []Value) ([]Value, error) {
			// First arg is self
			for _, a := range args[1:] {
				_, err := file.WriteString(a.String())
				if err != nil {
					return nil, err
				}
			}
			return []Value{TableValue(ft)}, nil
		},
	}))

	ft.RawSet(StringValue("close"), FunctionValue(&GoFunction{
		Name: "file:close",
		Fn: func(args []Value) ([]Value, error) {
			err := file.Close()
			if err != nil {
				return []Value{NilValue(), StringValue(err.Error())}, nil
			}
			return []Value{BoolValue(true)}, nil
		},
	}))

	ft.RawSet(StringValue("lines"), FunctionValue(&GoFunction{
		Name: "file:lines",
		Fn: func(args []Value) ([]Value, error) {
			scanner := bufio.NewScanner(file)
			iter := &GoFunction{
				Name: "file:lines_iterator",
				Fn: func(_ []Value) ([]Value, error) {
					if scanner.Scan() {
						return []Value{StringValue(scanner.Text())}, nil
					}
					if err := scanner.Err(); err != nil {
						return nil, err
					}
					return []Value{NilValue()}, nil
				},
			}
			return []Value{FunctionValue(iter)}, nil
		},
	}))

	return ft
}
