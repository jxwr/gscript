-- Benchmark: Mutual Recursion (Hofstadter sequences)
-- Tests: non-self recursive function calls, call overhead
-- Female/Male Hofstadter sequences: F(n) = n - M(F(n-1)), M(n) = n - F(M(n-1))

local F, M

F = function(n)
    if n == 0 then return 1 end
    return n - M(F(n - 1))
end

M = function(n)
    if n == 0 then return 0 end
    return n - F(M(n - 1))
end

local N = 25
local REPS = 1000

local t0 = os.clock()
local result = 0
for rep = 1, REPS do
    result = F(N)
end
local elapsed = os.clock() - t0

print(string.format("F(%d) = %d (%d reps)", N, result, REPS))
print(string.format("Time: %.3fs", elapsed))
