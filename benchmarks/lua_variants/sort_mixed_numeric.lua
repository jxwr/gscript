-- Structural variant: quicksort with negative ints and mixed integral floats.

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

local function make_negative_array(n, seed)
    local arr = {}
    local x = seed
    for i = 1, n do
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = (x % 200001) - 100000
    end
    return arr
end

local function make_mixed_integral_array(n, seed)
    local arr = {}
    local x = seed
    for i = 1, n do
        x = (x * 1664525 + 1013904223) % 2147483648
        local v = (x % 300001) - 150000
        if i % 3 == 0 then
            arr[i] = v + 0.0
        else
            arr[i] = v
        end
    end
    return arr
end

local function is_sorted(arr, n)
    for i = 1, n - 1 do
        if arr[i] > arr[i + 1] then return false end
    end
    return true
end

local function sample_checksum(arr, n)
    local total = 0.0
    for i = 1, n, 997 do
        total = total + arr[i] * ((i % 89) + 1)
    end
    return total
end

local N = 60000
local REPS = 4
local negative_checksum = 0.0
local mixed_checksum = 0.0
local negative_sorted = false
local mixed_sorted = false

local t0 = os.clock()
for rep = 1, REPS do
    local neg = make_negative_array(N, rep * 91)
    quicksort(neg, 1, N)
    negative_checksum = negative_checksum + sample_checksum(neg, N)
    negative_sorted = is_sorted(neg, N)

    local mixed = make_mixed_integral_array(N, rep * 137)
    quicksort(mixed, 1, N)
    mixed_checksum = mixed_checksum + sample_checksum(mixed, N)
    mixed_sorted = is_sorted(mixed, N)
end
local elapsed = os.clock() - t0

print(string.format("negative sorted: %s checksum=%.1f", tostring(negative_sorted), negative_checksum))
print(string.format("mixed integral sorted: %s checksum=%.1f", tostring(mixed_sorted), mixed_checksum))
print(string.format("Time: %.3fs", elapsed))
