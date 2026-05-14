package methodjit

import "github.com/gscript/gscript/internal/vm"

// TableIntArrayKernelPass recognizes small whole-region int-array kernels
// whose scalar fallback remains present in the CFG. It handles the
// prefix-reversal loop:
//
//	for lo < hi {
//	    t = a[lo]
//	    a[lo] = a[hi]
//	    a[hi] = t
//	    lo = lo + 1
//	    hi = hi - 1
//	}
//
// and the local-work-array prefix-copy loop:
//
//	for i := 1; i <= n; i++ {
//	    dst[i] = src[i]
//	}
//
// The rewritten preheader first tries a guarded native kernel. On success it
// branches to the original loop exit; on failure it branches to the original
// loop header. Prefix reversal accepts general int-array-shaped loops; prefix
// copy is limited to local work tables so its scalar fallback cannot be asked
// to recover arbitrary external-table copy semantics.
func TableIntArrayKernelPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	if li == nil || !li.hasLoops() {
		return fn, nil
	}
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if header == nil || !li.loopHeaders[header.ID] {
			continue
		}
		if cand, reason, ok := tableIntArrayReversePrefixCandidate(header, li.headerBlocks[header.ID]); ok {
			kernel := &Instr{
				ID:    fn.newValueID(),
				Op:    OpTableIntArrayReversePrefix,
				Type:  TypeBool,
				Args:  []*Value{cand.table, cand.hiSeed},
				Block: cand.preheader,
			}
			kernel.copySourceFrom(cand.source)
			insertKernelBranch(cand.preheader, cand.exit, cand.header, kernel)
			functionRemarks(fn).Add("TableIntArrayKernel", "changed", cand.preheader.ID, kernel.ID, kernel.Op,
				"guarded prefix-reversal loop with scalar fallback")
			continue
		} else if reason != "" {
			functionRemarks(fn).Add("TableIntArrayKernel", "missed", header.ID, 0, OpTableIntArrayReversePrefix, reason)
		}
		if cand, ok := tableIntArrayCopyPrefixCandidate(header, li.headerBlocks[header.ID]); ok {
			kernel := &Instr{
				ID:    fn.newValueID(),
				Op:    OpTableIntArrayCopyPrefix,
				Type:  TypeBool,
				Args:  []*Value{cand.dst, cand.src, cand.hi},
				Block: cand.preheader,
			}
			kernel.copySourceFrom(cand.source)
			insertKernelBranch(cand.preheader, cand.exit, cand.header, kernel)
			functionRemarks(fn).Add("TableIntArrayKernel", "changed", cand.preheader.ID, kernel.ID, kernel.Op,
				"guarded prefix-copy loop with scalar fallback")
			continue
		}
		if cand, reason, ok := tableArraySwapPairsCandidate(fn, header, li.headerBlocks[header.ID]); ok {
			kernel := &Instr{
				ID:    fn.newValueID(),
				Op:    OpTableArraySwapPairs,
				Type:  TypeBool,
				Args:  []*Value{cand.table, cand.start, cand.hi},
				Aux:   cand.kind,
				Block: cand.preheader,
			}
			kernel.copySourceFrom(cand.source)
			insertKernelBranch(cand.preheader, cand.exit, cand.header, kernel)
			functionRemarks(fn).Add("TableIntArrayKernel", "changed", cand.preheader.ID, kernel.ID, kernel.Op,
				"guarded adjacent pair-swap loop with scalar fallback")
		} else if reason != "" && loopBodyHasOp(li.headerBlocks[header.ID], fn, OpTableArraySwap) {
			functionRemarks(fn).Add("TableIntArrayKernel", "missed", header.ID, 0, OpTableArraySwapPairs, reason)
		}
	}
	return fn, nil
}

func loopBodyHasOp(bodySet map[int]bool, fn *Function, op Op) bool {
	if bodySet == nil || fn == nil {
		return false
	}
	for _, block := range fn.Blocks {
		if block == nil || !bodySet[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == op {
				return true
			}
		}
	}
	return false
}

func insertKernelBranch(preheader, success, fallback *Block, kernel *Instr) {
	term := blockTerminator(preheader)
	insertAt := len(preheader.Instrs) - 1
	preheader.Instrs = append(preheader.Instrs[:insertAt], append([]*Instr{kernel}, preheader.Instrs[insertAt:]...)...)
	term.Op = OpBranch
	term.Args = []*Value{kernel.Value()}
	term.Aux = int64(success.ID)
	term.Aux2 = int64(fallback.ID)
	preheader.Succs = []*Block{success, fallback}
	copyPhiArgsForNewPred(success, fallback, preheader)
	addPredIfMissing(success, preheader)
}

