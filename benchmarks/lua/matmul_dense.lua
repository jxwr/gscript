-- Benchmark: Matrix multiplication using a flat dense matrix helper.

local function dense(rows, cols)
    return { rows = rows, cols = cols, data = {} }
end

local function getf(m, i, j)
    return m.data[i * m.cols + j] or 0.0
end

local function setf(m, i, j, v)
    m.data[i * m.cols + j] = v
end

local function matgen(n)
    local m = dense(n, n)
    for i = 0, n - 1 do
        for j = 0, n - 1 do
            setf(m, i, j, (i * n + j + 1.0) / (n * n))
        end
    end
    return m
end

local function matmul(a, b, n)
    local c = dense(n, n)
    for i = 0, n - 1 do
        for j = 0, n - 1 do
            local sum = 0.0
            for k = 0, n - 1 do
                sum = sum + getf(a, i, k) * getf(b, k, j)
            end
            setf(c, i, j, sum)
        end
    end
    return c
end

local N = 300
local t0 = os.clock()
local a = matgen(N)
local b = matgen(N)
local c = matmul(a, b, N)
local elapsed = os.clock() - t0

print(string.format("matmul_dense(%d) c[0][0] = %.6f", N, getf(c, 0, 0)))
print(string.format("Time: %.3fs", elapsed))
