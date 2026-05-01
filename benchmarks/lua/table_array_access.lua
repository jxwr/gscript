-- Benchmark: Table Array Access
-- Matches benchmarks/suite/table_array_access.gs.

local function int_array_sum(n)
    local arr = {}
    for i = 1, n do
        arr[i] = i
    end
    local sum = 0
    for i = 1, n do
        sum = sum + arr[i]
    end
    return sum
end

local function float_dot_product(n)
    local a = {}
    local b = {}
    for i = 1, n do
        a[i] = 1.0 * i / n
        b[i] = 2.0 * (n - i + 1) / n
    end
    local dot = 0.0
    for i = 1, n do
        dot = dot + a[i] * b[i]
    end
    return dot
end

local function array_swap_bench(n, reps)
    local arr = {}
    for i = 1, n do
        arr[i] = n - i + 1
    end
    for r = 1, reps do
        for i = 1, n - 1, 2 do
            local t = arr[i]
            arr[i] = arr[i + 1]
            arr[i + 1] = t
        end
    end
    return arr[1]
end

local function array_2d_access(size)
    local rows = {}
    for i = 1, size do
        local row = {}
        for j = 1, size do
            row[j] = i * size + j
        end
        rows[i] = row
    end
    local sum = 0
    for i = 1, size do
        local row = rows[i]
        for j = 1, size do
            sum = sum + row[j]
        end
    end
    return sum
end

local N = 100000
local REPS = 100
local MATRIX_SIZE = 300

local t0 = os.clock()
local r1 = 0
for rep = 1, 10 do
    r1 = int_array_sum(N)
end
local t1 = os.clock() - t0

t0 = os.clock()
local r2 = 0.0
for rep = 1, 10 do
    r2 = float_dot_product(N)
end
local t2 = os.clock() - t0

t0 = os.clock()
local r3 = array_swap_bench(N, REPS)
local t3 = os.clock() - t0

t0 = os.clock()
local r4 = array_2d_access(MATRIX_SIZE)
local t4 = os.clock() - t0

local total = t1 + t2 + t3 + t4

print(string.format("int_array_sum:    %.3fs (result=%d)", t1, r1))
print(string.format("float_dot:        %.3fs (result=%.6f)", t2, r2))
print(string.format("array_swap:       %.3fs (result=%d)", t3, r3))
print(string.format("array_2d:         %.3fs (result=%d)", t4, r4))
print(string.format("Time: %.3fs", total))
