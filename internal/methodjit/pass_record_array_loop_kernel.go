package methodjit

import "fmt"

type tableFieldUpdatePair struct {
	pos int
	vel int
}

// RecordArrayLoopKernelPass recognizes fixed-shape table-array loops whose
// bodies are expressible as float field loads, scalar operands, a small DAG,
// and field stores. The generated op is parameterized by a runtime-built
// RecordArrayLoopKernelSpec rather than a benchmark-specific static opcode.
func RecordArrayLoopKernelPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if lowerRecordArrayLoopKernel(fn, header) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func lowerRecordArrayLoopKernel(fn *Function, header *Block) bool {
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
	spec, ok := parseRecordFieldUpdateBody(body, next)
	if !ok {
		return false
	}
	pre := nonLoopPredecessor(header, body)
	if pre == nil {
		return false
	}
	op := &Instr{
		ID:    fn.newValueID(),
		Op:    OpRecordArrayLoopKernel,
		Type:  TypeUnknown,
		Args:  []*Value{spec.data, spec.len, limit, spec.scale, spec.damp},
		Block: header,
	}
	if fn.RecordArrayLoopKernels == nil {
		fn.RecordArrayLoopKernels = make(map[int]RecordArrayLoopKernelSpec)
	}
	fn.RecordArrayLoopKernels[op.ID] = spec.kernel
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Block: header}
	header.Instrs = []*Instr{op, ret}
	header.Preds = []*Block{pre}
	header.Succs = nil
	functionRemarks(fn).Add("RecordArrayLoopKernel", "changed", header.ID, op.ID, op.Op,
		fmt.Sprintf("lowered fixed-shape record-array loop shape %d fields %v ops=%d stores=%d",
			spec.kernel.ShapeID, spec.kernel.FieldLoads, len(spec.kernel.Ops), len(spec.kernel.Stores)))
	return true
}

type recordFieldUpdateSpec struct {
	data   *Value
	len    *Value
	scale  *Value
	damp   *Value
	pairs  []tableFieldUpdatePair
	kernel RecordArrayLoopKernelSpec
}

func parseRecordFieldUpdateBody(body *Block, index *Value) (recordFieldUpdateSpec, bool) {
	var spec recordFieldUpdateSpec
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
			return recordFieldUpdateSpec{}, false
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
			return recordFieldUpdateSpec{}, false
		}
		spec.pairs = append(spec.pairs, pair)
	}
	if len(spec.pairs) < 2 || spec.scale == nil || spec.damp == nil {
		return recordFieldUpdateSpec{}, false
	}
	spec.kernel = buildRecordArrayKernelFromFieldPairs(uint32(svals.Aux), spec.pairs)
	return spec, true
}

func buildRecordArrayKernelFromFieldPairs(shapeID uint32, pairs []tableFieldUpdatePair) RecordArrayLoopKernelSpec {
	spec := RecordArrayLoopKernelSpec{ShapeID: shapeID, ScalarCount: 2}
	fieldIndex := make(map[int]int)
	addField := func(field int) int {
		if idx, ok := fieldIndex[field]; ok {
			return idx
		}
		idx := len(spec.FieldLoads)
		fieldIndex[field] = idx
		spec.FieldLoads = append(spec.FieldLoads, field)
		if field > spec.MaxField {
			spec.MaxField = field
		}
		return idx
	}
	fieldSrc := func(field int) RecordArrayKernelSource {
		return RecordArrayKernelSource{Kind: RecordArrayKernelSourceField, Index: addField(field)}
	}
	scalarSrc := func(index int) RecordArrayKernelSource {
		return RecordArrayKernelSource{Kind: RecordArrayKernelSourceScalar, Index: index}
	}
	opSrc := func(index int) RecordArrayKernelSource {
		return RecordArrayKernelSource{Kind: RecordArrayKernelSourceOp, Index: index}
	}
	for _, pair := range pairs {
		pos := fieldSrc(pair.pos)
		vel := fieldSrc(pair.vel)
		fmaIdx := len(spec.Ops)
		spec.Ops = append(spec.Ops, RecordArrayKernelFloatOp{Kind: RecordArrayKernelFloatOpFMA, A: vel, B: scalarSrc(0), C: pos})
		mulIdx := len(spec.Ops)
		spec.Ops = append(spec.Ops, RecordArrayKernelFloatOp{Kind: RecordArrayKernelFloatOpMul, A: vel, B: scalarSrc(1)})
		spec.Stores = append(spec.Stores,
			RecordArrayKernelStore{Field: pair.pos, Value: opSrc(fmaIdx)},
			RecordArrayKernelStore{Field: pair.vel, Value: opSrc(mulIdx)},
		)
	}
	return spec
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