type tableIntArrayReversePrefixLoop struct {
	header    *Block
	preheader *Block
	body      *Block
	exit      *Block
	table     *Value
	hiSeed    *Value
	source    *Instr
}

type tableIntArrayCopyPrefixLoop struct {
	header    *Block
	preheader *Block
	body      *Block
	exit      *Block
	dst       *Value
	src       *Value
	hi        *Value
	source    *Instr
}

type tableArraySwapPairsLoop struct {
	header    *Block
	preheader *Block
	body      *Block
	exit      *Block
	table     *Value
	start     *Value
	hi        *Value
	kind      int64
	source    *Instr
}

func tableIntArrayCopyPrefixCandidate(header *Block, bodySet map[int]bool) (tableIntArrayCopyPrefixLoop, bool) {
	var cand tableIntArrayCopyPrefixLoop
	if header == nil || bodySet == nil {
		return cand, false
	}
	preheader := tableArrayStoreLoopPreheader(header, bodySet)
	if preheader == nil || blockTerminator(preheader) == nil || blockTerminator(preheader).Op != OpJump {
		return cand, false
	}
	term := blockTerminator(header)
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 || len(header.Succs) != 2 {
		return cand, false
	}
	cond := term.Args[0]
	if cond == nil || cond.Def == nil || cond.Def.Op != OpLeInt || len(cond.Def.Args) != 2 {
		return cand, false
	}
	idx := cond.Def.Args[0]
	hi := cond.Def.Args[1]
	if idx == nil || idx.Def == nil || idx.Def.Op != OpAddInt || len(idx.Def.Args) != 2 {
		return cand, false
	}
	var phi *Value
	if isHeaderPhi(idx.Def.Args[0], header) && isConstIntValue(idx.Def.Args[1], 1) {
		phi = idx.Def.Args[0]
	} else if isHeaderPhi(idx.Def.Args[1], header) && isConstIntValue(idx.Def.Args[0], 1) {
		phi = idx.Def.Args[1]
	} else {
		return cand, false
	}
	body, exit := branchLoopBodyExit(header, bodySet)
	if body == nil || exit == nil {
		return cand, false
	}
	if len(body.Succs) != 1 || body.Succs[0] != header || blockTerminator(body) == nil || blockTerminator(body).Op != OpJump {
		return cand, false
	}
	if !isConstIntValue(phiArgForPred(phi.Def, header, preheader), 0) || !sameSSAValue(phiArgForPred(phi.Def, header, body), idx) {
		return cand, false
	}
	match, ok := matchCopyPrefixBody(body, bodySet, idx)
	if !ok {
		return cand, false
	}
	cand.header = header
	cand.preheader = preheader
	cand.body = body
	cand.exit = exit
	cand.dst = match.dst
	cand.src = match.src
	cand.hi = hi
	cand.source = match.source
	return cand, true
}

