package gscript

import "fmt"

// ErrorKind identifies the phase of GScript execution that produced an error.
type ErrorKind string

const (
	ErrLex     ErrorKind = "lex"
	ErrParse   ErrorKind = "parse"
	ErrRuntime ErrorKind = "runtime"
	ErrScript  ErrorKind = "script" // error() called from GScript
)

// Error is a structured error from GScript execution.
type Error struct {
	Kind    ErrorKind
	Message string
	Line    int
	Col     int
	File    string
	// Value holds the original GScript error value when Kind == ErrScript.
	// It may be a string, table, or any GScript value converted to interface{}.
	Value interface{}
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("[%s] %s:%d: %s", e.Kind, e.File, e.Line, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Kind, e.Message)
}

func wrapError(kind ErrorKind, err error) *Error {
	return &Error{Kind: kind, Message: err.Error()}
}
