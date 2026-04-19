//go:build !rl
// +build !rl

// stdlib_rl_stub.go: default build stub for the raylib library.
// The real bindings live in stdlib_rl.go (gated behind `-tags rl`).
// Building without the tag avoids pulling in raylib-go's bundled
// C sources whose tautological-compare warnings pollute every
// build. Scripts that try to use `rl.*` on the stub build get
// a runtime error ("raylib support not compiled in").

package runtime

func rlLib(interp *Interpreter) *Table {
	t := NewTable()
	// Expose a single `_stub` field so callers can detect absence of
	// the real lib at script level (if anyone needs to).
	t.RawSetString("_stub", BoolValue(true))
	return t
}
