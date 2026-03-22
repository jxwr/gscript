// Benchmark: Table Field Access
// Tests: intensive field read/write on table objects (nbody-like pattern without physics)
// Stresses GETFIELD/SETFIELD performance, inline field cache effectiveness

func make_particle(x, y, z, vx, vy, vz) {
    return {x: x, y: y, z: z, vx: vx, vy: vy, vz: vz, mass: 1.0}
}

// Create N particles with pseudo-random positions
func create_particles(n) {
    particles := {}
    for i := 1; i <= n; i++ {
        x := 1.0 * i / n
        y := 2.0 * i / n - 0.5
        z := 0.5 * i / n + 0.3
        vx := 0.01 * (i % 7)
        vy := 0.02 * (i % 11)
        vz := -0.01 * (i % 13)
        particles[i] = make_particle(x, y, z, vx, vy, vz)
    }
    return particles
}

// Update all particles: read 6 fields, write 6 fields per particle per step
func step(particles, n, dt) {
    for i := 1; i <= n; i++ {
        p := particles[i]
        // Apply velocity
        p.x = p.x + p.vx * dt
        p.y = p.y + p.vy * dt
        p.z = p.z + p.vz * dt
        // Simple damping
        p.vx = p.vx * 0.999
        p.vy = p.vy * 0.999
        p.vz = p.vz * 0.999
    }
}

// Sum all positions for correctness check
func checksum(particles, n) {
    sum := 0.0
    for i := 1; i <= n; i++ {
        p := particles[i]
        sum = sum + p.x + p.y + p.z
    }
    return sum
}

N := 1000
STEPS := 5000
dt := 0.01

particles := create_particles(N)

t0 := time.now()
for s := 1; s <= STEPS; s++ {
    step(particles, N, dt)
}
elapsed := time.since(t0)

cs := checksum(particles, N)
print(string.format("table_field_access(%d particles, %d steps)", N, STEPS))
print(string.format("Checksum: %.6f", cs))
print(string.format("Time: %.3fs", elapsed))
