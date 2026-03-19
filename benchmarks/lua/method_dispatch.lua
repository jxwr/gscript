-- Benchmark: OOP Method Dispatch via Tables
-- Tests: table-as-object pattern, field access, function calls

local function new_point(x, y)
    return {x = x, y = y}
end

local function point_distance(p1, p2)
    local dx = p1.x - p2.x
    local dy = p1.y - p2.y
    return math.sqrt(dx * dx + dy * dy)
end

local function point_translate(p, dx, dy)
    return new_point(p.x + dx, p.y + dy)
end

local function point_scale(p, factor)
    return new_point(p.x * factor, p.y * factor)
end

-- Test: create points, compute distances, transform
local function test_points(n)
    local total_dist = 0.0
    local p = new_point(0.0, 0.0)
    for i = 1, n do
        local q = new_point(1.0 * i, 2.0 * i)
        total_dist = total_dist + point_distance(p, q)
        p = point_translate(p, 0.1, 0.2)
        p = point_scale(p, 0.999)
    end
    return total_dist
end

local N = 100000

local t0 = os.clock()
local result = test_points(N)
local elapsed = os.clock() - t0

print(string.format("method_dispatch(%d): dist=%.4f", N, result))
print(string.format("Time: %.3fs", elapsed))
