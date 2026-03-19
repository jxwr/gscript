-- Benchmark: String Operations
-- Tests: concatenation, length checks, string comparison

-- Test 1: String concatenation (building a long string)
local function test_concat()
    local s = ""
    for i = 1, 10000 do
        s = s .. "x"
    end
    return #s
end

-- Test 2: String formatting (sprintf-style)
local function test_format()
    local total = 0
    for i = 1, 50000 do
        local s = string.format("item_%d_value_%d", i, i * 2)
        total = total + #s
    end
    return total
end

-- Test 3: String comparison (sorting strings via bubble sort)
local function test_compare()
    local arr = {}
    for i = 1, 1000 do
        arr[i] = string.format("key_%05d", (i * 7) % 1000)
    end
    -- Simple bubble sort (tests string comparison)
    local n = #arr
    for i = 1, n - 1 do
        for j = 1, n - i do
            if arr[j] > arr[j + 1] then
                local t = arr[j]
                arr[j] = arr[j + 1]
                arr[j + 1] = t
            end
        end
    end
    return arr[1] .. " .. " .. arr[n]
end

local t0 = os.clock()
local r1 = test_concat()
local t1 = os.clock() - t0

t0 = os.clock()
local r2 = test_format()
local t2 = os.clock() - t0

t0 = os.clock()
local r3 = test_compare()
local t3 = os.clock() - t0

print(string.format("concat:  %.3fs (len=%d)", t1, r1))
print(string.format("format:  %.3fs (total=%d)", t2, r2))
print(string.format("compare: %.3fs (first..last=%s)", t3, r3))