func tableArraySwapPairsCandidate(fn *Function, header *Block, bodySet map[int]bool) (tableArraySwapPairsLoop, string, bool) {
	var cand tableArraySwapPairsLoop
	if fn == nil || header == nil || bodySet == nil {
		return cand, "", false
	}
	preheader := tableArrayStoreLoopPreheader(header, bodySet)
	if preheader == nil || blockTerminator(preheader) == nil || blockTerminator(preheader).Op != OpJump {
		return cand, "loop has no single jump preheader", false
	}
	if loopBodyHasOp(bodySet, nil, OpTableArraySwap) {
		panic("debug reverse candidate saw fused swap loop")
	}
	term := blockTerminator(header)
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 || len(header.Succs) != 2 {
		return cand, "loop header is not a two-way branch", false
	}
	cond := term.Args[0]
	if cond == nil || cond.Def == nil || cond.Def.Op != OpLeInt || len(cond.Def.Args) != 2 {
		return cand, "loop condition is not <= integer induction limit", false
	}
	idx := cond.Def.Args[0]
	hi := cond.Def.Args[1]
	if idx == nil || idx.Def == nil || idx.Def.Op != OpAddInt || len(idx.Def.Args) != 2 || hi == nil {
		return cand, "loop condition does not expose incremented index", false
	}
	var phi *Value
	if isHeaderPhi(idx.Def.Args[0], header) && isConstIntValue(idx.Def.Args[1], 2) {
		phi = idx.Def.Args[0]
	} else if isHeaderPhi(idx.Def.Args[1], header) && isConstIntValue(idx.Def.Args[0], 2) {
		phi = idx.Def.Args[1]
	} else {
		return cand, "loop induction step is not +2 from header phi", false
	}
	body, exit := branchLoopBodyExit(header, bodySet)
	if body == nil || exit == nil {
		return cand, "could not identify loop body and exit successors", false
	}
	if len(body.Succs) != 1 || body.Succs[0] != header || blockTerminator(body) == nil || blockTerminator(body).Op != OpJump {
		return cand, "loop body is not a single-block latch", false
	}
	seed := phiArgForPred(phi.Def, header, preheader)
	next := phiArgForPred(phi.Def, header, body)
	if !sameSSAValue(next, idx) {
		return cand, "loop phi backedge is not the incremented index", false
	}
	start, ok := constPlus(seed, 2)
	if !ok {
		return cand, "loop start is not a constant seed plus two", false
	}
	match, ok := matchSwapPairsBody(body, idx)
	if !ok {
		return cand, "loop body is not adjacent table-array swap", false
	}
	startInstr := &Instr{
		ID:    fn.newValueID(),
		Op:    OpConstInt,
		Type:  TypeInt,
		Aux:   start,
		Block: preheader,
	}
	insertBeforeTerminator(preheader, startInstr)
	cand.header = header
	cand.preheader = preheader
	cand.body = body
	cand.exit = exit
	cand.table = match.table
	cand.start = startInstr.Value()
	cand.hi = hi
	cand.kind = match.kind
	cand.source = match.source
	return cand, "", true
}

func branchLoopBodyExit(header *Block, bodySet map[int]bool) (*Block, *Block) {
	if header == nil || bodySet == nil || len(header.Succs) != 2 {
		return nil, nil
	}
	a, b := header.Succs[0], header.Succs[1]
	if a == nil || b == nil {
		return nil, nil
	}
	switch {
	case bodySet[a.ID] && !bodySet[b.ID]:
		return a, b
	case bodySet[b.ID] && !bodySet[a.ID]:
		return b, a
	default:
		return nil, nil
	}
}

type swapPairsBodyMatch struct {
	table  *Value
	kind   int64
	source *Instr
}

func matchSwapPairsBody(body *Block, idx *Value) (swapPairsBodyMatch, bool) {
	var match swapPairsBodyMatch
	var swap *Instr
	for _, instr := range body.Instrs {
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpTableArraySwap:
			if swap != nil || len(instr.Args) < 5 || (instr.Aux != int64(vm.FBKindInt) && instr.Aux != int64(vm.FBKindFloat)) {
				return match, false
			}
			if !sameSSAValue(instr.Args[3], idx) || !isAddOneOf(instr.Args[4], idx) {
				return match, false
			}
			swap = instr
		case OpAddInt, OpGuardTableKind, OpJump, OpNop:
			continue
		default:
			return match, false
		}
	}
	if swap == nil {
		return match, false
	}
	match.table = swap.Args[0]
	match.kind = swap.Aux
	match.source = swap
	return match, true
}

type copyPrefixBodyMatch struct {
	dst    *Value
	src    *Value
	source *Instr
}

func matchCopyPrefixBody(body *Block, bodySet map[int]bool, idx *Value) (copyPrefixBodyMatch, bool) {
	var match copyPrefixBodyMatch
	var load *Instr
	var store *Instr
	for _, instr := range body.Instrs {
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpTableArrayLoad:
			if len(instr.Args) != 3 || instr.Aux != int64(vm.FBKindInt) || !sameSSAValue(instr.Args[2], idx) {
				return match, false
			}
			if load != nil {
				return match, false
			}
			load = instr
		case OpSetTable:
			if len(instr.Args) != 3 || instr.Aux2 != int64(vm.FBKindInt) || !sameSSAValue(instr.Args[1], idx) {
				return match, false
			}
			if store != nil {
				return match, false
			}
			store = instr
		case OpTableArrayStore:
			if len(instr.Args) < 5 || instr.Aux != int64(vm.FBKindInt) || !sameSSAValue(instr.Args[3], idx) {
				return match, false
			}
			if store != nil {
				return match, false
			}
			store = instr
		case OpAddInt, OpGuardTableKind, OpJump:
			continue
		default:
			return match, false
		}
	}
	if load == nil || store == nil {
		return match, false
	}
	valArg := 2
	if store.Op == OpTableArrayStore {
		valArg = 4
	}
	if !sameSSAValue(store.Args[valArg], load.Value()) {
		return match, false
	}
	src := tableFromArrayDataValue(load.Args[0])
	if src == nil {
		return match, false
	}
	if !tableIntArrayKernelLocalTable(store.Args[0], bodySet) || !tableIntArrayKernelLocalTable(src, bodySet) {
		return match, false
	}
	match.dst = store.Args[0]
	match.src = src
	match.source = store
	return match, true
}

