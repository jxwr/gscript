-- Benchmark: Table Field Access
-- Matches benchmarks/suite/table_field_access.gs.

local function make_particle(x, y, z, vx, vy, vz)
    return { x = x, y = y, z = z, vx = vx, vy = vy, vz = vz, mass = 1.0 }
end

local function create_particles(n)
    local particles = {}
    for i = 1, n do
        local x = 1.0 * i / n
        local y = 2.0 * i / n - 0.5
        local z = 0.5 * i / n + 0.3
        local vx = 0.01 * (i % 7)
        local vy = 0.02 * (i % 11)
        local vz = -0.01 * (i % 13)
        particles[i] = make_particle(x, y, z, vx, vy, vz)
    end
    return particles
end

local function step(particles, n, dt)
    for i = 1, n do
        local p = particles[i]
        p.x = p.x + p.vx * dt
        p.y = p.y + p.vy * dt
        p.z = p.z + p.vz * dt
        p.vx = p.vx * 0.999
        p.vy = p.vy * 0.999
        p.vz = p.vz * 0.999
    end
end

local function checksum(particles, n)
    local sum = 0.0
    for i = 1, n do
        local p = particles[i]
        sum = sum + p.x + p.y + p.z
    end
    return sum
end

local N = 1000
local STEPS = 5000
local dt = 0.01

local particles = create_particles(N)

local t0 = os.clock()
for s = 1, STEPS do
    step(particles, N, dt)
end
local elapsed = os.clock() - t0

local cs = checksum(particles, N)
print(string.format("table_field_access(%d particles, %d steps)", N, STEPS))
print(string.format("Checksum: %.6f", cs))
print(string.format("Time: %.3fs", elapsed))
