-- Benchmark: Coroutine Performance
-- Matches benchmarks/suite/coroutine_bench.gs.

local function test_yield_loop(n)
    local co = coroutine.create(function()
        for i = 1, n do
            coroutine.yield(i)
        end
        return n
    end)
    local sum = 0
    for i = 1, n do
        local ok, val = coroutine.resume(co)
        if not ok then error(val) end
        sum = sum + val
    end
    return sum
end

local function test_create_resume(n)
    local total = 0
    for i = 1, n do
        local co = coroutine.create(function()
            return i * 2
        end)
        local ok, val = coroutine.resume(co)
        if not ok then error(val) end
        total = total + val
    end
    return total
end

local function test_generator(n)
    local gen = coroutine.wrap(function()
        for i = 1, n do
            coroutine.yield(i * i)
        end
    end)
    local sum = 0
    for i = 1, n do
        local val = gen()
        if val == nil then break end
        sum = sum + val
    end
    return sum
end

local N1 = 100000
local N2 = 50000
local N3 = 100000

local t0 = os.clock()
local r1 = test_yield_loop(N1)
local t1 = os.clock() - t0

t0 = os.clock()
local r2 = test_create_resume(N2)
local t2 = os.clock() - t0

t0 = os.clock()
local r3 = test_generator(N3)
local t3 = os.clock() - t0

local total = t1 + t2 + t3

print(string.format("yield_loop(%d):    %.3fs (sum=%d)", N1, t1, r1))
print(string.format("create_resume(%d): %.3fs (sum=%d)", N2, t2, r2))
print(string.format("generator(%d):     %.3fs (sum=%d)", N3, t3, r3))
print(string.format("Time: %.3fs", total))
