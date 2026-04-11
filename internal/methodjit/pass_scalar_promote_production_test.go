//go:build darwin && arm64

// pass_scalar_promote_production_test.go is an observe-only diagnostic that
// runs LoopScalarPromotionPass through the production pipeline on nbody's
// advance() and logs the unpromoted (GetField+SetField) pair count plus the
// number of TypeFloat phis inserted by the pass. It is kept as a template
// for real-pipeline pass verification (the rule from R31/R32/R33). It does
// NOT assert pass/fail because R33 proved the plan's premise was incomplete
// — see opt/premise_error.md: even with a correct float gate, the pass bails
// earlier on exit-block-preds and isInvariantObj, so the unpromoted count
// stays at 9 and float-phi count at 0. Once the upstream gates are relaxed,
// flip this test to assert unpromoted ≤ 6 and floatPhis ≥ 3.

package methodjit

import (
	"fmt"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestR33_ScalarPromoteFiresOnNbody(t *testing.T) {
	src := `
bodies := {
    {name: "sun",     x: 0.0, y: 0.0, z: 0.0, vx: 0.0172, vy: 0.0, vz: 0.0, mass: 39.478},
    {name: "jupiter", x: 4.841, y: -1.160, z: -0.103, vx: 0.6069, vy: 2.811, vz: -0.0252, mass: 0.03770},
    {name: "saturn",  x: 8.343, y: 4.124, z: -0.403, vx: -1.010, vy: 1.825, vz: 0.0841, mass: 0.01128},
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

for iter := 1; iter <= 10; iter++ {
    advance(0.01)
}
`

	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	var advanceProto *vm.FuncProto
	for _, child := range proto.Protos {
		if child.Name == "advance" {
			advanceProto = child
			break
		}
	}
	if advanceProto == nil {
		t.Fatal("advance proto not found")
	}

	advanceProto.EnsureFeedback()
	fn := BuildGraph(advanceProto)
	fn2, _, pipeErr := RunTier2Pipeline(fn, nil)
	if pipeErr != nil {
		t.Fatalf("pipeline error: %v", pipeErr)
	}

	type fieldAccess struct {
		objID int
		field int64
	}
	unpromoted := 0
	var surviving []string
	for _, blk := range fn2.Blocks {
		blkGet := map[fieldAccess]int{}
		blkSet := map[fieldAccess]int{}
		for _, instr := range blk.Instrs {
			if instr.Op == OpGetField && len(instr.Args) > 0 {
				blkGet[fieldAccess{instr.Args[0].ID, instr.Aux}]++
			}
			if instr.Op == OpSetField && len(instr.Args) > 0 {
				blkSet[fieldAccess{instr.Args[0].ID, instr.Aux}]++
			}
		}
		for k, g := range blkGet {
			if s := blkSet[k]; s > 0 {
				unpromoted++
				name := ""
				if int(k.field) < len(advanceProto.Constants) {
					if c := advanceProto.Constants[k.field]; c.IsString() {
						name = c.Str()
					}
				}
				surviving = append(surviving, fmt.Sprintf("B%d obj=v%d field[%d]=%q get=%d set=%d",
					blk.ID, k.objID, k.field, name, g, s))
			}
		}
	}

	floatPhis := 0
	for _, blk := range fn2.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpPhi && instr.Type == TypeFloat {
				floatPhis++
			}
		}
	}

	t.Logf("unpromoted pairs=%d float-phis=%d", unpromoted, floatPhis)
	for _, s := range surviving {
		t.Logf("  surviving: %s", s)
	}
	t.Skip("observe-only; see opt/premise_error.md (R33) — pass bails on exit-block-preds before classification, so unpromoted/floatPhi counts do not reflect the gate-fix hypothesis")
}
