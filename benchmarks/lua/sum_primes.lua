-- Benchmark: Sum of Primes (trial division)
-- Tests: integer arithmetic, nested loops, conditional branching

local function is_prime(n)
    if n < 2 then return 0 end
    if n < 4 then return 1 end
    if n % 2 == 0 then return 0 end
    if n % 3 == 0 then return 0 end
    local i = 5
    while i * i <= n do
        if n % i == 0 then return 0 end
        if n % (i + 2) == 0 then return 0 end
        i = i + 6
    end
    return 1
end

local function sum_primes(limit)
    local sum = 0
    local count = 0
    for i = 2, limit do
        if is_prime(i) ~= 0 then
            sum = sum + i
            count = count + 1
        end
    end
    return {sum = sum, count = count}
end

local N = 100000
local REPS = 20

local t0 = os.clock()
local result = {sum = 0, count = 0}
for rep = 1, REPS do
    result = sum_primes(N)
end
local elapsed = os.clock() - t0

print(string.format("sum_primes(%d)x%d: %d primes, sum=%d", N, REPS, result.count, result.sum))
print(string.format("Time: %.3fs", elapsed))
