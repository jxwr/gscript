-- Benchmark: Spectral Norm
-- Tests: floating-point computation, array indexing, nested loops, function calls
-- Computes spectral norm of an infinite matrix using power iteration
-- Note: uses 0-based indexing via offset (arrays are 1-indexed in Lua, we shift by 1)

local function A(i, j)
    return 1.0 / ((i + j) * (i + j + 1) / 2 + i + 1)
end

local function multiplyAv(n, v, av)
    for i = 0, n - 1 do
        local sum = 0.0
        for j = 0, n - 1 do
            sum = sum + A(i, j) * v[j]
        end
        av[i] = sum
    end
end

local function multiplyAtv(n, v, atv)
    for i = 0, n - 1 do
        local sum = 0.0
        for j = 0, n - 1 do
            sum = sum + A(j, i) * v[j]
        end
        atv[i] = sum
    end
end

local function multiplyAtAv(n, v, atav)
    local u = {}
    for i = 0, n - 1 do u[i] = 0.0 end
    multiplyAv(n, v, u)
    multiplyAtv(n, u, atav)
end

local N = 500

local t0 = os.clock()

local u = {}
local v = {}
for i = 0, N - 1 do
    u[i] = 1.0
    v[i] = 0.0
end

for iter = 0, 9 do
    multiplyAtAv(N, u, v)
    multiplyAtAv(N, v, u)
end

local vBv = 0.0
local vv = 0.0
for i = 0, N - 1 do
    vBv = vBv + u[i] * v[i]
    vv = vv + v[i] * v[i]
end

local result = math.sqrt(vBv / vv)
local elapsed = os.clock() - t0

print(string.format("spectral_norm(%d) = %.9f", N, result))
print(string.format("Time: %.3fs", elapsed))
