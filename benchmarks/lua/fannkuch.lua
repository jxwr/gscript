-- Benchmark: Fannkuch-Redux
-- Tests: array permutation, conditional branching, integer heavy
-- A classic benchmark from the Computer Language Benchmarks Game

local function fannkuch(n)
    local perm = {}
    local perm1 = {}
    local count = {}
    for i = 1, n do
        perm1[i] = i
        count[i] = i
    end

    local maxFlips = 0
    local checksum = 0
    local nperm = 0

    while true do
        -- Copy perm1 to perm
        for i = 1, n do
            perm[i] = perm1[i]
        end

        -- Count flips
        local flips = 0
        local k = perm[1]
        while k ~= 1 do
            -- Reverse first k elements
            local lo = 1
            local hi = k
            while lo < hi do
                local t = perm[lo]
                perm[lo] = perm[hi]
                perm[hi] = t
                lo = lo + 1
                hi = hi - 1
            end
            flips = flips + 1
            k = perm[1]
        end
        if flips > maxFlips then maxFlips = flips end
        if nperm % 2 == 0 then
            checksum = checksum + flips
        else
            checksum = checksum - flips
        end
        nperm = nperm + 1

        -- Next permutation (Johnson-Trotter)
        local done = true
        for i = 2, n do
            -- Rotate perm1[1..i] left by one
            local t = perm1[1]
            for j = 1, i - 1 do
                perm1[j] = perm1[j + 1]
            end
            perm1[i] = t

            count[i] = count[i] - 1
            if count[i] > 0 then
                done = false
                break
            end
            count[i] = i
        end
        if done then break end
    end

    return maxFlips, checksum
end

local N = 9
local t0 = os.clock()
local maxFlips, checksum = fannkuch(N)
local elapsed = os.clock() - t0

print(string.format("fannkuch(%d) = %d flips, checksum %d", N, maxFlips, checksum))
print(string.format("Time: %.3fs", elapsed))
