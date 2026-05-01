// Extended benchmark: polymorphic object dispatch with mutation.

func step_worker(a, tick) {
    a.x = a.x + a.vx
    a.y = a.y + a.vy
    a.load = (a.load + tick + a.id) % 97
    if a.load > 70 {
        a.vx = a.vx * 0.99
        a.vy = a.vy * 1.01
    } else {
        a.vx = a.vx + 0.001
    }
    return a.x * 3.0 + a.y * 2.0 + a.load
}

func step_io(a, tick) {
    a.queue = (a.queue + tick + a.id) % 211
    a.bytes = a.bytes + a.queue * 13 + tick
    if a.queue % 5 == 0 {
        a.state = "flush"
    } else {
        a.state = "read"
    }
    return a.bytes % 100000 + #a.state
}

func step_cache(a, tick) {
    slot := (tick + a.id) % 8 + 1
    old := a.lines[slot]
    next := (old * 33 + tick + a.id) % 1009
    a.lines[slot] = next
    a.hits = a.hits + (next % 7)
    return a.hits + next
}

func new_actor(i) {
    mod := i % 3
    if mod == 0 {
        return {id: i, kind: "worker", x: i * 0.25, y: i * 0.5, vx: 0.15, vy: 0.25, load: i % 91, step: step_worker}
    } elseif mod == 1 {
        return {id: i, kind: "io", queue: i % 113, bytes: i * 17, state: "read", step: step_io}
    }
    return {id: i, kind: "cache", lines: {1, 3, 5, 7, 11, 13, 17, 19}, hits: i % 31, step: step_cache}
}

func build_actors(n) {
    actors := {}
    for i := 1; i <= n; i++ {
        actors[i] = new_actor(i)
    }
    return actors
}

func run_world(actors, n, ticks) {
    checksum := 0
    for tick := 1; tick <= ticks; tick++ {
        for i := 1; i <= n; i++ {
            actor := actors[i]
            step := actor.step
            value := step(actor, tick)
            checksum = (checksum + math.floor(value) + #actor.kind + i) % 1000000007
        }
    }
    return checksum
}

N := 5000
TICKS := 200

t0 := time.now()
actors := build_actors(N)
checksum := run_world(actors, N, TICKS)
elapsed := time.since(t0)

print(string.format("actors_dispatch_mutation actors=%d ticks=%d", N, TICKS))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
