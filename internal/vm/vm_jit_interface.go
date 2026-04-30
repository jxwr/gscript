// vm_jit_interface.go exposes VM internals needed by the JIT deopt recovery
// path. These functions are separate from vm.go to keep that file under the
// 1000-line limit.

package vm

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
)

// ResumeFromPC resumes execution of the current top call frame starting at
// startPC. The VM's register file must already contain the execution state up
// to that point (written by the JIT before the deopt).
//
// Used by the baseline JIT overflow deopt path: instead of restarting at
// pc=0 (which replays side effects like table writes or SETGLOBAL exits that
// occurred before the overflowing instruction), the interpreter resumes at
// the exact bytecode where the int-spec guard fired.
//
// The caller must hold an active call frame (vm.frameCount > 0). The frame
// was pushed by vm.call before invoking vm.methodJIT.Execute; ResumeFromPC
// reuses it so no double-frame is created.
func (vm *VM) ResumeFromPC(startPC int) ([]runtime.Value, error) {
	if vm.frameCount == 0 {
		return nil, fmt.Errorf("ResumeFromPC: no active call frame")
	}
	vm.frames[vm.frameCount-1].pc = startPC
	return vm.run()
}

// TableGetForJIT exposes the VM's full table-get semantics to JIT slow paths.
// It includes non-table errors and __index metamethod dispatch, unlike
// runtime.Table.RawGet.
func (vm *VM) TableGetForJIT(table, key runtime.Value) (runtime.Value, error) {
	return vm.tableGet(table, key)
}
