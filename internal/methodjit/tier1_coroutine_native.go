//go:build darwin && arm64

package methodjit

import "os"

var tier1CoroutineNativeSwitchEnabled = os.Getenv("GSCRIPT_TIER1_CORO_NATIVE_SWITCH") == "1"
