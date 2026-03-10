package runtime

// LuaError represents an error raised by the error() built-in or other
// GScript runtime mechanisms. It wraps a Value so that error objects are
// not limited to strings.
type LuaError struct {
	Value Value // the error value (can be any type)
}

func (e *LuaError) Error() string {
	return e.Value.String()
}
