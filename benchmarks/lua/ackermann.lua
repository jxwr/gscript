-- Benchmark: Ackermann Function
-- Tests: deep recursion, function call overhead, integer comparison
-- ack(3,4) = 125, matches GScript benchmark: ack(3,4) x 500 reps

local function ack(m, n)
    if m == 0 then return n + 1 end
    if n == 0 then return ack(m - 1, 1) end
    return ack(m - 1, ack(m, n - 1))
end

local REPS = 500

local t0 = os.clock()
local result = 0
for r = 1, REPS do
    result = ack(3, 4)
end
local elapsed = os.clock() - t0

print(string.format("ack(3,4) = %d (%d reps)", result, REPS))
print(string.format("Time: %.3fs (%.1f us/call)", elapsed, elapsed / REPS * 1e6))
