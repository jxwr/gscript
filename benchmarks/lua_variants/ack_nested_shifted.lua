-- Structural variant: renamed Ackermann-like nested recurrence.

local function nestwave(level, width)
    if level == 0 then return width + 2 end
    if width == 0 then return nestwave(level - 1, 2) end
    return nestwave(level - 1, nestwave(level, width - 1))
end

local REPS = 60000
local t0 = os.clock()
local result = 0
local checksum = 0
for r = 1, REPS do
    result = nestwave(2, 6)
    checksum = checksum + (result % 997)
end
local elapsed = os.clock() - t0

print(string.format("nestwave(2,6) = %d (%d reps)", result, REPS))
print(string.format("checksum = %d", checksum))
print(string.format("Time: %.3fs", elapsed))
