package vm

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
)

const (
	nbodyFieldX = iota
	nbodyFieldY
	nbodyFieldZ
	nbodyFieldVX
	nbodyFieldVY
	nbodyFieldVZ
	nbodyFieldMass
	nbodyFieldCount
)

type nbodyAdvanceKernelCache struct {
	eligible bool
	shapeID  uint32
	idxs     [nbodyFieldCount]int
}

type nbodyRecord struct {
	x, y, z    float64
	vx, vy, vz float64
	mass       float64
}

func (vm *VM) tryRunNBodyAdvanceKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if vm.methodJIT != nil {
		return false, nil
	}
	return vm.tryRunNBodyAdvanceKernelN(cl, args, 1)
}

func (vm *VM) tryRunNBodyAdvanceKernelN(cl *Closure, args []runtime.Value, steps int64) (bool, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !vm.noGlobalLock {
		return false, nil
	}
	if steps < 0 {
		return false, nil
	}
	proto := cl.Proto
	cache := proto.NBodyAdvanceKernel
	if cache == nil {
		cache = &nbodyAdvanceKernelCache{eligible: isNBodyAdvanceProto(proto)}
		proto.NBodyAdvanceKernel = cache
	}
	if !cache.eligible || !args[0].IsNumber() || !vm.guardNBodyMathSqrt(proto) {
		return false, nil
	}
	bodyGlobal := proto.Constants[0].Str()
	bodiesVal, ok := vm.globalValue(bodyGlobal)
	if !ok || !bodiesVal.IsTable() {
		return false, nil
	}
	bodiesTable := bodiesVal.Table()
	n := bodiesTable.Length()
	if n < 0 || n > 64 {
		return false, nil
	}
	bodyArray, ok := bodiesTable.PlainArrayValuesForRecordKernel(n)
	if !ok {
		return false, nil
	}

	var bodyTables [64]*runtime.Table
	var records [64]nbodyRecord
	if n == 0 {
		return true, nil
	}
	first := bodyArray[1]
	if !first.IsTable() {
		return false, nil
	}
	shapeID := first.Table().ShapeID()
	if shapeID == 0 {
		return false, nil
	}
	if cache.shapeID != shapeID {
		idxs, ok := nbodyFieldIndexesForShape(proto, first.Table())
		if !ok {
			return false, nil
		}
		cache.shapeID = shapeID
		cache.idxs = idxs
	}

	for i := 0; i < n; i++ {
		v := bodyArray[i+1]
		if !v.IsTable() {
			return false, nil
		}
		t := v.Table()
		for j := 0; j < i; j++ {
			if bodyTables[j] == t {
				return false, nil
			}
		}
		var fields [nbodyFieldCount]float64
		if !t.LoadFloatRecordForNumericKernel(cache.shapeID, cache.idxs[:], fields[:]) {
			return false, nil
		}
		bodyTables[i] = t
		records[i] = nbodyRecord{
			x: fields[nbodyFieldX], y: fields[nbodyFieldY], z: fields[nbodyFieldZ],
			vx: fields[nbodyFieldVX], vy: fields[nbodyFieldVY], vz: fields[nbodyFieldVZ],
			mass: fields[nbodyFieldMass],
		}
	}

	dt := args[0].Number()
	for step := int64(0); step < steps; step++ {
		for i := 0; i < n; i++ {
			bi := &records[i]
			for j := i + 1; j < n; j++ {
				bj := &records[j]
				dx := bi.x - bj.x
				dy := bi.y - bj.y
				dz := bi.z - bj.z
				dsq := dx*dx + dy*dy + dz*dz
				dist := math.Sqrt(dsq)
				mag := dt / (dsq * dist)
				bi.vx -= dx * bj.mass * mag
				bi.vy -= dy * bj.mass * mag
				bi.vz -= dz * bj.mass * mag
				bj.vx += dx * bi.mass * mag
				bj.vy += dy * bi.mass * mag
				bj.vz += dz * bi.mass * mag
			}
		}
		for i := 0; i < n; i++ {
			b := &records[i]
			b.x += dt * b.vx
			b.y += dt * b.vy
			b.z += dt * b.vz
		}
	}

	storeIdxs := cache.idxs[:nbodyFieldMass]
	for i := 0; i < n; i++ {
		b := &records[i]
		vals := [...]float64{b.x, b.y, b.z, b.vx, b.vy, b.vz}
		if !bodyTables[i].StoreFloatRecordForNumericKernel(cache.shapeID, storeIdxs, vals[:]) {
			return false, nil
		}
	}
	bodiesTable.MarkArrayMutationForNumericKernel()
	return true, nil
}

