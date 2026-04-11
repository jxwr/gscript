//go:build darwin && arm64

// main_test.go provides TestMain for the methodjit package test binary.
//
// Root cause of crashes in deep-recursion tests:
//
// JIT code is ARM64 machine code without Go frame metadata (pclntab entries).
// When Go's async-preemption signal (SIGURG) fires while JIT native call
// frames are on the goroutine stack, the runtime injects asyncPreempt() at
// the current PC. The injected frame calls the stack unwinder, which looks up
// _func for the JIT PC, gets nil (not in pclntab), and dereferences nil+0x110:
//
//   - "fatal error: traceback did not unwind completely"   (scheduler preempt)
//   - "fatal error: semasleep on Darwin signal stack"       (GC scan)
//
// Fix: re-exec the test binary with both GODEBUG=asyncpreemptoff=1 and
// GOGC=off set at process startup (before the Go runtime initialises).
// These settings cannot be applied from inside TestMain — they must be
// present when the runtime starts. This is the standard Go pattern for
// startup-only settings (used by os/exec tests, etc.).
//
//   GODEBUG=asyncpreemptoff=1 — disables async preemption (SIGURG injection
//     of asyncPreempt). With this, goroutines are only preempted at
//     cooperative yield points, which all occur after jit.CallJIT() returns
//     (when only Go frames are on the stack).
//
//   GOGC=off — disables the garbage collector entirely from process start.
//     Without this, a GC cycle can start during Go runtime initialisation
//     (before TestMain runs), and the in-progress cycle's mark workers may
//     try to scan a goroutine that has JIT frames (without pclntab) on its
//     stack, crashing in unwinder.next().
//
// SetGCPercent(-1) is kept as defense-in-depth to guard against any late
// GC trigger.
//
// Production fix (tracked in docs-internal/known-issues.md): register JIT
// code pages with a minimal fake pclntab so the unwinder can safely skip
// JIT frames without crashing. This would remove the test-binary workaround.

package methodjit

import (
	"os"
	"os/exec"
	"runtime/debug"
	"testing"
)

const envKey = "_GSCRIPT_JIT_TESTS_RERUN"

func TestMain(m *testing.M) {
	if os.Getenv(envKey) == "" {
		// First run: re-exec the test binary with GODEBUG=asyncpreemptoff=1.
		// GODEBUG must be set before the Go runtime initialises (before main),
		// so we cannot set it from inside TestMain — we must re-exec.
		exe, err := os.Executable()
		if err != nil {
			panic("TestMain: os.Executable: " + err.Error())
		}
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(),
			envKey+"=1",
			"GODEBUG=asyncpreemptoff=1",
			"GOGC=off",
		)
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
		return // parent exits 0 after child completes
	}

	// Second run (re-exec'd child): asyncpreemptoff=1 is active.
	// Also disable GC as defense-in-depth.
	debug.SetGCPercent(-1)
	os.Exit(m.Run())
}