func tableIntArrayKernelLocalTable(table *Value, body map[int]bool) bool {
	if table == nil || table.Def == nil || table.Def.Op != OpNewTable || table.Def.Block == nil {
		return false
	}
	return body == nil || !body[table.Def.Block.ID]
}

func tableFromArrayDataValue(data *Value) *Value {
	if data == nil || data.Def == nil || data.Def.Op != OpTableArrayData || len(data.Def.Args) != 1 {
		return nil
	}
	header := data.Def.Args[0]
	if header == nil || header.Def == nil || header.Def.Op != OpTableArrayHeader || len(header.Def.Args) != 1 {
		return nil
	}
	return header.Def.Args[0]
}

func tableIntArrayReversePrefixCandidate(header *Block, bodySet map[int]bool) (tableIntArrayReversePrefixLoop, string, bool) {
	var cand tableIntArrayReversePrefixLoop
	if header == nil || bodySet == nil {
		return cand, "", false
	}
	preheader := tableArrayStoreLoopPreheader(header, bodySet)
	if preheader == nil || blockTerminator(preheader) == nil || blockTerminator(preheader).Op != OpJump {
		return cand, "loop has no single jump preheader", false
	}
	term := blockTerminator(header)
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 || len(header.Succs) != 2 {
		return cand, "loop header is not a two-way branch", false
	}
	cond := term.Args[0]
	if cond == nil || cond.Def == nil || cond.Def.Op != OpLtInt || len(cond.Def.Args) != 2 {
		return cand, "loop condition is not < integer bounds", false
	}
	phiA := cond.Def.Args[0]
	phiB := cond.Def.Args[1]
	if !isHeaderPhi(phiA, header) || !isHeaderPhi(phiB, header) {
		return cand, "loop condition operands are not header phis", false
	}
	body, exit := branchLoopBodyExit(header, bodySet)
	if body == nil || exit == nil || blockStartsWithPhi(exit) {
		return cand, "could not identify loop body and exit successors", false
	}
	if len(body.Succs) != 1 || body.Succs[0] != header || blockTerminator(body) == nil || blockTerminator(body).Op != OpJump {
		return cand, "loop body is not a single-block latch", false
	}
	loPhi, hiPhi, hiSeed, ok := reversePrefixLoopPhis(header, preheader, body, phiA, phiB)
	if !ok {
		return cand, "loop phi seeds/backedges do not match prefix reverse", false
	}
	match, ok := matchReversePrefixBody(body, loPhi, hiPhi)
	if !ok {
		return cand, "loop body is not prefix reverse swap", false
	}
	cand.header = header
	cand.preheader = preheader
	cand.body = body
	cand.exit = exit
	cand.table = match.table
	cand.hiSeed = hiSeed
	cand.source = match.source
	return cand, "", true
}

type reversePrefixBodyMatch struct {
	table  *Value
	source *Instr
}

