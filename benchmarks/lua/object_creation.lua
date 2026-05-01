-- Benchmark: Object Creation
-- Matches benchmarks/suite/object_creation.gs.

local function new_vec3(x, y, z)
    return { x = x, y = y, z = z }
end

local function vec3_add(a, b)
    return new_vec3(a.x + b.x, a.y + b.y, a.z + b.z)
end

local function vec3_scale(v, s)
    return new_vec3(v.x * s, v.y * s, v.z * s)
end

local function vec3_length_sq(v)
    return v.x * v.x + v.y * v.y + v.z * v.z
end

local function create_and_sum(n)
    local total = new_vec3(0.0, 0.0, 0.0)
    for i = 1, n do
        local v = new_vec3(1.0 * i, 2.0 * i, 3.0 * i)
        total = vec3_add(total, v)
    end
    return vec3_length_sq(total)
end

local function transform_chain(n)
    local v = new_vec3(1.0, 0.0, 0.0)
    for i = 1, n do
        local offset = new_vec3(0.001, 0.002, 0.003)
        v = vec3_add(v, offset)
        v = vec3_scale(v, 0.9999)
    end
    return vec3_length_sq(v)
end

local function complex_objects(n)
    local total = 0.0
    for i = 1, n do
        local obj = {
            name = "particle",
            id = i,
            x = 1.0 * i,
            y = 2.0 * i,
            z = 3.0 * i,
            vx = 0.1,
            vy = 0.2,
            vz = 0.3,
            mass = 1.0,
            active = true,
        }
        total = total + obj.x + obj.y + obj.z + obj.mass
    end
    return total
end

local N1 = 200000
local N2 = 500000
local N3 = 100000

local t0 = os.clock()
local r1 = create_and_sum(N1)
local t1 = os.clock() - t0

t0 = os.clock()
local r2 = transform_chain(N2)
local t2 = os.clock() - t0

t0 = os.clock()
local r3 = complex_objects(N3)
local t3 = os.clock() - t0

local total = t1 + t2 + t3

print(string.format("create_and_sum(%d):   %.3fs (len_sq=%.2f)", N1, t1, r1))
print(string.format("transform_chain(%d):  %.3fs (len_sq=%.6f)", N2, t2, r2))
print(string.format("complex_objects(%d):  %.3fs (total=%.2f)", N3, t3, r3))
print(string.format("Time: %.3fs", total))
