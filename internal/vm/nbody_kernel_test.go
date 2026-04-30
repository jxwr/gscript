package vm

import (
	"math"
	"testing"
)

const nbodyKernelTestProgram = `
SOLAR_MASS := 39.47841760435743
bodies := {
    {name: "a", x: 0, y: 0, z: 0, vx: 0, vy: 0, vz: 0, mass: SOLAR_MASS},
    {name: "b", x: 4.841, y: -1.160, z: -0.103, vx: 0.606, vy: 2.811, vz: -0.025, mass: 0.0377},
    {name: "c", x: 8.343, y: 4.124, z: -0.403, vx: -1.010, vy: 1.825, vz: 0.084, mass: 0.0112},
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
`

func TestNBodyAdvanceKernelRecognizesStructuralProto(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, nbodyKernelTestProgram)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	if len(proto.Protos) != 1 || !isNBodyAdvanceProto(proto.Protos[0]) {
		t.Fatal("advance proto not recognized")
	}
}

func TestNBodyAdvanceKernelMatchesFallback(t *testing.T) {
	kernelGlobals := compileAndRun(t, nbodyKernelTestProgram+`
for i := 1; i <= 5; i++ { advance(0.01) }
result := bodies[1].x + bodies[1].y + bodies[2].vx + bodies[3].vz
`)
	fallbackGlobals := compileAndRun(t, nbodyKernelTestProgram+`
sqrt0 := math.sqrt
math.sqrt = func(x) { return sqrt0(x) }
for i := 1; i <= 5; i++ { advance(0.01) }
result := bodies[1].x + bodies[1].y + bodies[2].vx + bodies[3].vz
`)
	got := kernelGlobals["result"].Number()
	want := fallbackGlobals["result"].Number()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("kernel result %.15f, fallback %.15f", got, want)
	}
}

func TestNBodyAdvanceLoopKernelMatchesFallback(t *testing.T) {
	kernelGlobals := compileAndRun(t, nbodyKernelTestProgram+`
N := 1500
dt := 0.01
for i := 1; i <= N; i++ { advance(dt) }
result := bodies[1].x + bodies[1].y + bodies[2].vx + bodies[3].vz
`)
	fallbackGlobals := compileAndRun(t, nbodyKernelTestProgram+`
sqrt0 := math.sqrt
math.sqrt = func(x) { return sqrt0(x) }
N := 1500
dt := 0.01
for i := 1; i <= N; i++ { advance(dt) }
result := bodies[1].x + bodies[1].y + bodies[2].vx + bodies[3].vz
`)
	got := kernelGlobals["result"].Number()
	want := fallbackGlobals["result"].Number()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("loop kernel result %.15f, fallback %.15f", got, want)
	}
}

func TestNBodyAdvanceKernelFallsBackOnAliasedBodyRecords(t *testing.T) {
	globals := compileAndRun(t, nbodyKernelTestProgram+`
bodies[2] = bodies[1]
sqrt0 := math.sqrt
calls := 0
math.sqrt = func(x) {
    calls = calls + 1
    return sqrt0(x)
}
advance(0.01)
`)
	expectGlobalInt(t, globals, "calls", 3)
}
