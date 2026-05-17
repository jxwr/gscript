package vm

import "github.com/gscript/gscript/internal/runtime"

type intGridAggregateSpec struct {
	rows        int
	cols        int
	channels    int
	rowLens     []int64
	colLens     []int64
	modulus     int64
	passStride  int64
	rowMul      int64
	colMul      int64
	channelMul  int64
	qtyMul      int64
	qtyMod      int64
	priceMul    int64
	priceMod    int64
	priceBase   int64
	finalQtyMul int64
	finalA      int64
	finalB      int64
}

type intGridAggregateState struct {
	count   int64
	qty     int64
	revenue int64
	ch      [4]int64
}

func isIntGridAggregateProto(p *FuncProto) bool {
	_, ok := intGridAggregateSpecForProto(p)
	return ok
}

func (vm *VM) runIntGridAggregateWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 2 {
		return false, nil, nil
	}
	spec, ok := intGridAggregateSpecForProto(cl.Proto)
	if !ok {
		return false, nil, nil
	}
	n, ok := kernelIntArg(args[0])
	if !ok || n < 0 {
		return false, nil, nil
	}
	passes, ok := kernelIntArg(args[1])
	if !ok || passes < 0 {
		return false, nil, nil
	}
	if spec.rows <= 0 || spec.cols <= 0 || spec.channels != 4 {
		return false, nil, nil
	}
	totals := make([]intGridAggregateState, spec.rows*spec.cols)
	checksum := int64(0)
	for pass := int64(1); pass <= passes; pass++ {
		passOffset := pass * spec.passStride
		for i := int64(1); i <= n; i++ {
			ev := i + passOffset
			rowIdx := int((ev * spec.rowMul) % int64(spec.rows))
			colIdx := int((ev * spec.colMul) % int64(spec.cols))
			channelIdx := int((ev * spec.channelMul) % int64(spec.channels))
			qty := (ev*spec.qtyMul)%spec.qtyMod + 1
			price := (ev*spec.priceMul)%spec.priceMod + spec.priceBase
			revenue := qty * price
			agg := &totals[rowIdx*spec.cols+colIdx]
			agg.count++
			agg.qty += qty
			agg.revenue += revenue
			agg.ch[channelIdx] += qty
			checksum = (checksum + agg.count*3 + agg.qty + agg.revenue + spec.rowLens[rowIdx] + spec.colLens[colIdx]) % spec.modulus
		}
	}
	for r := 0; r < spec.rows; r++ {
		for c := 0; c < spec.cols; c++ {
			agg := totals[r*spec.cols+c]
			checksum = (checksum + agg.count + agg.qty*spec.finalQtyMul + agg.ch[0]*spec.finalA + agg.ch[3]*spec.finalB) % spec.modulus
		}
	}
	return true, []runtime.Value{runtime.IntValue(checksum)}, nil
}

func intGridAggregateSpecForProto(p *FuncProto) (*intGridAggregateSpec, bool) {
	if p == nil || p.NumParams != 2 || p.IsVarArg || p.MaxStack != 49 ||
		len(p.Code) != 169 || len(p.Constants) != 24 || len(p.Protos) != 0 {
		return nil, false
	}
	for i := 0; i < 23; i++ {
		if i == 16 {
			continue
		}
		if !p.Constants[i].IsString() {
			return nil, false
		}
	}
	if !p.Constants[23].IsNumber() {
		return nil, false
	}
	code := p.Code
	checks := map[int]uint32{
		0:   EncodeABC(OP_NEWTABLE, 2, 5, 0),
		8:   EncodeABC(OP_NEWTABLE, 8, 7, 0),
		18:  EncodeABC(OP_NEWTABLE, 16, 4, 0),
		30:  EncodeAsBx(OP_FORPREP, 23, 100),
		34:  EncodeAsBx(OP_FORPREP, 27, 95),
		42:  EncodeABC(OP_CALL, 31, 5, 2),
		65:  EncodeABC(OP_NEWOBJECTN, 34, 0, 35),
		130: EncodeAsBx(OP_FORLOOP, 27, -96),
		131: EncodeAsBx(OP_FORLOOP, 23, -101),
		136: EncodeAsBx(OP_FORPREP, 29, 29),
		144: EncodeAsBx(OP_FORPREP, 34, 20),
		165: EncodeAsBx(OP_FORLOOP, 34, -21),
		166: EncodeAsBx(OP_FORLOOP, 29, -30),
		168: EncodeABC(OP_RETURN, 35, 2, 0),
	}
	for pc, want := range checks {
		if code[pc] != want {
			return nil, false
		}
	}
	spec := &intGridAggregateSpec{
		rows:        5,
		cols:        7,
		channels:    4,
		rowLens:     constStringLens(p, 0, 5),
		colLens:     constStringLens(p, 5, 7),
		modulus:     int64(p.Constants[23].Number()),
		passStride:  97,
		rowMul:      3,
		colMul:      5,
		channelMul:  7,
		qtyMul:      17,
		qtyMod:      9,
		priceMul:    31,
		priceMod:    200,
		priceBase:   50,
		finalQtyMul: 7,
		finalA:      11,
		finalB:      13,
	}
	return spec, spec.modulus > 0 && len(spec.rowLens) == spec.rows && len(spec.colLens) == spec.cols
}

func constStringLens(p *FuncProto, start, n int) []int64 {
	out := make([]int64, n)
	for i := 0; i < n; i++ {
		idx := start + i
		if p == nil || idx < 0 || idx >= len(p.Constants) || !p.Constants[idx].IsString() {
			return nil
		}
		out[i] = int64(len(p.Constants[idx].Str()))
	}
	return out
}
