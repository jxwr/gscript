-- Extended benchmark: polymorphic object dispatch with mutation.

local function step_worker(a, tick)
    a.x = a.x + a.vx
    a.y = a.y + a.vy
    a.load = (a.load + tick + a.id) % 97
    if a.load > 70 then
        a.vx = a.vx * 0.99
        a.vy = a.vy * 1.01
    else
        a.vx = a.vx + 0.001
    end
    return a.x * 3.0 + a.y * 2.0 + a.load
end

local function step_io(a, tick)
    a.queue = (a.queue + tick + a.id) % 211
    a.bytes = a.bytes + a.queue * 13 + tick
    if a.queue % 5 == 0 then
        a.state = "flush"
    else
        a.state = "read"
    end
    return a.bytes % 100000 + #a.state
end

local function step_cache(a, tick)
    local slot = (tick + a.id) % 8 + 1
    local old = a.lines[slot]
    local next_value = (old * 33 + tick + a.id) % 1009
    a.lines[slot] = next_value
    a.hits = a.hits + (next_value % 7)
    return a.hits + next_value
end

local function new_actor(i)
    local mod = i % 3
    if mod == 0 then
        return {id = i, kind = "worker", x = i * 0.25, y = i * 0.5, vx = 0.15, vy = 0.25, load = i % 91, step = step_worker}
    elseif mod == 1 then
        return {id = i, kind = "io", queue = i % 113, bytes = i * 17, state = "read", step = step_io}
    end
    return {id = i, kind = "cache", lines = {1, 3, 5, 7, 11, 13, 17, 19}, hits = i % 31, step = step_cache}
end

local function build_actors(n)
    local actors = {}
    for i = 1, n do
        actors[i] = new_actor(i)
    end
    return actors
end

local function run_world(actors, n, ticks)
    local checksum = 0
    for tick = 1, ticks do
        for i = 1, n do
            local actor = actors[i]
            local value = actor.step(actor, tick)
            checksum = (checksum + math.floor(value) + #actor.kind + i) % 1000000007
        end
    end
    return checksum
end

local N = 5000
local TICKS = 200

local t0 = os.clock()
local actors = build_actors(N)
local checksum = run_world(actors, N, TICKS)
local elapsed = os.clock() - t0

print(string.format("actors_dispatch_mutation actors=%d ticks=%d", N, TICKS))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
