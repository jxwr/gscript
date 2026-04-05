// loops_preheader_test.go tests computeLoopPreheaders and
// collectPreheaderInvariants — pure loop-analysis helpers that identify
// single-predecessor pre-header blocks around loop headers and list the
// SSA values those pre-headers define which are consumed inside the
// corresponding loop body. Tests run on any platform (no build tag) to
// match loops.go.

package methodjit

import (
	"os"
	"sort"
	"testing"
)

// TestComputeLoopPreheaders_Mandelbrot runs mandelbrot through the full
// optimization pipeline (up to and including LICM, which builds pre-headers)
// and verifies that computeLoopPreheaders identifies at least one header -> PH
// pair, and that each PH it returns is a deterministic single-successor
// outside predecessor of its header.
func TestComputeLoopPreheaders_Mandelbrot(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/mandelbrot.gs")
	if err != nil {
		t.Fatalf("read mandelbrot.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))

	target := findProtoByName(top, "mandelbrot")
	if target == nil {
		t.Fatalf("mandelbrot proto not found")
	}

	fn := BuildGraph(target)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = IntrinsicPass(fn)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	fn, _ = RangeAnalysisPass(fn)
	fn, _ = LICMPass(fn)

	li := computeLoopInfo(fn)
	preheaders := computeLoopPreheaders(fn, li)

	if len(preheaders) == 0 {
		t.Fatalf("expected LICM to have built at least one pre-header, got 0")
	}

	for headerID, phID := range preheaders {
		if !li.loopHeaders[headerID] {
			t.Errorf("preheaders[%d]=%d: %d is not a loop header", headerID, phID, headerID)
		}
		ph := findBlockByID(fn, phID)
		if ph == nil {
			t.Errorf("preheader block B%d not found in fn.Blocks", phID)
			continue
		}
		hdr := findBlockByID(fn, headerID)
		if hdr == nil {
			t.Errorf("header block B%d not found in fn.Blocks", headerID)
			continue
		}
		if len(ph.Succs) != 1 || ph.Succs[0] != hdr {
			t.Errorf("preheader B%d: Succs must be [B%d], got %v", phID, headerID, ph.Succs)
		}
		if body := li.headerBlocks[headerID]; body != nil && body[phID] {
			t.Errorf("preheader B%d must not be inside loop body of header B%d", phID, headerID)
		}
		t.Logf("header B%d has pre-header B%d", headerID, phID)
	}
}

// TestCollectPreheaderInvariants_Synthetic constructs a minimal hand-built
// loop IR with a dedicated pre-header block that holds two ConstFloat
// definitions. The body block consumes both via a MulFloat/AddFloat pair.
// The invariant collector must return exactly those two value IDs in
// sorted ascending order.
func TestCollectPreheaderInvariants_Synthetic(t *testing.T) {
	fn := &Function{NumRegs: 1}

	// Blocks:
	//   b0 (entry) -> bPh (pre-header) -> bHdr (header, phi)
	//                                      -> bBody (uses both consts) -> bHdr
	//                                      -> bExit (return)
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	bPh := &Block{ID: 1, defs: make(map[int]*Value)}
	bHdr := &Block{ID: 2, defs: make(map[int]*Value)}
	bBody := &Block{ID: 3, defs: make(map[int]*Value)}
	bExit := &Block{ID: 4, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0, bPh, bHdr, bBody, bExit}

	b0.Succs = []*Block{bPh}
	bPh.Preds = []*Block{b0}
	bPh.Succs = []*Block{bHdr}
	bHdr.Preds = []*Block{bPh, bBody}
	bHdr.Succs = []*Block{bBody, bExit}
	bBody.Preds = []*Block{bHdr}
	bBody.Succs = []*Block{bHdr}
	bExit.Preds = []*Block{bHdr}

	// b0: vSeed = ConstFloat 0.0; jump bPh
	vSeed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0,
		Aux: int64(bPh.ID)}
	b0.Instrs = []*Instr{vSeed, b0Term}

	// bPh: vC1 = ConstFloat; vC2 = ConstFloat; jump bHdr
	vC1 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: bPh, Aux: 0}
	vC2 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: bPh, Aux: 0}
	bPhTerm := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: bPh,
		Aux: int64(bHdr.ID)}
	bPh.Instrs = []*Instr{vC1, vC2, bPhTerm}

	// bHdr: phi(vSeed from bPh, vBody from bBody); cond=true; branch bBody/bExit
	vPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: bHdr, Aux: 0}
	vCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: bHdr, Aux: 1}
	bHdrTerm := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: bHdr,
		Args: []*Value{vCond.Value()},
		Aux:  int64(bBody.ID), Aux2: int64(bExit.ID)}
	bHdr.Instrs = []*Instr{vPhi, vCond, bHdrTerm}

	// bBody: vMul = MulFloat(vC1, vC1); vAdd = AddFloat(vPhi, vC2); jump bHdr
	vMul := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Block: bBody,
		Args: []*Value{vC1.Value(), vC1.Value()}}
	vAdd := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: bBody,
		Args: []*Value{vPhi.Value(), vC2.Value()}}
	bBodyTerm := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: bBody,
		Aux: int64(bHdr.ID)}
	bBody.Instrs = []*Instr{vMul, vAdd, bBodyTerm}

	// Wire phi args: from bPh -> vSeed, from bBody -> vAdd.
	vPhi.Args = []*Value{vSeed.Value(), vAdd.Value()}

	// bExit: return vPhi
	bExitTerm := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: bExit,
		Args: []*Value{vPhi.Value()}}
	bExit.Instrs = []*Instr{bExitTerm}

	li := computeLoopInfo(fn)
	if !li.loopHeaders[bHdr.ID] {
		t.Fatalf("expected B%d to be detected as loop header; loopHeaders=%v",
			bHdr.ID, li.loopHeaders)
	}
	preheaders := computeLoopPreheaders(fn, li)
	if len(preheaders) != 1 {
		t.Fatalf("expected exactly 1 pre-header pair, got %d: %v", len(preheaders), preheaders)
	}
	if got := preheaders[bHdr.ID]; got != bPh.ID {
		t.Fatalf("preheaders[B%d] = %d, want %d", bHdr.ID, got, bPh.ID)
	}

	inv := collectPreheaderInvariants(fn, li, preheaders)
	got := inv[bHdr.ID]
	if len(got) != 2 {
		t.Fatalf("expected 2 invariants for header B%d, got %d: %v", bHdr.ID, len(got), got)
	}

	// Expected set: {vC1.ID, vC2.ID}.
	want := []int{vC1.ID, vC2.ID}
	sort.Ints(want)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("invariant[%d] = %d, want %d (got=%v want=%v)",
				i, got[i], want[i], got, want)
		}
	}
	if !sort.IntsAreSorted(got) {
		t.Fatalf("invariants not sorted ascending: %v", got)
	}

	// Sanity: values defined in bHdr itself must NOT appear in the invariant set.
	for _, id := range got {
		if id == vPhi.ID || id == vCond.ID {
			t.Fatalf("value v%d defined in header B%d leaked into invariants %v",
				id, bHdr.ID, got)
		}
	}
}
