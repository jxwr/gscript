-- Benchmark: Sum of Primes (trial division)
-- Tests: integer arithmetic, nested loops, conditional branching

local function is_prime(n)
    if n < 2 then return false end
    if n < 4 then return true end
    if n % 2 == 0 then return false end
    if n % 3 == 0 then return false end
    local i = 5
    while i * i <= n do
        if n % i == 0 then return false end
        if n % (i + 2) == 0 then return false end
        i = i + 6
    end
    return true
end

local N = 100000

local t0 = os.clock()
local sum = 0
local count = 0
for i = 2, N do
    if is_prime(i) then
        sum = sum + i
        count = count + 1
    end
end
local elapsed = os.clock() - t0

print(string.format("sum_primes(%d): %d primes, sum=%d", N, count, sum))
print(string.format("Time: %.3fs", elapsed))
