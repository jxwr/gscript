-- Structural variant: closure accumulators with non-unit integer delta and
-- fractional fallback state.

local function make_accumulator(start, delta)
    local value = start
    return function()
        value = value + delta
        return value
    end
end

local function run_int_accumulator()
    local acc = make_accumulator(7, 3)
    local total = 0
    for i = 1, 5000000 do
        total = total + acc()
    end
    return total
end

local function run_float_accumulator()
    local acc = make_accumulator(0.5, 1.25)
    local total = 0.0
    for i = 1, 2000000 do
        total = total + acc()
    end
    return total
end

local t0 = os.clock()
local int_result = run_int_accumulator()
local t1 = os.clock() - t0

t0 = os.clock()
local float_result = run_float_accumulator()
local t2 = os.clock() - t0

print(string.format("int_delta accumulator: %.3fs (result=%d)", t1, int_result))
print(string.format("float_delta accumulator: %.3fs (result=%.3f)", t2, float_result))
print(string.format("Time: %.3fs", t1 + t2))
