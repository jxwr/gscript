-- Structural variant: table-of-tables matrix multiplication with a different
-- size and row construction shape.

local function matgen_attached_rows(n, scale)
    local m = {}
    for i = 0, n - 1 do
        m[i] = {}
    end
    for i = 0, n - 1 do
        local row = m[i]
        local base = i * n
        for j = 0, n - 1 do
            row[j] = (base + j + scale) / (n * n)
        end
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

local N = 360
local REPS = 4
local t0 = os.clock()

local half = math.floor(N / 2)
local checksum = 0.0
for rep = 1, REPS do
    local a = matgen_attached_rows(N, rep + 2.0)
    local b = matgen_attached_rows(N, rep + 6.0)
    local c = matmul(a, b, N)
    checksum = checksum + c[0][0] + c[half][half] + c[N - 1][N - 1]
end
local elapsed = os.clock() - t0

print(string.format("matmul_row_variant(%d) x %d checksum = %.6f", N, REPS, checksum))
print(string.format("Time: %.3fs", elapsed))
