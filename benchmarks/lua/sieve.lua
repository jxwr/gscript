-- Benchmark: Sieve of Eratosthenes
-- Tests: array access, integer arithmetic, conditional branching
-- Expected: 78498 primes up to 1,000,000

local function sieve(n)
    local is_prime = {}
    for i = 2, n do
        is_prime[i] = true
    end
    local i = 2
    while i * i <= n do
        if is_prime[i] then
            local j = i * i
            while j <= n do
                is_prime[j] = false
                j = j + i
            end
        end
        i = i + 1
    end
    local count = 0
    for i = 2, n do
        if is_prime[i] then count = count + 1 end
    end
    return count
end

local N = 1000000
local REPS = 3

local t0 = os.clock()
local result = 0
for r = 1, REPS do
    result = sieve(N)
end
local elapsed = os.clock() - t0

print(string.format("sieve(%d) = %d primes", N, result))
print(string.format("Time: %.3fs (%d reps)", elapsed, REPS))