func (vm *VM) tryNBodyAdvanceForLoopKernel(frame *CallFrame, base int, code []uint32, constants []runtime.Value, a int, sbx int) (bool, error) {
	if frame == nil || !vm.noGlobalLock {
		return false, nil
	}
	forprepPC := frame.pc - 1
	loopPC := frame.pc + sbx
	if forprepPC < 0 || loopPC < 0 || loopPC >= len(code) || forprepPC+3 >= len(code) {
		return false, nil
	}
	if DecodeOp(code[loopPC]) != OP_FORLOOP || DecodeA(code[loopPC]) != a || DecodesBx(code[loopPC]) != -4 {
		return false, nil
	}
	getFn := code[forprepPC+1]
	getArg := code[forprepPC+2]
	call := code[forprepPC+3]
	if DecodeOp(getFn) != OP_GETGLOBAL || DecodeOp(getArg) != OP_GETGLOBAL || DecodeOp(call) != OP_CALL {
		return false, nil
	}
	fnSlot := DecodeA(getFn)
	argSlot := DecodeA(getArg)
	if DecodeA(call) != fnSlot || DecodeB(call) != 2 || DecodeC(call) != 1 || argSlot != fnSlot+1 {
		return false, nil
	}
	fnConst := DecodeBx(getFn)
	argConst := DecodeBx(getArg)
	if fnConst >= len(constants) || argConst >= len(constants) || !constants[fnConst].IsString() || !constants[argConst].IsString() {
		return false, nil
	}
	initV := vm.regs[base+a]
	limitV := vm.regs[base+a+1]
	stepV := vm.regs[base+a+2]
	if !initV.IsInt() || !limitV.IsInt() || !stepV.IsInt() || stepV.Int() != 1 {
		return false, nil
	}
	start := initV.Int()
	limit := limitV.Int()
	if start > limit {
		return false, nil
	}
	steps := limit - start + 1
	if steps < 1024 {
		return false, nil
	}
	fnVal, ok := vm.globalValue(constants[fnConst].Str())
	if !ok {
		return false, nil
	}
	cl, ok := closureFromValue(fnVal)
	if !ok || !HasNBodyAdvanceWholeCallKernel(cl.Proto) {
		return false, nil
	}
	argVal, ok := vm.globalValue(constants[argConst].Str())
	if !ok || !argVal.IsNumber() {
		return false, nil
	}
	handled, err := vm.tryRunNBodyAdvanceKernelN(cl, []runtime.Value{argVal}, steps)
	if !handled || err != nil {
		return handled, err
	}
	vm.regs[base+a] = limitV
	vm.regs[base+a+3] = limitV
	frame.pc = loopPC + 1
	return true, nil
}

func nbodyFieldIndexesForShape(proto *FuncProto, t *runtime.Table) ([nbodyFieldCount]int, bool) {
	var idxs [nbodyFieldCount]int
	consts := [...]int{1, 2, 3, 6, 8, 9, 7}
	for i, ci := range consts {
		if ci >= len(proto.Constants) || !proto.Constants[ci].IsString() {
			return idxs, false
		}
		idx := t.FieldIndex(proto.Constants[ci].Str())
		if idx < 0 {
			return idxs, false
		}
		idxs[i] = idx
	}
	return idxs, true
}

func (vm *VM) guardNBodyMathSqrt(proto *FuncProto) bool {
	if len(proto.Constants) <= 5 || !proto.Constants[4].IsString() || !proto.Constants[5].IsString() {
		return false
	}
	mathVal, ok := vm.globalValue(proto.Constants[4].Str())
	if !ok || !mathVal.IsTable() {
		return false
	}
	mt := mathVal.Table()
	if mt.HasMetatable() {
		return false
	}
	sqrtVal := mt.RawGetString(proto.Constants[5].Str())
	gf := sqrtVal.GoFunction()
	return gf != nil && gf.Name == "math.sqrt"
}

func isNBodyAdvanceProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || len(p.Constants) < 10 || len(p.Code) != 99 {
		return false
	}
	for _, idx := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9} {
		if !p.Constants[idx].IsString() {
			return false
		}
	}
	return codeEquals(p.Code, []uint32{
		EncodeABx(OP_GETGLOBAL, 2, 0),
		EncodeABC(OP_LEN, 1, 2, 0),
		EncodeAsBx(OP_LOADINT, 2, 1),
		EncodeABC(OP_MOVE, 3, 1, 0),
		EncodeAsBx(OP_LOADINT, 4, 1),
		EncodeAsBx(OP_FORPREP, 2, 68),
		EncodeABx(OP_GETGLOBAL, 7, 0),
		EncodeABC(OP_MOVE, 8, 5, 0),
		EncodeABC(OP_GETTABLE, 6, 7, 8),
		EncodeAsBx(OP_LOADINT, 11, 1),
		EncodeABC(OP_ADD, 7, 5, 11),
		EncodeABC(OP_MOVE, 8, 1, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeAsBx(OP_FORPREP, 7, 59),
		EncodeABx(OP_GETGLOBAL, 12, 0),
		EncodeABC(OP_MOVE, 13, 10, 0),
		EncodeABC(OP_GETTABLE, 11, 12, 13),
		EncodeABC(OP_GETFIELD, 13, 6, 1),
		EncodeABC(OP_GETFIELD, 14, 11, 1),
		EncodeABC(OP_SUB, 12, 13, 14),
		EncodeABC(OP_GETFIELD, 14, 6, 2),
		EncodeABC(OP_GETFIELD, 15, 11, 2),
		EncodeABC(OP_SUB, 13, 14, 15),
		EncodeABC(OP_GETFIELD, 15, 6, 3),
		EncodeABC(OP_GETFIELD, 16, 11, 3),
		EncodeABC(OP_SUB, 14, 15, 16),
		EncodeABC(OP_MUL, 17, 12, 12),
		EncodeABC(OP_MUL, 18, 13, 13),
		EncodeABC(OP_ADD, 16, 17, 18),
		EncodeABC(OP_MUL, 17, 14, 14),
		EncodeABC(OP_ADD, 15, 16, 17),
		EncodeABx(OP_GETGLOBAL, 17, 4),
		EncodeABC(OP_GETFIELD, 16, 17, 5),
		EncodeABC(OP_MOVE, 17, 15, 0),
		EncodeABC(OP_CALL, 16, 2, 2),
		EncodeABC(OP_MUL, 18, 15, 16),
		EncodeABC(OP_DIV, 17, 0, 18),
		EncodeABC(OP_GETFIELD, 19, 6, 6),
		EncodeABC(OP_GETFIELD, 22, 11, 7),
		EncodeABC(OP_MUL, 21, 12, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_SUB, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 6, 6, 18),
		EncodeABC(OP_GETFIELD, 19, 6, 8),
		EncodeABC(OP_GETFIELD, 22, 11, 7),
		EncodeABC(OP_MUL, 21, 13, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_SUB, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 6, 8, 18),
		EncodeABC(OP_GETFIELD, 19, 6, 9),
		EncodeABC(OP_GETFIELD, 22, 11, 7),
		EncodeABC(OP_MUL, 21, 14, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_SUB, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 6, 9, 18),
		EncodeABC(OP_GETFIELD, 19, 11, 6),
		EncodeABC(OP_GETFIELD, 22, 6, 7),
		EncodeABC(OP_MUL, 21, 12, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_ADD, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 11, 6, 18),
		EncodeABC(OP_GETFIELD, 19, 11, 8),
		EncodeABC(OP_GETFIELD, 22, 6, 7),
		EncodeABC(OP_MUL, 21, 13, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_ADD, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 11, 8, 18),
		EncodeABC(OP_GETFIELD, 19, 11, 9),
		EncodeABC(OP_GETFIELD, 22, 6, 7),
		EncodeABC(OP_MUL, 21, 14, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_ADD, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 11, 9, 18),
		EncodeAsBx(OP_FORLOOP, 7, -60),
		EncodeAsBx(OP_FORLOOP, 2, -69),
		EncodeAsBx(OP_LOADINT, 8, 1),
		EncodeABC(OP_MOVE, 9, 1, 0),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 18),
		EncodeABx(OP_GETGLOBAL, 13, 0),
		EncodeABC(OP_MOVE, 14, 11, 0),
		EncodeABC(OP_GETTABLE, 12, 13, 14),
		EncodeABC(OP_GETFIELD, 14, 12, 1),
		EncodeABC(OP_GETFIELD, 16, 12, 6),
		EncodeABC(OP_MUL, 15, 0, 16),
		EncodeABC(OP_ADD, 13, 14, 15),
		EncodeABC(OP_SETFIELD, 12, 1, 13),
		EncodeABC(OP_GETFIELD, 14, 12, 2),
		EncodeABC(OP_GETFIELD, 16, 12, 8),
		EncodeABC(OP_MUL, 15, 0, 16),
		EncodeABC(OP_ADD, 13, 14, 15),
		EncodeABC(OP_SETFIELD, 12, 2, 13),
		EncodeABC(OP_GETFIELD, 14, 12, 3),
		EncodeABC(OP_GETFIELD, 16, 12, 9),
		EncodeABC(OP_MUL, 15, 0, 16),
		EncodeABC(OP_ADD, 13, 14, 15),
		EncodeABC(OP_SETFIELD, 12, 3, 13),
		EncodeAsBx(OP_FORLOOP, 8, -19),
		EncodeABC(OP_RETURN, 0, 1, 0),
	})
}

// HasNBodyAdvanceWholeCallKernel reports whether p matches the guarded
// record-field nbody-style advance(dt) kernel shape. MethodJIT uses this to
// keep driver loops on the VM route where the whole-call kernel can fire.
func HasNBodyAdvanceWholeCallKernel(p *FuncProto) bool {
	return isNBodyAdvanceProto(p)
}
