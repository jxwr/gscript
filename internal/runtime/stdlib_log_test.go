package runtime

import (
	"strings"
	"testing"
)

// logInterp creates an interpreter with the log library registered.
func logInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "log", buildLogLib())
}

// ==================================================================
// log level constants
// ==================================================================

func TestLogLevelConstants(t *testing.T) {
	interp := logInterp(t, `
		d := log.DEBUG
		i := log.INFO
		w := log.WARN
		e := log.ERROR
		f := log.FATAL
	`)
	if interp.GetGlobal("d").Int() != 0 { t.Error("DEBUG should be 0") }
	if interp.GetGlobal("i").Int() != 1 { t.Error("INFO should be 1") }
	if interp.GetGlobal("w").Int() != 2 { t.Error("WARN should be 2") }
	if interp.GetGlobal("e").Int() != 3 { t.Error("ERROR should be 3") }
	if interp.GetGlobal("f").Int() != 4 { t.Error("FATAL should be 4") }
}

// ==================================================================
// log.info / log.history tests
// ==================================================================

func TestLogInfo(t *testing.T) {
	interp := logInterp(t, `
		log.info("hello", "world")
		h := log.history()
		count := #h
		msg := h[1]
	`)
	if interp.GetGlobal("count").Int() != 1 { t.Error("expected 1 log entry") }
	msg := interp.GetGlobal("msg").Str()
	if !strings.Contains(msg, "[INFO]") {
		t.Errorf("expected [INFO] in message, got: %s", msg)
	}
	if !strings.Contains(msg, "hello world") {
		t.Errorf("expected 'hello world' in message, got: %s", msg)
	}
}

// ==================================================================
// log level filtering tests
// ==================================================================

func TestLogLevelFiltering(t *testing.T) {
	interp := logInterp(t, `
		log.setLevel(log.WARN)
		log.debug("should not appear")
		log.info("should not appear")
		log.warn("warning message")
		log.error("error message")
		count := log.count()
	`)
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected 2 log entries (warn+error), got %d", interp.GetGlobal("count").Int())
	}
}

func TestLogDebugLevel(t *testing.T) {
	interp := logInterp(t, `
		log.setLevel(log.DEBUG)
		log.debug("debug msg")
		log.info("info msg")
		count := log.count()
	`)
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected 2 log entries, got %d", interp.GetGlobal("count").Int())
	}
}

// ==================================================================
// log.setPrefix tests
// ==================================================================

func TestLogPrefix(t *testing.T) {
	interp := logInterp(t, `
		log.setPrefix("[MyApp]")
		log.info("started")
		h := log.history()
		msg := h[1]
	`)
	msg := interp.GetGlobal("msg").Str()
	if !strings.Contains(msg, "[MyApp]") {
		t.Errorf("expected prefix [MyApp], got: %s", msg)
	}
}

// ==================================================================
// log.setTimestamp / log.setShowLevel tests
// ==================================================================

func TestLogNoTimestamp(t *testing.T) {
	interp := logInterp(t, `
		log.setTimestamp(false)
		log.info("no time")
		h := log.history()
		msg := h[1]
	`)
	msg := interp.GetGlobal("msg").Str()
	// Without timestamp, message should start with [INFO]
	if !strings.HasPrefix(msg, "[INFO]") {
		t.Errorf("expected message to start with [INFO], got: %s", msg)
	}
}

func TestLogNoLevel(t *testing.T) {
	interp := logInterp(t, `
		log.setTimestamp(false)
		log.setShowLevel(false)
		log.info("plain message")
		h := log.history()
		msg := h[1]
	`)
	msg := interp.GetGlobal("msg").Str()
	if msg != "plain message" {
		t.Errorf("expected 'plain message', got: %s", msg)
	}
}

// ==================================================================
// log.getLevel tests
// ==================================================================

func TestLogGetLevel(t *testing.T) {
	interp := logInterp(t, `
		log.setLevel(log.ERROR)
		result := log.getLevel()
	`)
	if interp.GetGlobal("result").Int() != 3 {
		t.Errorf("expected ERROR(3), got %d", interp.GetGlobal("result").Int())
	}
}

// ==================================================================
// log.clear tests
// ==================================================================

func TestLogClear(t *testing.T) {
	interp := logInterp(t, `
		log.info("msg1")
		log.info("msg2")
		log.clear()
		count := log.count()
	`)
	if interp.GetGlobal("count").Int() != 0 {
		t.Errorf("expected 0 after clear, got %d", interp.GetGlobal("count").Int())
	}
}

// ==================================================================
// log.format tests
// ==================================================================

func TestLogFormat(t *testing.T) {
	interp := logInterp(t, `
		log.setTimestamp(false)
		result := log.format(log.WARN, "something", "happened")
	`)
	msg := interp.GetGlobal("result").Str()
	if !strings.Contains(msg, "[WARN]") {
		t.Errorf("expected [WARN], got: %s", msg)
	}
	if !strings.Contains(msg, "something happened") {
		t.Errorf("expected 'something happened', got: %s", msg)
	}
}

// ==================================================================
// log.fatal tests
// ==================================================================

func TestLogFatal(t *testing.T) {
	interp := logInterp(t, `
		log.fatal("critical failure")
		h := log.history()
		msg := h[1]
	`)
	msg := interp.GetGlobal("msg").Str()
	if !strings.Contains(msg, "[FATAL]") {
		t.Errorf("expected [FATAL], got: %s", msg)
	}
}

// ==================================================================
// log with multiple args
// ==================================================================

func TestLogMultipleArgs(t *testing.T) {
	interp := logInterp(t, `
		log.setTimestamp(false)
		log.setShowLevel(false)
		log.info("count:", 42, "status:", true)
		h := log.history()
		msg := h[1]
	`)
	msg := interp.GetGlobal("msg").Str()
	if msg != "count: 42 status: true" {
		t.Errorf("expected 'count: 42 status: true', got: %s", msg)
	}
}
