-- Benchmark: Iterative Fibonacci
-- Matches benchmarks/suite/fibonacci_iterative.gs: fib_iter(70) x 1,000,000 reps.

local function fib_iter(n)
    local a = 0
    local b = 1
    for i = 0, n - 1 do
        local t = a + b
        a = b
        b = t
    end
    return a
end

local function bench_fib_iter(n, reps)
    local result = 0
    for r = 1, reps do
        result = fib_iter(n)
    end
    return result
end

local N = 70
local REPS = 1000000

local t0 = os.clock()
local result = bench_fib_iter(N, REPS)
local elapsed = os.clock() - t0

print(string.format("fibonacci_iterative(%d) x %d reps", N, REPS))
print(string.format("fib(%d) = %d", N, result))
print(string.format("Time: %.3fs", elapsed))
