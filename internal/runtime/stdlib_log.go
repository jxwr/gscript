package runtime

import (
	"fmt"
	"strings"
	"time"
)

// Log levels
const (
	logLevelDebug = 0
	logLevelInfo  = 1
	logLevelWarn  = 2
	logLevelError = 3
	logLevelFatal = 4
)

// buildLogLib creates the "log" standard library table.
// Provides structured logging with levels, formatting, and output capture.
// Inspired by Odin's log package and common logging libraries.
func buildLogLib() *Table {
	t := NewTable()

	// Mutable log state captured by closures
	currentLevel := logLevelInfo
	var logOutput []string // captured output for testing/programmatic access
	prefix := ""
	showTimestamp := true
	showLevel := true

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "log." + name,
			Fn:   fn,
		}))
	}

	// Level constants
	t.RawSet(StringValue("DEBUG"), IntValue(logLevelDebug))
	t.RawSet(StringValue("INFO"), IntValue(logLevelInfo))
	t.RawSet(StringValue("WARN"), IntValue(logLevelWarn))
	t.RawSet(StringValue("ERROR"), IntValue(logLevelError))
	t.RawSet(StringValue("FATAL"), IntValue(logLevelFatal))

	levelName := func(level int) string {
		switch level {
		case logLevelDebug:
			return "DEBUG"
		case logLevelInfo:
			return "INFO"
		case logLevelWarn:
			return "WARN"
		case logLevelError:
			return "ERROR"
		case logLevelFatal:
			return "FATAL"
		default:
			return "UNKNOWN"
		}
	}

	// Format a log message with timestamp, level, prefix
	formatMsg := func(level int, args []Value) string {
		var parts []string
		if showTimestamp {
			parts = append(parts, time.Now().Format("2006-01-02 15:04:05"))
		}
		if showLevel {
			parts = append(parts, fmt.Sprintf("[%s]", levelName(level)))
		}
		if prefix != "" {
			parts = append(parts, prefix)
		}
		// Join args with spaces
		var msgParts []string
		for _, a := range args {
			msgParts = append(msgParts, a.String())
		}
		parts = append(parts, strings.Join(msgParts, " "))
		return strings.Join(parts, " ")
	}

	doLog := func(level int, args []Value) {
		if level < currentLevel {
			return
		}
		msg := formatMsg(level, args)
		logOutput = append(logOutput, msg)
		fmt.Println(msg)
	}

	// log.setLevel(level) - set minimum log level
	set("setLevel", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'log.setLevel' (number expected)")
		}
		currentLevel = int(toInt(args[0]))
		return nil, nil
	})

	// log.getLevel() -> current log level
	set("getLevel", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(currentLevel))}, nil
	})

	// log.setPrefix(str) - set log prefix
	set("setPrefix", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'log.setPrefix' (string expected)")
		}
		prefix = args[0].Str()
		return nil, nil
	})

	// log.setTimestamp(bool) - enable/disable timestamp
	set("setTimestamp", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'log.setTimestamp' (boolean expected)")
		}
		showTimestamp = args[0].Truthy()
		return nil, nil
	})

	// log.setShowLevel(bool) - enable/disable level display
	set("setShowLevel", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'log.setShowLevel' (boolean expected)")
		}
		showLevel = args[0].Truthy()
		return nil, nil
	})

	// log.debug(args...) - log at DEBUG level
	set("debug", func(args []Value) ([]Value, error) {
		doLog(logLevelDebug, args)
		return nil, nil
	})

	// log.info(args...) - log at INFO level
	set("info", func(args []Value) ([]Value, error) {
		doLog(logLevelInfo, args)
		return nil, nil
	})

	// log.warn(args...) - log at WARN level
	set("warn", func(args []Value) ([]Value, error) {
		doLog(logLevelWarn, args)
		return nil, nil
	})

	// log.error(args...) - log at ERROR level
	set("error", func(args []Value) ([]Value, error) {
		doLog(logLevelError, args)
		return nil, nil
	})

	// log.fatal(args...) - log at FATAL level
	set("fatal", func(args []Value) ([]Value, error) {
		doLog(logLevelFatal, args)
		return nil, nil
	})

	// log.history() -> table of all log messages (array)
	set("history", func(args []Value) ([]Value, error) {
		result := NewTable()
		for i, msg := range logOutput {
			result.RawSet(IntValue(int64(i+1)), StringValue(msg))
		}
		return []Value{TableValue(result)}, nil
	})

	// log.clear() - clear the log history
	set("clear", func(args []Value) ([]Value, error) {
		logOutput = nil
		return nil, nil
	})

	// log.count() -> int, number of logged messages
	set("count", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(len(logOutput)))}, nil
	})

	// log.format(level, args...) -> string (format without printing)
	set("format", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'log.format' (level expected)")
		}
		level := int(toInt(args[0]))
		msg := formatMsg(level, args[1:])
		return []Value{StringValue(msg)}, nil
	})

	return t
}