func matchReversePrefixBody(body *Block, loPhi, hiPhi *Value) (reversePrefixBodyMatch, bool) {
	var match reversePrefixBodyMatch
	var loLoad, hiLoad, loStore, hiStore *Instr
	var fusedSwap *Instr
	for _, instr := range body.Instrs {
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpTableArraySwap:
			if len(instr.Args) < 5 || instr.Aux != int64(vm.FBKindInt) {
				return match, false
			}
			if !sameSSAValue(instr.Args[3], loPhi) || !sameSSAValue(instr.Args[4], hiPhi) {
				return match, false
			}
			if fusedSwap != nil {
				return match, false
			}
			fusedSwap = instr
		case OpTableArrayLoad:
			if len(instr.Args) != 3 || instr.Aux != int64(vm.FBKindInt) {
				return match, false
			}
			switch {
			case sameSSAValue(instr.Args[2], loPhi):
				if loLoad != nil {
					return match, false
				}
				loLoad = instr
			case sameSSAValue(instr.Args[2], hiPhi):
				if hiLoad != nil {
					return match, false
				}
				hiLoad = instr
			default:
				return match, false
			}
		case OpTableArrayStore:
			if len(instr.Args) < 5 || instr.Aux != int64(vm.FBKindInt) {
				return match, false
			}
			if sameSSAValue(instr.Args[3], loPhi) {
				loStore = instr
			} else if sameSSAValue(instr.Args[3], hiPhi) {
				hiStore = instr
			} else {
				return match, false
			}
		case OpAddInt, OpSubInt, OpGuardTableKind, OpJump, OpNop:
			continue
		default:
			return match, false
		}
	}
	if fusedSwap != nil {
		match.table = fusedSwap.Args[0]
		match.source = fusedSwap
		return match, true
	}
	if loLoad == nil || hiLoad == nil || loStore == nil || hiStore == nil {
		return match, false
	}
	if !sameSSAValue(loLoad.Args[0], hiLoad.Args[0]) || !sameSSAValue(loLoad.Args[1], hiLoad.Args[1]) {
		return match, false
	}
	if !sameSSAValue(loStore.Args[0], hiStore.Args[0]) || !sameSSAValue(loStore.Args[1], loLoad.Args[0]) ||
		!sameSSAValue(loStore.Args[2], loLoad.Args[1]) || !sameSSAValue(hiStore.Args[1], loLoad.Args[0]) ||
		!sameSSAValue(hiStore.Args[2], loLoad.Args[1]) {
		return match, false
	}
	if !sameSSAValue(loStore.Args[4], hiLoad.Value()) || !sameSSAValue(hiStore.Args[4], loLoad.Value()) {
		return match, false
	}
	match.table = loStore.Args[0]
	match.source = loStore
	return match, true
}

func reversePrefixLoopPhis(header, preheader, body *Block, a, b *Value) (*Value, *Value, *Value, bool) {
	if lo, hi, hiSeed, ok := reversePrefixLoopPhiOrder(header, preheader, body, a, b); ok {
		return lo, hi, hiSeed, true
	}
	return reversePrefixLoopPhiOrder(header, preheader, body, b, a)
}

func reversePrefixLoopPhiOrder(header, preheader, body *Block, loPhi, hiPhi *Value) (*Value, *Value, *Value, bool) {
	if loPhi == nil || hiPhi == nil || loPhi.Def == nil || hiPhi.Def == nil {
		return nil, nil, nil, false
	}
	loSeed := phiArgForPred(loPhi.Def, header, preheader)
	hiSeed := phiArgForPred(hiPhi.Def, header, preheader)
	loNext := phiArgForPred(loPhi.Def, header, body)
	hiNext := phiArgForPred(hiPhi.Def, header, body)
	if !isConstIntValue(loSeed, 1) || hiSeed == nil || !isAddOneOf(loNext, loPhi) || !isSubOneFrom(hiNext, hiPhi) {
		return nil, nil, nil, false
	}
	return loPhi, hiPhi, hiSeed, true
}

func isHeaderPhi(v *Value, header *Block) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpPhi && v.Def.Block == header
}

func isConstIntValue(v *Value, n int64) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpConstInt && v.Def.Aux == n
}

func constPlus(v *Value, delta int64) (int64, bool) {
	if v == nil || v.Def == nil || v.Def.Op != OpConstInt {
		return 0, false
	}
	return v.Def.Aux + delta, true
}

func isSubOneFrom(v, base *Value) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpSubInt && len(v.Def.Args) == 2 &&
		sameSSAValue(v.Def.Args[0], base) && isConstIntValue(v.Def.Args[1], 1)
}

func addPredIfMissing(block, pred *Block) {
	if block == nil || pred == nil {
		return
	}
	for _, p := range block.Preds {
		if p == pred {
			return
		}
	}
	block.Preds = append(block.Preds, pred)
}

func copyPhiArgsForNewPred(block, existingPred, newPred *Block) {
	if block == nil || existingPred == nil || newPred == nil || predIndex(block, newPred) >= 0 {
		return
	}
	oldIdx := predIndex(block, existingPred)
	if oldIdx < 0 {
		return
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpPhi {
			return
		}
		if oldIdx >= len(instr.Args) {
			return
		}
		instr.Args = append(instr.Args, instr.Args[oldIdx])
	}
}
