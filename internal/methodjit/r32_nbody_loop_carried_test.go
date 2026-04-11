//go:build darwin && arm64

// r32_nbody_loop_carried_test.go is the R32 diagnostic test for the nbody
// advance() j-loop. It:
//   1. Runs advance() through TieringManager to collect real Tier 1 feedback.
//   2. Compiles to native via RunTier2Pipeline + AllocateRegisters + Compile.
//   3. Dumps the ARM64 binary to /tmp/gscript_nbody_advance_r32.bin.
//   4. Counts loop-body GetField/SetField pairs (scalar promotion candidates).
//   5. Reports field-name constant pool so field[N] can be mapped to names.

package methodjit

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Suppress unused import.
var _ fmt.Stringer

func TestR32_NbodyLoopCarried(t *testing.T) {
	// Inline source to keep feedback identical to production nbody.gs
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

	// Step 1: Run through TieringManager to collect Tier 1 feedback.
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	// Step 2: Find the advance proto.
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

	t.Logf("advance: CallCount=%d Tier2Promoted=%v", advanceProto.CallCount, advanceProto.Tier2Promoted)

	// Step 3: Print field constant pool for field[N] → name mapping.
	t.Logf("=== CONSTANT POOL (field names) ===")
	for i, c := range advanceProto.Constants {
		if c.IsString() {
			t.Logf("  constants[%d] = %q", i, c.Str())
		}
	}

	// Step 4: Build IR + run full pipeline with feedback.
	advanceProto.EnsureFeedback()
	fn := BuildGraph(advanceProto)
	fn2, _, pipeErr := RunTier2Pipeline(fn, nil)
	if pipeErr != nil {
		t.Fatalf("pipeline error: %v", pipeErr)
	}

	irAfter := Print(fn2)
	t.Logf("\n=== FULL IR AFTER PIPELINE ===\n%s", irAfter)

	// Step 5: Analyze the j-loop body block for GetField/SetField pairs.
	// The j-loop body is identified as the block with the most GetField+SetField
	// ops (B2 in the current IR layout).
	type fieldAccess struct {
		objID int
		field int64
	}
	getFields := map[fieldAccess]int{} // (obj,field) -> count
	setFields := map[fieldAccess]int{} // (obj,field) -> count

	// Find the block with most GetField+SetField activity (j-loop body).
	type blockStat struct {
		blockID  int
		gfCount  int
		sfCount  int
		totalOps int
	}
	var blockStats []blockStat

	for _, blk := range fn2.Blocks {
		gf, sf := 0, 0
		for _, instr := range blk.Instrs {
			if instr.Op == OpGetField {
				gf++
				if len(instr.Args) > 0 {
					k := fieldAccess{objID: instr.Args[0].ID, field: instr.Aux}
					getFields[k]++
				}
			}
			if instr.Op == OpSetField {
				sf++
				if len(instr.Args) > 0 {
					k := fieldAccess{objID: instr.Args[0].ID, field: instr.Aux}
					setFields[k]++
				}
			}
		}
		if gf+sf > 0 {
			blockStats = append(blockStats, blockStat{blk.ID, gf, sf, len(blk.Instrs)})
		}
	}

	t.Logf("\n=== BLOCK GetField/SetField BREAKDOWN ===")
	for _, bs := range blockStats {
		t.Logf("  B%d: GetField=%d SetField=%d totalOps=%d", bs.blockID, bs.gfCount, bs.sfCount, bs.totalOps)
	}

	// Identify loop-carried (both GetField and SetField on same obj+field).
	t.Logf("\n=== LOOP-CARRIED (obj,field) PAIRS (GetField AND SetField in same block) ===")
	loopCarriedCount := 0

	// Collect by block separately.
	for _, blk := range fn2.Blocks {
		blkGet := map[fieldAccess]int{}
		blkSet := map[fieldAccess]int{}
		for _, instr := range blk.Instrs {
			if instr.Op == OpGetField && len(instr.Args) > 0 {
				k := fieldAccess{objID: instr.Args[0].ID, field: instr.Aux}
				blkGet[k]++
			}
			if instr.Op == OpSetField && len(instr.Args) > 0 {
				k := fieldAccess{objID: instr.Args[0].ID, field: instr.Aux}
				blkSet[k]++
			}
		}
		for k := range blkGet {
			if blkSet[k] > 0 {
				loopCarriedCount++
				fieldName := ""
				if int(k.field) < len(advanceProto.Constants) {
					c := advanceProto.Constants[k.field]
					if c.IsString() {
						fieldName = c.Str()
					}
				}
				t.Logf("  B%d: obj=v%d field[%d]=%q → GetField×%d SetField×%d (CANDIDATE)",
					blk.ID, k.objID, k.field, fieldName, blkGet[k], blkSet[k])
			}
		}
	}
	t.Logf("Total loop-carried (obj,field) pairs: %d", loopCarriedCount)

	// Count total GetField and SetField in inner j-loop body.
	t.Logf("\n=== IR OP COUNTS (full function) ===")
	var totalGF, totalSF, totalMath, totalGuard, totalOps int
	for _, blk := range fn2.Blocks {
		for _, instr := range blk.Instrs {
			totalOps++
			switch instr.Op {
			case OpGetField:
				totalGF++
			case OpSetField:
				totalSF++
			case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpSqrt:
				totalMath++
			case OpGuardType:
				totalGuard++
			}
		}
	}
	t.Logf("  GetField: %d", totalGF)
	t.Logf("  SetField: %d", totalSF)
	t.Logf("  Float math ops: %d", totalMath)
	t.Logf("  GuardType: %d", totalGuard)
	t.Logf("  Total: %d", totalOps)

	// Step 6: Compile to native and dump binary.
	fn2.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn2)
	cf, compErr := Compile(fn2, alloc)
	if compErr != nil {
		t.Fatalf("Compile error: %v", compErr)
	}
	t.Cleanup(func() { cf.Code.Free() })

	size := cf.Code.Size()
	src2 := unsafe.Slice((*byte)(cf.Code.Ptr()), size)
	outBytes := make([]byte, size)
	copy(outBytes, src2)

	outPath := "/tmp/gscript_nbody_advance_r32.bin"
	if err := os.WriteFile(outPath, outBytes, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	t.Logf("\nWrote %d bytes to %s", size, outPath)
	t.Logf("DirectEntryOffset=%d NumSpills=%d", cf.DirectEntryOffset, cf.NumSpills)

	// Verify binary is from Tier 2 (size > 0 and non-trivial).
	if size < 64 {
		t.Errorf("CROSS-CHECK FAIL: binary too small (%d bytes) — not a real Tier 2 compile", size)
	} else {
		t.Logf("CROSS-CHECK (b): binary size=%d bytes — plausible Tier 2 output", size)
	}

	// Verify IR mentions advance.
	if !strings.Contains(irAfter, "function advance") {
		t.Errorf("CROSS-CHECK (c): IR does not mention 'advance'")
	} else {
		t.Logf("CROSS-CHECK (c): IR function name = 'advance' — OK")
	}
}
