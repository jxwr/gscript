-- Benchmark: Mandelbrot Set
-- Tests: floating-point loops, conditional branching, nested iteration
-- Counts pixels in the Mandelbrot set for an NxN grid
-- (copied and adapted from /tmp/mandelbrot_luajit.lua)

local function mandelbrot(size)
    local count = 0
    for y = 0, size - 1 do
        local ci = 2.0 * y / size - 1.0
        for x = 0, size - 1 do
            local cr = 2.0 * x / size - 1.5
            local zr = 0.0
            local zi = 0.0
            local escaped = false
            for iter = 0, 49 do
                local tr = zr * zr - zi * zi + cr
                local ti = 2.0 * zr * zi + ci
                zr = tr
                zi = ti
                if zr * zr + zi * zi > 4.0 then
                    escaped = true
                    break
                end
            end
            if not escaped then count = count + 1 end
        end
    end
    return count
end

local N = 1000

local t0 = os.clock()
local result = mandelbrot(N)
local elapsed = os.clock() - t0

print(string.format("mandelbrot(%d) = %d pixels in set", N, result))
print(string.format("Time: %.3fs", elapsed))
