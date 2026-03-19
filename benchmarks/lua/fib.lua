-- Benchmark: Fibonacci (Recursive) - warm loop
-- Tests: recursive function call overhead, integer arithmetic
-- Matches GScript benchmark: fib(20) x 1000 reps

local function fib(n)
    if n < 2 then return n end
    return fib(n - 1) + fib(n - 2)
end

local N = 20
local REPS = 1000

local t0 = os.clock()
local result = 0
for rep = 1, REPS do
    result = fib(N)
end
local elapsed = os.clock() - t0

print(string.format("fib(%d) = %d (%d reps)", N, result, REPS))
print(string.format("Time: %.3fs (%.1f us/call)", elapsed, elapsed / REPS * 1e6))
