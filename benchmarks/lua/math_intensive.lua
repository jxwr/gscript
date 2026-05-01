local function leibniz_pi(n)
    local sum = 0.0
    local sign = 1.0
    for i = 0, n - 1 do
        sum = sum + sign / (2.0 * i + 1.0)
        sign = -sign
    end
    return sum * 4.0
end

local function collatz_total(limit)
    local total_steps = 0
    for n = 2, limit do
        local x = n
        local steps = 0
        while x ~= 1 do
            if x % 2 == 0 then
                x = x / 2
            else
                x = 3 * x + 1
            end
            steps = steps + 1
        end
        total_steps = total_steps + steps
    end
    return total_steps
end

local function distance_sum(n)
    local total = 0.0
    for i = 1, n do
        local x = 1.0 * i / n
        local y = 1.0 - x
        local z = x * y
        total = total + math.sqrt(x * x + y * y + z * z)
    end
    return total
end

local function gcd(a, b)
    while b ~= 0 do
        local t = b
        b = a % b
        a = t
    end
    return a
end

local function gcd_bench(n)
    local total = 0
    for i = 1, n do
        for j = 1, 100 do
            total = total + gcd(i * 7 + 13, j * 11 + 3)
        end
    end
    return total
end

local N_PI = 5000000
local N_COLLATZ = 50000
local N_DIST = 1000000
local N_GCD = 10000

local t0 = os.clock()
local r1 = leibniz_pi(N_PI)
local t1 = os.clock() - t0

t0 = os.clock()
local r2 = collatz_total(N_COLLATZ)
local t2 = os.clock() - t0

t0 = os.clock()
local r3 = distance_sum(N_DIST)
local t3 = os.clock() - t0

t0 = os.clock()
local r4 = gcd_bench(N_GCD)
local t4 = os.clock() - t0

local total = t1 + t2 + t3 + t4

print(string.format("leibniz_pi(%d):  %.3fs (pi=%.10f)", N_PI, t1, r1))
print(string.format("collatz(%d):     %.3fs (total_steps=%d)", N_COLLATZ, t2, r2))
print(string.format("distance(%d):    %.3fs (sum=%.6f)", N_DIST, t3, r3))
print(string.format("gcd(%d):         %.3fs (total=%d)", N_GCD, t4, r4))
print(string.format("Time: %.3fs", total))
