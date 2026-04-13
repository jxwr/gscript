//go:build darwin && arm64

// testhelpers_test.go holds small package-scope helpers shared by the
// methodjit test suite. Separated from larger test files so archiving or
// reworking any individual test doesn't pull these helpers out with it.

package methodjit

import "github.com/gscript/gscript/internal/vm"

// findProtoByName walks a proto tree depth-first and returns the first
// proto whose Name matches. Returns nil if not found.
func findProtoByName(top *vm.FuncProto, name string) *vm.FuncProto {
	if top == nil {
		return nil
	}
	if top.Name == name {
		return top
	}
	for _, p := range top.Protos {
		if got := findProtoByName(p, name); got != nil {
			return got
		}
	}
	return nil
}
