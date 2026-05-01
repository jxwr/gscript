-- Benchmark: Recursive Fibonacci
-- Matches benchmarks/suite/fib_recursive.gs: fib(35) x 10 reps

local function fib(n)
    if n < 2 then return n end
    return fib(n - 1) + fib(n - 2)
end

local N = 35
local REPS = 10

local result = 0
local t0 = os.clock()
for rep = 1, REPS do
    result = fib(N)
end
local elapsed = os.clock() - t0

print(string.format("fib(%d) = %d", N, result))
print(string.format("Time: %.3fs (%d reps)", elapsed, REPS))
