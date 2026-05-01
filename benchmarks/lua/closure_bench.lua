-- Benchmark: Closure Creation and Invocation
-- Tests: closure allocation, upvalue capture, higher-order functions

-- Test 1: Create and call closures in a loop
local function test_closure_call()
    local function make_adder(x)
        return function(y) return x + y end
    end
    local sum = 0
    for i = 1, 100000 do
        local add5 = make_adder(5)
        sum = sum + add5(i)
    end
    return sum
end

-- Test 2: Accumulator pattern (closure with mutable upvalue)
local function test_accumulator()
    local function make_counter()
        local count = 0
        return function()
            count = count + 1
            return count
        end
    end
    local total = 0
    local counter = make_counter()
    for i = 1, 1000000 do
        total = total + counter()
    end
    return total
end

-- Test 3: Higher-order function (map pattern)
local function test_map()
    local arr = {}
    for i = 1, 1000 do
        arr[i] = i
    end
    local function map_array(a, f)
        local result = {}
        local n = #a
        for i = 1, n do
            result[i] = f(a[i])
        end
        return result
    end
    local total = 0
    for rep = 1, 100 do
        local mapped = map_array(arr, function(x) return x * 2 + 1 end)
        total = total + mapped[500]
    end
    return total
end

local t0 = os.clock()
local r1 = test_closure_call()
local t1 = os.clock() - t0

t0 = os.clock()
local r2 = test_accumulator()
local t2 = os.clock() - t0

t0 = os.clock()
local r3 = test_map()
local t3 = os.clock() - t0

print(string.format("closure_call:  %.3fs (result=%d)", t1, r1))
print(string.format("accumulator:   %.3fs (result=%d)", t2, r2))
print(string.format("map_array:     %.3fs (result=%d)", t3, r3))
print(string.format("Time: %.3fs", t1 + t2 + t3))
