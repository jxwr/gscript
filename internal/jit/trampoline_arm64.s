#include "textflag.h"

// callJIT calls a JIT-compiled function directly.
// func callJIT(fn uintptr, ctx uintptr) int64
//
// fn:  pointer to the JIT-compiled native code
// ctx: pointer to JITContext struct (passed as first argument in R0)
//
// Returns the exit code from the JIT function (in R0).
TEXT ·callJIT(SB), NOSPLIT, $0-24
    MOVD fn+0(FP), R16    // R16 = function pointer
    MOVD ctx+8(FP), R0    // R0 = ctx pointer (first arg to JIT)
    CALL (R16)             // indirect call to JIT code (BLR R16)
    MOVD R0, ret+16(FP)   // store return value
    RET
