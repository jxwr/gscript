// Benchmark: N-Body Simulation
// Tests: floating-point arithmetic, table field access, nested loops, math.sqrt
// Simulates 5 bodies (Sun + 4 Jovian planets) for N timesteps

PI := 3.141592653589793
SOLAR_MASS := 4 * PI * PI
DAYS_PER_YEAR := 365.24

bodies := {
    {name: "sun",     x: 0, y: 0, z: 0, vx: 0, vy: 0, vz: 0, mass: SOLAR_MASS},
    {name: "jupiter", x: 4.84143144246472090, y: -1.16032004402742839, z: -0.10362204447112311,
     vx: 0.00166007664274403694 * DAYS_PER_YEAR, vy: 0.00769901118419740425 * DAYS_PER_YEAR,
     vz: -0.00006905169867435090 * DAYS_PER_YEAR, mass: 0.000954791938424326609 * SOLAR_MASS},
    {name: "saturn",  x: 8.34336671824457987, y: 4.12479856412430479, z: -0.40352341711789204,
     vx: -0.00276742510726862411 * DAYS_PER_YEAR, vy: 0.00499852801234917238 * DAYS_PER_YEAR,
     vz: 0.00023041729757376393 * DAYS_PER_YEAR, mass: 0.000285885980666130812 * SOLAR_MASS},
    {name: "uranus",  x: 12.89436956213913, y: -15.1111514016986312, z: -0.22330757889265573,
     vx: 0.00296460137564761618 * DAYS_PER_YEAR, vy: 0.00237847173959480950 * DAYS_PER_YEAR,
     vz: -0.00029658956854023756 * DAYS_PER_YEAR, mass: 0.0000436624404335156298 * SOLAR_MASS},
    {name: "neptune", x: 15.3796971148509165, y: -25.9193146099879641, z: 0.17925877295037118,
     vx: 0.00268067772490389322 * DAYS_PER_YEAR, vy: 0.00162824170038242295 * DAYS_PER_YEAR,
     vz: -0.00009515922545197159 * DAYS_PER_YEAR, mass: 0.0000515138902046611451 * SOLAR_MASS},
}

// Offset momentum
func offsetMomentum() {
    px := 0.0
    py := 0.0
    pz := 0.0
    for i := 2; i <= #bodies; i++ {
        b := bodies[i]
        px = px + b.vx * b.mass
        py = py + b.vy * b.mass
        pz = pz + b.vz * b.mass
    }
    sun := bodies[1]
    sun.vx = -px / SOLAR_MASS
    sun.vy = -py / SOLAR_MASS
    sun.vz = -pz / SOLAR_MASS
}

func energy() {
    e := 0.0
    n := #bodies
    for i := 1; i <= n; i++ {
        bi := bodies[i]
        e = e + 0.5 * bi.mass * (bi.vx * bi.vx + bi.vy * bi.vy + bi.vz * bi.vz)
        for j := i + 1; j <= n; j++ {
            bj := bodies[j]
            dx := bi.x - bj.x
            dy := bi.y - bj.y
            dz := bi.z - bj.z
            dist := math.sqrt(dx * dx + dy * dy + dz * dz)
            e = e - bi.mass * bj.mass / dist
        }
    }
    return e
}

func advance(dt) {
    n := #bodies
    for i := 1; i <= n; i++ {
        bi := bodies[i]
        for j := i + 1; j <= n; j++ {
            bj := bodies[j]
            dx := bi.x - bj.x
            dy := bi.y - bj.y
            dz := bi.z - bj.z
            dsq := dx * dx + dy * dy + dz * dz
            dist := math.sqrt(dsq)
            mag := dt / (dsq * dist)
            bi.vx = bi.vx - dx * bj.mass * mag
            bi.vy = bi.vy - dy * bj.mass * mag
            bi.vz = bi.vz - dz * bj.mass * mag
            bj.vx = bj.vx + dx * bi.mass * mag
            bj.vy = bj.vy + dy * bi.mass * mag
            bj.vz = bj.vz + dz * bi.mass * mag
        }
    }
    for i := 1; i <= n; i++ {
        b := bodies[i]
        b.x = b.x + dt * b.vx
        b.y = b.y + dt * b.vy
        b.z = b.z + dt * b.vz
    }
}

N := 500000
dt := 0.01

offsetMomentum()
e0 := energy()

t0 := time.now()
for i := 1; i <= N; i++ {
    advance(dt)
}
elapsed := time.since(t0)

e1 := energy()

print(string.format("nbody(%d steps)", N))
print(string.format("Energy before: %.9f", e0))
print(string.format("Energy after:  %.9f", e1))
print(string.format("Time: %.3fs", elapsed))
