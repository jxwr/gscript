package methodjit

import "fmt"

type tableFieldUpdatePair struct {
	pos int
	vel int
}

// TableFieldUpdateLoopPass recognizes a fixed-shape table-array loop whose
// body applies affine float updates to position-like fields from velocity-like
// fields. The match is structural: it uses lowered table-array and FieldSvals
// IR, not benchmark or function names.
func TableFieldUpdateLoopPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if lowerTableFieldUpdateLoop(fn, header) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func lowerTableFieldUpdateLoop(fn *Function, header *Block) bool {
	if fn == nil || header == nil || len(header.Preds) != 2 || len(header.Succs) != 2 {
		return false
	}
	var next, limit *Value
	var ok bool
	if indexPhi := secondPhi(header); indexPhi != nil {
		next, limit, ok = parseUnitBoundedLoopHeader(header, indexPhi)
	}
	if !ok {
		next, limit, ok = parseAnyUnitBoundedLoopHeader(header)
		if !ok {
			return false
		}
	}
	body := header.Succs[0]
	exit := header.Succs[1]
	if body == nil || exit == nil || !containsBlock(body.Succs, header) || !blockReturnsVoid(exit) {
		return false
	}
	spec, ok := parseTableFieldUpdateBody(body, next)
	if !ok {
		return false
	}
	pre := nonLoopPredecessor(header, body)
	if pre == nil {
		return false
	}
	op := &Instr{
		ID:    fn.newValueID(),
		Op:    OpTableFieldUpdateLoop,
		Type:  TypeUnknown,
		Args:  []*Value{spec.data, spec.len, limit, spec.scale, spec.damp},
		Aux:   int64(spec.shapeID),
		Aux2:  packTableFieldUpdatePairs(spec.pairs),
		Block: header,
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Block: header}
	header.Instrs = []*Instr{op, ret}
	header.Preds = []*Block{pre}
	header.Succs = nil
	functionRemarks(fn).Add("TableFieldUpdateLoop", "changed", header.ID, op.ID, op.Op,
		fmt.Sprintf("lowered fixed-shape table-array field update loop shape %d fields %v", spec.shapeID, spec.pairs))
	return true
}

type tableFieldUpdateSpec struct {
	data    *Value
	len     *Value
	scale   *Value
	damp    *Value
	shapeID uint32
	pairs   []tableFieldUpdatePair
}

func parseTableFieldUpdateBody(body *Block, index *Value) (tableFieldUpdateSpec, bool) {
	var spec tableFieldUpdateSpec
	if body == nil || index == nil {
		return spec, false
	}
	var load *Instr
	var svals *Instr
	for _, instr := range body.Instrs {
		if instr == nil {
			continue
		}
		if instr.Op == OpTableArrayLoad && len(instr.Args) >= 3 && sameSSAValue(instr.Args[2], index) && instr.Type == TypeTable {
			load = instr
		}
		if instr.Op == OpFieldSvals && len(instr.Args) == 1 && load != nil && sameSSAValue(instr.Args[0], load.Value()) && instr.Aux > 0 {
			svals = instr
		}
	}
	if load == nil || svals == nil || len(load.Args) < 3 {
		return spec, false
	}
	spec.data = load.Args[0]
	spec.len = load.Args[1]
	spec.shapeID = uint32(svals.Aux)

	fieldLoads := make(map[int]*Instr)
	for _, instr := range body.Instrs {
		if instr == nil || instr.Op != OpFieldLoad || instr.Type != TypeFloat || len(instr.Args) != 1 || !sameSSAValue(instr.Args[0], svals.Value()) {
			continue
		}
		fieldLoads[int(instr.Aux)] = instr
	}
	velToPair := make(map[int]tableFieldUpdatePair)
	for _, store := range body.Instrs {
		if store == nil || store.Op != OpFieldStore || len(store.Args) != 2 || !sameSSAValue(store.Args[0], svals.Value()) {
			continue
		}
		val := store.Args[1]
		if val == nil || val.Def == nil || val.Def.Op != OpFMA || len(val.Def.Args) != 3 {
			continue
		}
		posField := int(store.Aux)
		posLoad := fieldLoads[posField]
		if posLoad == nil {
			continue
		}
		velLoad, scale, ok := parseVelocityFMA(val.Def, posLoad.Value())
		if !ok || velLoad == nil || velLoad.Def == nil || velLoad.Def.Op != OpFieldLoad {
			continue
		}
		velField := int(velLoad.Def.Aux)
		if _, ok := fieldLoads[velField]; !ok {
			continue
		}
		if spec.scale == nil {
			spec.scale = scale
		} else if !sameSSAValue(spec.scale, scale) {
			return tableFieldUpdateSpec{}, false
		}
		velToPair[velLoad.ID] = tableFieldUpdatePair{pos: posField, vel: velField}
	}
	for _, store := range body.Instrs {
		if store == nil || store.Op != OpFieldStore || len(store.Args) != 2 || !sameSSAValue(store.Args[0], svals.Value()) {
			continue
		}
		val := store.Args[1]
		if val == nil || val.Def == nil || val.Def.Op != OpMulFloat || len(val.Def.Args) != 2 {
			continue
		}
		velLoad, damp, ok := parseVelocityMul(val.Def)
		if !ok {
			continue
		}
		pair, ok := velToPair[velLoad.ID]
		if !ok || int(store.Aux) != pair.vel {
			continue
		}
		if spec.damp == nil {
			spec.damp = damp
		} else if !sameSSAValue(spec.damp, damp) {
			return tableFieldUpdateSpec{}, false
		}
		spec.pairs = append(spec.pairs, pair)
	}
	if len(spec.pairs) < 2 || len(spec.pairs) > 3 || spec.scale == nil || spec.damp == nil {
		return tableFieldUpdateSpec{}, false
	}
	return spec, true
}

