-- Benchmark: nbody using a flat dense matrix helper.

local function dense(rows, cols)
    return { rows = rows, cols = cols, data = {} }
end

local function getf(m, i, j)
    return m.data[i * m.cols + j] or 0.0
end

local function setf(m, i, j, v)
    m.data[i * m.cols + j] = v
end

local PI = 3.141592653589793
local SOLAR_MASS = 4 * PI * PI
local DAYS_PER_YEAR = 365.24

local N_BODIES = 5
local F_X = 0
local F_Y = 1
local F_Z = 2
local F_VX = 3
local F_VY = 4
local F_VZ = 5
local F_MASS = 6

local bodies = dense(N_BODIES, 7)

setf(bodies, 0, F_MASS, SOLAR_MASS)

setf(bodies, 1, F_X, 4.84143144246472090)
setf(bodies, 1, F_Y, -1.16032004402742839)
setf(bodies, 1, F_Z, -0.10362204447112311)
setf(bodies, 1, F_VX, 0.00166007664274403694 * DAYS_PER_YEAR)
setf(bodies, 1, F_VY, 0.00769901118419740425 * DAYS_PER_YEAR)
setf(bodies, 1, F_VZ, -0.00006905169867435090 * DAYS_PER_YEAR)
setf(bodies, 1, F_MASS, 0.000954791938424326609 * SOLAR_MASS)

setf(bodies, 2, F_X, 8.34336671824457987)
setf(bodies, 2, F_Y, 4.12479856412430479)
setf(bodies, 2, F_Z, -0.40352341711789204)
setf(bodies, 2, F_VX, -0.00276742510726862411 * DAYS_PER_YEAR)
setf(bodies, 2, F_VY, 0.00499852801234917238 * DAYS_PER_YEAR)
setf(bodies, 2, F_VZ, 0.00023041729757376393 * DAYS_PER_YEAR)
setf(bodies, 2, F_MASS, 0.000285885980666130812 * SOLAR_MASS)

setf(bodies, 3, F_X, 12.89436956213913)
setf(bodies, 3, F_Y, -15.1111514016986312)
setf(bodies, 3, F_Z, -0.22330757889265573)
setf(bodies, 3, F_VX, 0.00296460137564761618 * DAYS_PER_YEAR)
setf(bodies, 3, F_VY, 0.00237847173959480950 * DAYS_PER_YEAR)
setf(bodies, 3, F_VZ, -0.00029658956854023756 * DAYS_PER_YEAR)
setf(bodies, 3, F_MASS, 0.0000436624404335156298 * SOLAR_MASS)

setf(bodies, 4, F_X, 15.3796971148509165)
setf(bodies, 4, F_Y, -25.9193146099879641)
setf(bodies, 4, F_Z, 0.17925877295037118)
setf(bodies, 4, F_VX, 0.00268067772490389322 * DAYS_PER_YEAR)
setf(bodies, 4, F_VY, 0.00162824170038242295 * DAYS_PER_YEAR)
setf(bodies, 4, F_VZ, -0.00009515922545197159 * DAYS_PER_YEAR)
setf(bodies, 4, F_MASS, 0.0000515138902046611451 * SOLAR_MASS)

local function offsetMomentum()
    local px = 0.0
    local py = 0.0
    local pz = 0.0
    for i = 1, N_BODIES - 1 do
        local m = getf(bodies, i, F_MASS)
        px = px + getf(bodies, i, F_VX) * m
        py = py + getf(bodies, i, F_VY) * m
        pz = pz + getf(bodies, i, F_VZ) * m
    end
    setf(bodies, 0, F_VX, -px / SOLAR_MASS)
    setf(bodies, 0, F_VY, -py / SOLAR_MASS)
    setf(bodies, 0, F_VZ, -pz / SOLAR_MASS)
end

local function energy()
    local e = 0.0
    for i = 0, N_BODIES - 1 do
        local mi = getf(bodies, i, F_MASS)
        local vx = getf(bodies, i, F_VX)
        local vy = getf(bodies, i, F_VY)
        local vz = getf(bodies, i, F_VZ)
        e = e + 0.5 * mi * (vx * vx + vy * vy + vz * vz)
        for j = i + 1, N_BODIES - 1 do
            local dx = getf(bodies, i, F_X) - getf(bodies, j, F_X)
            local dy = getf(bodies, i, F_Y) - getf(bodies, j, F_Y)
            local dz = getf(bodies, i, F_Z) - getf(bodies, j, F_Z)
            local dist = math.sqrt(dx * dx + dy * dy + dz * dz)
            e = e - mi * getf(bodies, j, F_MASS) / dist
        end
    end
    return e
end

local function advance(dt)
    for i = 0, N_BODIES - 1 do
        local bix = getf(bodies, i, F_X)
        local biy = getf(bodies, i, F_Y)
        local biz = getf(bodies, i, F_Z)
        local bim = getf(bodies, i, F_MASS)
        local bivx = getf(bodies, i, F_VX)
        local bivy = getf(bodies, i, F_VY)
        local bivz = getf(bodies, i, F_VZ)
        for j = i + 1, N_BODIES - 1 do
            local bjx = getf(bodies, j, F_X)
            local bjy = getf(bodies, j, F_Y)
            local bjz = getf(bodies, j, F_Z)
            local bjm = getf(bodies, j, F_MASS)
            local bjvx = getf(bodies, j, F_VX)
            local bjvy = getf(bodies, j, F_VY)
            local bjvz = getf(bodies, j, F_VZ)
            local dx = bix - bjx
            local dy = biy - bjy
            local dz = biz - bjz
            local dsq = dx * dx + dy * dy + dz * dz
            local dist = math.sqrt(dsq)
            local mag = dt / (dsq * dist)
            bivx = bivx - dx * bjm * mag
            bivy = bivy - dy * bjm * mag
            bivz = bivz - dz * bjm * mag
            setf(bodies, j, F_VX, bjvx + dx * bim * mag)
            setf(bodies, j, F_VY, bjvy + dy * bim * mag)
            setf(bodies, j, F_VZ, bjvz + dz * bim * mag)
        end
        setf(bodies, i, F_VX, bivx)
        setf(bodies, i, F_VY, bivy)
        setf(bodies, i, F_VZ, bivz)
    end
    for i = 0, N_BODIES - 1 do
        setf(bodies, i, F_X, getf(bodies, i, F_X) + dt * getf(bodies, i, F_VX))
        setf(bodies, i, F_Y, getf(bodies, i, F_Y) + dt * getf(bodies, i, F_VY))
        setf(bodies, i, F_Z, getf(bodies, i, F_Z) + dt * getf(bodies, i, F_VZ))
    end
end

local N = 500000
local dt = 0.01

offsetMomentum()
local e0 = energy()

local t0 = os.clock()
for _ = 1, N do
    advance(dt)
end
local elapsed = os.clock() - t0

local e1 = energy()

print(string.format("nbody_dense(%d steps)", N))
print(string.format("Energy before: %.9f", e0))
print(string.format("Energy after:  %.9f", e1))
print(string.format("Time: %.3fs", elapsed))
