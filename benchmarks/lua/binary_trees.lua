local function make(d)
    if d == 0 then return {0, false, false} end
    d = d - 1
    return {0, make(d), make(d)}
end

local function check(node)
    if not node[2] then return 1 end
    return 1 + check(node[2]) + check(node[3])
end

local N = 15
local mindepth = 4
local maxdepth = N
if mindepth + 2 > N then maxdepth = mindepth + 2 end

do
    local stretchdepth = maxdepth + 1
    local stretchtree = make(stretchdepth)
    io.write(string.format("stretch tree of depth %d\t check: %d\n", stretchdepth, check(stretchtree)))
end

local longlivedtree = make(maxdepth)

for depth = mindepth, maxdepth, 2 do
    local iterations = 2 ^ (maxdepth - depth + mindepth)
    local c = 0
    for i = 1, iterations do
        c = c + check(make(depth))
    end
    io.write(string.format("%d\t trees of depth %d\t check: %d\n", iterations, depth, c))
end

io.write(string.format("long lived tree of depth %d\t check: %d\n", maxdepth, check(longlivedtree)))
