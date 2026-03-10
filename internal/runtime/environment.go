package runtime

// Environment represents a lexical scope. Each scope holds its own variables
// and a pointer to the enclosing (parent) scope. Variables are stored as
// Upvalue pointers so that closures can share mutable state with their
// defining scope.
type Environment struct {
	vars   map[string]*Upvalue
	parent *Environment
}

// NewEnvironment creates a new scope with the given parent (nil for global).
func NewEnvironment(parent *Environment) *Environment {
	return &Environment{
		vars:   make(map[string]*Upvalue),
		parent: parent,
	}
}

// Get looks up a variable by walking the scope chain.
func (e *Environment) Get(name string) (Value, bool) {
	uv, ok := e.vars[name]
	if ok {
		return uv.Get(), true
	}
	if e.parent != nil {
		return e.parent.Get(name)
	}
	return NilValue(), false
}

// Set assigns to an existing variable somewhere in the scope chain.
// Returns false if the variable was not found in any scope.
func (e *Environment) Set(name string, val Value) bool {
	uv, ok := e.vars[name]
	if ok {
		uv.Set(val)
		return true
	}
	if e.parent != nil {
		return e.parent.Set(name, val)
	}
	return false
}

// Define creates a new variable in the current scope (shadows any outer binding).
func (e *Environment) Define(name string, val Value) {
	v := val // make a local copy so the Upvalue points at stable storage
	e.vars[name] = NewUpvalue(&v)
}

// GetUpvalue returns the *Upvalue for a name in the scope chain, for closure capture.
func (e *Environment) GetUpvalue(name string) (*Upvalue, bool) {
	uv, ok := e.vars[name]
	if ok {
		return uv, true
	}
	if e.parent != nil {
		return e.parent.GetUpvalue(name)
	}
	return nil, false
}

// DefineUpvalue adds an existing *Upvalue to this scope (sharing the same pointer).
// This allows closures to share mutable references to captured variables.
func (e *Environment) DefineUpvalue(name string, uv *Upvalue) {
	e.vars[name] = uv
}
