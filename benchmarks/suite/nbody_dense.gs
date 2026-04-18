// R51: nbody ported to DenseMatrix backing.
// bodies is a 5×7 matrix: row = body index (0..4), col = field index (0..6).
// Field layout: 0=x, 1=y, 2=z, 3=vx, 4=vy, 5=vz, 6=mass.
// All field accesses go through matrix.getf/setf → JIT-intrinsic flat
// float64 LDR/STR with no shape-IC / NaN-box cost.

PI := 3.141592653589793
SOLAR_MASS := 4 * PI * PI
DAYS_PER_YEAR := 365.24

N_BODIES := 5
F_X := 0
F_Y := 1
F_Z := 2
F_VX := 3
F_VY := 4
F_VZ := 5
F_MASS := 6

bodies := matrix.dense(N_BODIES, 7)

// sun (index 0)
matrix.setf(bodies, 0, F_MASS, SOLAR_MASS)

// jupiter (index 1)
matrix.setf(bodies, 1, F_X,    4.84143144246472090)
matrix.setf(bodies, 1, F_Y,   -1.16032004402742839)
matrix.setf(bodies, 1, F_Z,   -0.10362204447112311)
matrix.setf(bodies, 1, F_VX,   0.00166007664274403694 * DAYS_PER_YEAR)
matrix.setf(bodies, 1, F_VY,   0.00769901118419740425 * DAYS_PER_YEAR)
matrix.setf(bodies, 1, F_VZ,  -0.00006905169867435090 * DAYS_PER_YEAR)
matrix.setf(bodies, 1, F_MASS, 0.000954791938424326609 * SOLAR_MASS)

// saturn (index 2)
matrix.setf(bodies, 2, F_X,    8.34336671824457987)
matrix.setf(bodies, 2, F_Y,    4.12479856412430479)
matrix.setf(bodies, 2, F_Z,   -0.40352341711789204)
matrix.setf(bodies, 2, F_VX,  -0.00276742510726862411 * DAYS_PER_YEAR)
matrix.setf(bodies, 2, F_VY,   0.00499852801234917238 * DAYS_PER_YEAR)
matrix.setf(bodies, 2, F_VZ,   0.00023041729757376393 * DAYS_PER_YEAR)
matrix.setf(bodies, 2, F_MASS, 0.000285885980666130812 * SOLAR_MASS)

// uranus (index 3)
matrix.setf(bodies, 3, F_X,   12.89436956213913)
matrix.setf(bodies, 3, F_Y,  -15.1111514016986312)
matrix.setf(bodies, 3, F_Z,   -0.22330757889265573)
matrix.setf(bodies, 3, F_VX,   0.00296460137564761618 * DAYS_PER_YEAR)
matrix.setf(bodies, 3, F_VY,   0.00237847173959480950 * DAYS_PER_YEAR)
matrix.setf(bodies, 3, F_VZ,  -0.00029658956854023756 * DAYS_PER_YEAR)
matrix.setf(bodies, 3, F_MASS, 0.0000436624404335156298 * SOLAR_MASS)

// neptune (index 4)
matrix.setf(bodies, 4, F_X,   15.3796971148509165)
matrix.setf(bodies, 4, F_Y,  -25.9193146099879641)
matrix.setf(bodies, 4, F_Z,    0.17925877295037118)
matrix.setf(bodies, 4, F_VX,   0.00268067772490389322 * DAYS_PER_YEAR)
matrix.setf(bodies, 4, F_VY,   0.00162824170038242295 * DAYS_PER_YEAR)
matrix.setf(bodies, 4, F_VZ,  -0.00009515922545197159 * DAYS_PER_YEAR)
matrix.setf(bodies, 4, F_MASS, 0.0000515138902046611451 * SOLAR_MASS)