func parseVelocityFMA(fma *Instr, pos *Value) (*Value, *Value, bool) {
	if fma == nil || len(fma.Args) != 3 || !sameSSAValue(fma.Args[2], pos) {
		return nil, nil, false
	}
	for side := 0; side < 2; side++ {
		if fma.Args[side] != nil && fma.Args[side].Def != nil && fma.Args[side].Def.Op == OpFieldLoad {
			return fma.Args[side], fma.Args[1-side], true
		}
	}
	return nil, nil, false
}

func parseVelocityMul(mul *Instr) (*Value, *Value, bool) {
	if mul == nil || len(mul.Args) != 2 {
		return nil, nil, false
	}
	for side := 0; side < 2; side++ {
		if mul.Args[side] != nil && mul.Args[side].Def != nil && mul.Args[side].Def.Op == OpFieldLoad {
			return mul.Args[side], mul.Args[1-side], true
		}
	}
	return nil, nil, false
}

func secondPhi(block *Block) *Instr {
	_, second := firstTwoPhis(block)
	return second
}

func nonLoopPredecessor(header, back *Block) *Block {
	if header == nil {
		return nil
	}
	for _, pred := range header.Preds {
		if pred != nil && pred != back {
			return pred
		}
	}
	return nil
}

func blockReturnsVoid(block *Block) bool {
	if block == nil || len(block.Instrs) == 0 {
		return false
	}
	last := block.Instrs[len(block.Instrs)-1]
	return last != nil && last.Op == OpReturn && len(last.Args) == 0
}

func packTableFieldUpdatePairs(pairs []tableFieldUpdatePair) int64 {
	var packed uint64
	for i, pair := range pairs {
		shift := uint(i * 16)
		packed |= uint64(uint8(pair.pos)) << shift
		packed |= uint64(uint8(pair.vel)) << (shift + 8)
	}
	return int64(packed)
}

func unpackTableFieldUpdatePairs(aux2 int64) []tableFieldUpdatePair {
	packed := uint64(aux2)
	out := make([]tableFieldUpdatePair, 0, 3)
	for i := 0; i < 3; i++ {
		shift := uint(i * 16)
		pos := int(uint8(packed >> shift))
		vel := int(uint8(packed >> (shift + 8)))
		if pos == 0 && vel == 0 && i > 0 {
			break
		}
		out = append(out, tableFieldUpdatePair{pos: pos, vel: vel})
	}
	return out
}
