-- Benchmark: Function Calls
-- Tests: tight function call overhead
-- Matches GScript benchmark: add(x,1) x 10K in loop x 1000 reps

local function add(a, b)
    return a + b
end

local function callMany()
    local x = 0
    for i = 1, 10000 do
        x = add(x, 1)
    end
    return x
end

local REPS = 1000

local t0 = os.clock()
local result = 0
for rep = 1, REPS do
    result = callMany()
end
local elapsed = os.clock() - t0

print(string.format("fn_calls(10K x %d reps) = %d", REPS, result))
print(string.format("Time: %.3fs (%.1f us/call)", elapsed, elapsed / REPS * 1e6))
