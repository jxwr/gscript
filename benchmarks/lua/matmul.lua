-- Benchmark: Matrix Multiplication
-- Tests: nested loops, 2D table-of-tables access, floating-point arithmetic
-- Note: uses 0-based indexing via offset to match GScript exactly

local function matgen(n)
    local m = {}
    for i = 0, n - 1 do
        local row = {}
        for j = 0, n - 1 do
            row[j] = (i * n + j + 1.0) / (n * n)
        end
        m[i] = row
    end
    return m
end

local function matmul(a, b, n)
    local c = {}
    for i = 0, n - 1 do
        local row = {}
        local ai = a[i]
        for j = 0, n - 1 do
            local sum = 0.0
            for k = 0, n - 1 do
                sum = sum + ai[k] * b[k][j]
            end
            row[j] = sum
        end
        c[i] = row
    end
    return c
end

local N = 300

local t0 = os.clock()

local a = matgen(N)
local b = matgen(N)
local c = matmul(a, b, N)

-- Checksum: center element
local half = math.floor(N / 2)
local result = c[half][half]
local elapsed = os.clock() - t0

print(string.format("matmul(%d) center = %.6f", N, result))
print(string.format("Time: %.3fs", elapsed))
