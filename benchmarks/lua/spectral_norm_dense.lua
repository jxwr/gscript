-- Benchmark: spectral norm using flat dense vectors.

local function dense(rows, cols)
    return { rows = rows, cols = cols, data = {} }
end

local function getf(m, i, j)
    return m.data[i * m.cols + j] or 0.0
end

local function setf(m, i, j, v)
    m.data[i * m.cols + j] = v
end

local function A(i, j)
    return 1.0 / ((i + j) * (i + j + 1) / 2 + i + 1)
end

local function multiplyAv(n, v, av)
    for i = 0, n - 1 do
        local sum = 0.0
        for j = 0, n - 1 do
            sum = sum + A(i, j) * getf(v, j, 0)
        end
        setf(av, i, 0, sum)
    end
end

local function multiplyAtv(n, v, atv)
    for i = 0, n - 1 do
        local sum = 0.0
        for j = 0, n - 1 do
            sum = sum + A(j, i) * getf(v, j, 0)
        end
        setf(atv, i, 0, sum)
    end
end

local function multiplyAtAv(n, v, u, atav)
    for i = 0, n - 1 do
        setf(u, i, 0, 0.0)
    end
    multiplyAv(n, v, u)
    multiplyAtv(n, u, atav)
end

local N = 1500
local t0 = os.clock()

local u = dense(N, 1)
local v = dense(N, 1)
local tmp = dense(N, 1)
for i = 0, N - 1 do
    setf(u, i, 0, 1.0)
    setf(v, i, 0, 0.0)
end

for _ = 0, 9 do
    multiplyAtAv(N, u, tmp, v)
    multiplyAtAv(N, v, tmp, u)
end

local vBv = 0.0
local vv = 0.0
for i = 0, N - 1 do
    local ui = getf(u, i, 0)
    local vi = getf(v, i, 0)
    vBv = vBv + ui * vi
    vv = vv + vi * vi
end

local result = math.sqrt(vBv / vv)
local elapsed = os.clock() - t0

print(string.format("spectral_norm_dense(%d) = %.9f", N, result))
print(string.format("Time: %.3fs", elapsed))
