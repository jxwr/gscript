-- Benchmark: Quicksort
-- Tests: array operations, recursion, comparison callbacks, partition patterns

local function quicksort(arr, lo, hi)
    if lo >= hi then return end
    local pivot = arr[hi]
    local i = lo
    for j = lo, hi - 1 do
        if arr[j] <= pivot then
            local t = arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        end
    end
    local t = arr[i]
    arr[i] = arr[hi]
    arr[hi] = t

    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
end

-- Generate pseudo-random array using LCG (same seed as GScript)
local function make_random_array(n, seed)
    local arr = {}
    local x = seed
    for i = 1, n do
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    end
    return arr
end

local function is_sorted(arr, n)
    for i = 1, n - 1 do
        if arr[i] > arr[i + 1] then return false end
    end
    return true
end

local N = 50000
local REPS = 3

local t0 = os.clock()
for rep = 1, REPS do
    local arr = make_random_array(N, rep * 42)
    quicksort(arr, 1, N)
end
local elapsed = os.clock() - t0

-- Verify correctness on last sort
local arr = make_random_array(N, REPS * 42)
quicksort(arr, 1, N)
local sorted = is_sorted(arr, N)

print(string.format("quicksort(%d) x %d reps", N, REPS))
print(string.format("Sorted correctly: %s", tostring(sorted)))
print(string.format("Time: %.3fs", elapsed))
