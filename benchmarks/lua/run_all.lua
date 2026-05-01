-- Run all LuaJIT benchmarks in sequence
-- Usage: luajit run_all.lua
--        lua run_all.lua

local benchmarks = {
    "fib",
    "sieve",
    "mandelbrot",
    "ackermann",
    "matmul",
    "spectral_norm",
    "nbody",
    "fannkuch",
    "sort",
    "sum_primes",
    "mutual_recursion",
    "method_dispatch",
    "closure_bench",
    "string_bench",
    "binary_trees",
    "table_field_access",
    "table_array_access",
    "coroutine_bench",
    "fibonacci_iterative",
    "math_intensive",
    "object_creation",
}

-- Determine directory of this script
local script_dir = ""
if arg and arg[0] then
    script_dir = arg[0]:match("(.*/)") or ""
end

print("============================================")
print("  LuaJIT Benchmark Suite")
print("  " .. (_VERSION or "Lua"))
print("============================================")
print("")

local total_t0 = os.clock()

for _, name in ipairs(benchmarks) do
    print("--- " .. name .. " ---")
    local path = script_dir .. name .. ".lua"
    local ok, err = pcall(dofile, path)
    if not ok then
        print("ERROR: " .. tostring(err))
    end
    print("")
end

local total_elapsed = os.clock() - total_t0
print("============================================")
print(string.format("  Suite complete  (total %.2fs)", total_elapsed))
print("============================================")
