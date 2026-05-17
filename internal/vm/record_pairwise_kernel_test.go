package vm

import (
	"math"
	"os"
	"strings"
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

func TestNBodyDenseAdvanceKernelRecognizesBenchmarkProto(t *testing.T) {
	src, err := os.ReadFile("../../benchmarks/suite/nbody_dense.gs")
	if err != nil {
		t.Fatalf("read nbody_dense benchmark: %v", err)
	}
	proto, vm := compileSpectralKernelTestProgram(t, string(src))
	defer vm.Close()
	advance := findTestProtoByName(proto, "advance")
	if advance == nil || !isNBodyDenseAdvanceProto(advance) || !HasNBodyAdvanceWholeCallKernel(advance) {
		t.Fatal("dense advance proto not recognized")
	}
}

func TestNBodyDenseAdvanceLoopKernelMatchesFallback(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/nbody_dense.gs")
	if err != nil {
		t.Fatalf("read nbody_dense benchmark: %v", err)
	}
	src := strings.Replace(string(srcBytes), "N := 500000", "N := 1500", 1)
	kernelGlobals := compileAndRun(t, src)
	fallbackGlobals := compileAndRun(t, strings.Replace(src, "dt := 0.01", `
getf0 := matrix.getf
setf0 := matrix.setf
matrix.getf = func(m, i, j) { return getf0(m, i, j) }
matrix.setf = func(m, i, j, v) { return setf0(m, i, j, v) }
dt := 0.01`, 1))
	got := kernelGlobals["e1"].Number()
	want := fallbackGlobals["e1"].Number()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("dense kernel e1 %.15f, fallback %.15f", got, want)
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

func findTestProtoByName(proto *FuncProto, name string) *FuncProto {
	if proto == nil {
		return nil
	}
	if proto.Name == name {
		return proto
	}
	for _, child := range proto.Protos {
		if got := findTestProtoByName(child, name); got != nil {
			return got
		}
	}
	return nil
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

func TestNBodyAdvanceKernelDerivesRecordSpecFromBytecode(t *testing.T) {
	const src = `
SOLAR_MASS := 39.47841760435743
m := math
planets := {
    {label: "a", px: 0, py: 0, pz: 0, sx: 0, sy: 0, sz: 0, weight: SOLAR_MASS},
    {label: "b", px: 4.841, py: -1.160, pz: -0.103, sx: 0.606, sy: 2.811, sz: -0.025, weight: 0.0377},
    {label: "c", px: 8.343, py: 4.124, pz: -0.403, sx: -1.010, sy: 1.825, sz: 0.084, weight: 0.0112},
}
func advance(dt) {
    n := #planets
    for i := 1; i <= n; i++ {
        bi := planets[i]
        for j := i + 1; j <= n; j++ {
            bj := planets[j]
            dx := bi.px - bj.px
            dy := bi.py - bj.py
            dz := bi.pz - bj.pz
            dsq := dx * dx + dy * dy + dz * dz
            dist := m.sqrt(dsq)
            mag := dt / (dsq * dist)
            bi.sx = bi.sx - dx * bj.weight * mag
            bi.sy = bi.sy - dy * bj.weight * mag
            bi.sz = bi.sz - dz * bj.weight * mag
            bj.sx = bj.sx + dx * bi.weight * mag
            bj.sy = bj.sy + dy * bi.weight * mag
            bj.sz = bj.sz + dz * bi.weight * mag
        }
    }
    for i := 1; i <= n; i++ {
        b := planets[i]
        b.px = b.px + dt * b.sx
        b.py = b.py + dt * b.sy
        b.pz = b.pz + dt * b.sz
    }
}
`
	proto, vm := compileSpectralKernelTestProgram(t, src)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	advance := findTestProtoByName(proto, "advance")
	if advance == nil || !isNBodyAdvanceProto(advance) {
		t.Fatal("renamed advance proto not recognized")
	}
	spec, ok := recordPairwiseAdvanceKernelSpecForProto(advance)
	if !ok {
		t.Fatal("record pairwise spec not derived")
	}
	if spec.tableName != "planets" || spec.sqrtTableName != "m" || spec.sqrtFieldName != "sqrt" {
		t.Fatalf("unexpected spec globals: %+v", spec)
	}
	wantFields := [nbodyFieldCount]string{"px", "py", "pz", "sx", "sy", "sz", "weight"}
	if spec.fieldNames != wantFields {
		t.Fatalf("field spec = %v, want %v", spec.fieldNames, wantFields)
	}

	kernelGlobals := compileAndRun(t, src+`
for i := 1; i <= 5; i++ { advance(0.01) }
result := planets[1].px + planets[1].py + planets[2].sx + planets[3].sz
`)
	fallbackGlobals := compileAndRun(t, src+`
sqrt0 := m.sqrt
m.sqrt = func(x) { return sqrt0(x) }
for i := 1; i <= 5; i++ { advance(0.01) }
result := planets[1].px + planets[1].py + planets[2].sx + planets[3].sz
`)
	got := kernelGlobals["result"].Number()
	want := fallbackGlobals["result"].Number()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("kernel result %.15f, fallback %.15f", got, want)
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