func offsetMomentum() {
    px := 0.0
    py := 0.0
    pz := 0.0
    for i := 1; i < N_BODIES; i++ {
        m := matrix.getf(bodies, i, F_MASS)
        px = px + matrix.getf(bodies, i, F_VX) * m
        py = py + matrix.getf(bodies, i, F_VY) * m
        pz = pz + matrix.getf(bodies, i, F_VZ) * m
    }
    matrix.setf(bodies, 0, F_VX, -px / SOLAR_MASS)
    matrix.setf(bodies, 0, F_VY, -py / SOLAR_MASS)
    matrix.setf(bodies, 0, F_VZ, -pz / SOLAR_MASS)
}

func energy() {
    e := 0.0
    for i := 0; i < N_BODIES; i++ {
        mi := matrix.getf(bodies, i, F_MASS)
        vx := matrix.getf(bodies, i, F_VX)
        vy := matrix.getf(bodies, i, F_VY)
        vz := matrix.getf(bodies, i, F_VZ)
        e = e + 0.5 * mi * (vx * vx + vy * vy + vz * vz)
        for j := i + 1; j < N_BODIES; j++ {
            dx := matrix.getf(bodies, i, F_X) - matrix.getf(bodies, j, F_X)
            dy := matrix.getf(bodies, i, F_Y) - matrix.getf(bodies, j, F_Y)
            dz := matrix.getf(bodies, i, F_Z) - matrix.getf(bodies, j, F_Z)
            dist := math.sqrt(dx * dx + dy * dy + dz * dz)
            e = e - mi * matrix.getf(bodies, j, F_MASS) / dist
        }
    }
    return e
}

func advance(dt) {
    for i := 0; i < N_BODIES; i++ {
        bix  := matrix.getf(bodies, i, F_X)
        biy  := matrix.getf(bodies, i, F_Y)
        biz  := matrix.getf(bodies, i, F_Z)
        bim  := matrix.getf(bodies, i, F_MASS)
        bivx := matrix.getf(bodies, i, F_VX)
        bivy := matrix.getf(bodies, i, F_VY)
        bivz := matrix.getf(bodies, i, F_VZ)
        for j := i + 1; j < N_BODIES; j++ {
            bjx := matrix.getf(bodies, j, F_X)
            bjy := matrix.getf(bodies, j, F_Y)
            bjz := matrix.getf(bodies, j, F_Z)
            bjm := matrix.getf(bodies, j, F_MASS)
            bjvx := matrix.getf(bodies, j, F_VX)
            bjvy := matrix.getf(bodies, j, F_VY)
            bjvz := matrix.getf(bodies, j, F_VZ)
            dx := bix - bjx
            dy := biy - bjy
            dz := biz - bjz
            dsq := dx * dx + dy * dy + dz * dz
            dist := math.sqrt(dsq)
            mag := dt / (dsq * dist)
            bivx = bivx - dx * bjm * mag
            bivy = bivy - dy * bjm * mag
            bivz = bivz - dz * bjm * mag
            matrix.setf(bodies, j, F_VX, bjvx + dx * bim * mag)
            matrix.setf(bodies, j, F_VY, bjvy + dy * bim * mag)
            matrix.setf(bodies, j, F_VZ, bjvz + dz * bim * mag)
        }
        matrix.setf(bodies, i, F_VX, bivx)
        matrix.setf(bodies, i, F_VY, bivy)
        matrix.setf(bodies, i, F_VZ, bivz)
    }
    for i := 0; i < N_BODIES; i++ {
        matrix.setf(bodies, i, F_X, matrix.getf(bodies, i, F_X) + dt * matrix.getf(bodies, i, F_VX))
        matrix.setf(bodies, i, F_Y, matrix.getf(bodies, i, F_Y) + dt * matrix.getf(bodies, i, F_VY))
        matrix.setf(bodies, i, F_Z, matrix.getf(bodies, i, F_Z) + dt * matrix.getf(bodies, i, F_VZ))
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

print(string.format("nbody_dense(%d steps)", N))
print(string.format("Energy before: %.9f", e0))
print(string.format("Energy after:  %.9f", e1))
print(string.format("Time: %.3fs", elapsed))
