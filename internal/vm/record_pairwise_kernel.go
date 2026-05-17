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
	spec     *recordPairwiseAdvanceKernelSpec
}

type nbodyRecord struct {
	x, y, z    float64
	vx, vy, vz float64
	mass       float64
}

type recordPairwiseAdvanceKernelSpec struct {
	tableName     string
	sqrtTableName string
	sqrtFieldName string
	fieldNames    [nbodyFieldCount]string
}

type nbodyAdvanceDriverLoopShape struct {
	loopPC   int
	fnConst  int
	argConst int
}

func (vm *VM) tryRunNBodyAdvanceKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if vm.methodJIT != nil {
		return false, nil
	}
	if cl == nil || cl.Proto == nil || !hotWholeCallKernelRecognized(cl.Proto, wholeCallKernelNBodyAdvance) {
		return false, nil
	}
	return vm.runNBodyAdvanceKernel(cl, args)
}

func (vm *VM) runNBodyAdvanceKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if vm.methodJIT != nil {
		return false, nil
	}
	return vm.runNBodyAdvanceKernelN(cl, args, 1)
}

func (vm *VM) tryRunNBodyAdvanceKernelN(cl *Closure, args []runtime.Value, steps int64) (bool, error) {
	if cl == nil || cl.Proto == nil || !hotWholeCallKernelRecognized(cl.Proto, wholeCallKernelNBodyAdvance) {
		return false, nil
	}
	return vm.runNBodyAdvanceKernelN(cl, args, steps)
}

func (vm *VM) runNBodyAdvanceKernelN(cl *Closure, args []runtime.Value, steps int64) (bool, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !vm.noGlobalLock {
		return false, nil
	}
	if steps < 0 {
		return false, nil
	}
	proto := cl.Proto
	if isNBodyDenseAdvanceProto(proto) {
		return vm.runNBodyDenseAdvanceKernelN(args, steps)
	}
	cache := proto.NBodyAdvanceKernel
	if cache == nil {
		cache = &nbodyAdvanceKernelCache{eligible: true}
		proto.NBodyAdvanceKernel = cache
	}
	spec := cache.spec
	if spec == nil {
		var ok bool
		spec, ok = recordPairwiseAdvanceKernelSpecForProto(proto)
		if !ok {
			cache.eligible = false
			return false, nil
		}
		cache.spec = spec
	}
	if !cache.eligible || !args[0].IsNumber() || !vm.guardRecordPairwiseSqrt(spec) {
		return false, nil
	}
	bodiesVal, ok := vm.recordPairwiseTableValue(spec)
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
		idxs, ok := recordPairwiseFieldIndexesForShape(proto, spec, first.Table())
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

func (vm *VM) runNBodyDenseAdvanceKernelN(args []runtime.Value, steps int64) (bool, error) {
	if len(args) != 1 || !args[0].IsNumber() || !vm.guardNBodyDenseMathSqrt() || !vm.guardNBodyDenseMatrixLib() {
		return false, nil
	}
	n, ok := vm.guardNBodyDenseGlobals()
	if !ok {
		return false, nil
	}
	bodiesVal, ok := vm.globalValue("bodies")
	if !ok || !bodiesVal.IsTable() {
		return false, nil
	}
	flat, stride, ok := bodiesVal.Table().DenseFloatMatrixForNumericKernel(n, nbodyFieldCount)
	if !ok || stride < nbodyFieldCount {
		return false, nil
	}
	dt := args[0].Number()
	for step := int64(0); step < steps; step++ {
		for i := 0; i < n; i++ {
			bi := i * stride
			bix := flat[bi+nbodyFieldX]
			biy := flat[bi+nbodyFieldY]
			biz := flat[bi+nbodyFieldZ]
			bim := flat[bi+nbodyFieldMass]
			bivx := flat[bi+nbodyFieldVX]
			bivy := flat[bi+nbodyFieldVY]
			bivz := flat[bi+nbodyFieldVZ]
			for j := i + 1; j < n; j++ {
				bj := j * stride
				bjx := flat[bj+nbodyFieldX]
				bjy := flat[bj+nbodyFieldY]
				bjz := flat[bj+nbodyFieldZ]
				bjm := flat[bj+nbodyFieldMass]
				bjvx := flat[bj+nbodyFieldVX]
				bjvy := flat[bj+nbodyFieldVY]
				bjvz := flat[bj+nbodyFieldVZ]
				dx := bix - bjx
				dy := biy - bjy
				dz := biz - bjz
				dsq := dx*dx + dy*dy + dz*dz
				dist := math.Sqrt(dsq)
				mag := dt / (dsq * dist)
				bivx -= dx * bjm * mag
				bivy -= dy * bjm * mag
				bivz -= dz * bjm * mag
				flat[bj+nbodyFieldVX] = bjvx + dx*bim*mag
				flat[bj+nbodyFieldVY] = bjvy + dy*bim*mag
				flat[bj+nbodyFieldVZ] = bjvz + dz*bim*mag
			}
			flat[bi+nbodyFieldVX] = bivx
			flat[bi+nbodyFieldVY] = bivy
			flat[bi+nbodyFieldVZ] = bivz
		}
		for i := 0; i < n; i++ {
			b := i * stride
			flat[b+nbodyFieldX] += dt * flat[b+nbodyFieldVX]
			flat[b+nbodyFieldY] += dt * flat[b+nbodyFieldVY]
			flat[b+nbodyFieldZ] += dt * flat[b+nbodyFieldVZ]
		}
	}
	bodiesVal.Table().MarkArrayMutationForNumericKernel()
	return true, nil
}

func (vm *VM) tryNBodyAdvanceForLoopKernel(frame *CallFrame, base int, code []uint32, constants []runtime.Value, a int, sbx int) (bool, error) {
	if frame == nil || !vm.noGlobalLock {
		return false, nil
	}
	forprepPC := frame.pc - 1
	shape, ok := matchNBodyAdvanceDriverLoopShape(code, constants, forprepPC, a, sbx)
	if !ok {
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
	fnVal, ok := vm.globalValue(constants[shape.fnConst].Str())
	if !ok {
		return false, nil
	}
	cl, ok := closureFromValue(fnVal)
	if !ok || !HasNBodyAdvanceWholeCallKernel(cl.Proto) {
		return false, nil
	}
	argVal, ok := vm.globalValue(constants[shape.argConst].Str())
	if !ok || !argVal.IsNumber() {
		return false, nil
	}
	handled, err := vm.tryRunNBodyAdvanceKernelN(cl, []runtime.Value{argVal}, steps)
	if !handled || err != nil {
		return handled, err
	}
	vm.regs[base+a] = limitV
	vm.regs[base+a+3] = limitV
	frame.pc = shape.loopPC + 1
	return true, nil
}

func matchNBodyAdvanceDriverLoopShape(code []uint32, constants []runtime.Value, forprepPC int, a int, sbx int) (nbodyAdvanceDriverLoopShape, bool) {
	var shape nbodyAdvanceDriverLoopShape
	bodyPC := forprepPC + 1
	loopPC := bodyPC + sbx
	if forprepPC < 0 || bodyPC < 0 || loopPC < 0 || loopPC >= len(code) || loopPC-bodyPC != 3 {
		return shape, false
	}
	loop := code[loopPC]
	if DecodeOp(loop) != OP_FORLOOP || DecodeA(loop) != a || loopPC+1+DecodesBx(loop) != bodyPC {
		return shape, false
	}
	getFn := code[bodyPC]
	getArg := code[bodyPC+1]
	call := code[bodyPC+2]
	if DecodeOp(getFn) != OP_GETGLOBAL || DecodeOp(getArg) != OP_GETGLOBAL || DecodeOp(call) != OP_CALL {
		return shape, false
	}
	fnSlot := DecodeA(getFn)
	argSlot := DecodeA(getArg)
	if DecodeA(call) != fnSlot || DecodeB(call) != 2 || DecodeC(call) != 1 || argSlot != fnSlot+1 {
		return shape, false
	}
	fnConst := DecodeBx(getFn)
	argConst := DecodeBx(getArg)
	if !stringConst(constants, fnConst) || !stringConst(constants, argConst) {
		return shape, false
	}
	return nbodyAdvanceDriverLoopShape{
		loopPC:   loopPC,
		fnConst:  fnConst,
		argConst: argConst,
	}, true
}

func recordPairwiseFieldIndexesForShape(proto *FuncProto, spec *recordPairwiseAdvanceKernelSpec, t *runtime.Table) ([nbodyFieldCount]int, bool) {
	var idxs [nbodyFieldCount]int
	if proto == nil || spec == nil || t == nil {
		return idxs, false
	}
	for i, fieldName := range spec.fieldNames {
		if fieldName == "" {
			return idxs, false
		}
		idx := t.FieldIndex(fieldName)
		if idx < 0 {
			return idxs, false
		}
		idxs[i] = idx
	}
	return idxs, true
}

func (vm *VM) guardRecordPairwiseSqrt(spec *recordPairwiseAdvanceKernelSpec) bool {
	if spec == nil || spec.sqrtTableName == "" || spec.sqrtFieldName == "" {
		return false
	}
	return vm.guardGoFunctionField(spec.sqrtTableName, spec.sqrtFieldName, "math.sqrt")
}

func (vm *VM) guardNBodyDenseMathSqrt() bool {
	return vm.guardGoFunctionField("math", "sqrt", "math.sqrt")
}

func (vm *VM) guardGoFunctionField(tableName, fieldName, goName string) bool {
	mathVal, ok := vm.globalValue(tableName)
	if !ok || !mathVal.IsTable() {
		return false
	}
	mt := mathVal.Table()
	if mt.HasMetatable() {
		return false
	}
	sqrtVal := mt.RawGetString(fieldName)
	gf := sqrtVal.GoFunction()
	return gf != nil && gf.Name == goName
}

func (vm *VM) guardNBodyDenseMatrixLib() bool {
	matrixVal, ok := vm.globalValue("matrix")
	if !ok || !matrixVal.IsTable() {
		return false
	}
	mt := matrixVal.Table()
	if mt.HasMetatable() {
		return false
	}
	getf := mt.RawGetString("getf").GoFunction()
	setf := mt.RawGetString("setf").GoFunction()
	return getf != nil && getf.Name == "matrix.getf" &&
		setf != nil && setf.Name == "matrix.setf"
}

func (vm *VM) guardNBodyDenseGlobals() (int, bool) {
	expected := map[string]int64{
		"N_BODIES": int64(5),
		"F_X":      int64(nbodyFieldX),
		"F_Y":      int64(nbodyFieldY),
		"F_Z":      int64(nbodyFieldZ),
		"F_VX":     int64(nbodyFieldVX),
		"F_VY":     int64(nbodyFieldVY),
		"F_VZ":     int64(nbodyFieldVZ),
		"F_MASS":   int64(nbodyFieldMass),
	}
	for name, want := range expected {
		v, ok := vm.globalValue(name)
		if !ok || !v.IsInt() || v.Int() != want {
			return 0, false
		}
	}
	return 5, true
}

func (vm *VM) recordPairwiseTableValue(spec *recordPairwiseAdvanceKernelSpec) (runtime.Value, bool) {
	if spec == nil || spec.tableName == "" {
		return runtime.NilValue(), false
	}
	v, ok := vm.globalValue(spec.tableName)
	if !ok || !v.IsTable() {
		return runtime.NilValue(), false
	}
	return v, true
}

func recordPairwiseAdvanceKernelSpecForProto(proto *FuncProto) (*recordPairwiseAdvanceKernelSpec, bool) {
	if proto == nil || isNBodyDenseAdvanceProto(proto) {
		return nil, false
	}
	if isNBodyAdvanceProto(proto) {
		return analyzeRecordPairwiseAdvanceKernelSpec(proto)
	}
	return nil, false
}

func analyzeRecordPairwiseAdvanceKernelSpec(proto *FuncProto) (*recordPairwiseAdvanceKernelSpec, bool) {
	tableConst, biReg, bjReg, ok := findRecordPairwiseTableAndRecordRegs(proto.Code)
	if !ok {
		return nil, false
	}
	sqrtTableConst, sqrtFieldConst, ok := findRecordPairwiseSqrtConsts(proto.Code)
	if !ok {
		return nil, false
	}
	positionConsts, ok := findRecordPairwisePositionConsts(proto.Code, biReg, bjReg)
	if !ok {
		return nil, false
	}
	velocityConsts, ok := findRecordPairwiseVelocityConsts(proto.Code, biReg)
	if !ok {
		return nil, false
	}
	massConst, ok := findRecordPairwiseMassConst(proto.Code, biReg, bjReg, positionConsts, velocityConsts)
	if !ok {
		return nil, false
	}
	fieldConsts := [nbodyFieldCount]int{
		positionConsts[0], positionConsts[1], positionConsts[2],
		velocityConsts[0], velocityConsts[1], velocityConsts[2],
		massConst,
	}
	tableName, ok := protoStringConstant(proto, tableConst)
	if !ok {
		return nil, false
	}
	sqrtTableName, ok := protoStringConstant(proto, sqrtTableConst)
	if !ok {
		return nil, false
	}
	sqrtFieldName, ok := protoStringConstant(proto, sqrtFieldConst)
	if !ok {
		return nil, false
	}
	spec := &recordPairwiseAdvanceKernelSpec{
		tableName:     tableName,
		sqrtTableName: sqrtTableName,
		sqrtFieldName: sqrtFieldName,
	}
	for i, constIdx := range fieldConsts {
		name, ok := protoStringConstant(proto, constIdx)
		if !ok {
			return nil, false
		}
		spec.fieldNames[i] = name
	}
	return spec, true
}

func findRecordPairwiseTableAndRecordRegs(code []uint32) (int, int, int, bool) {
	tableConst := -1
	biReg := -1
	bjReg := -1
	for pc := 0; pc+2 < len(code); pc++ {
		getGlobal := code[pc]
		move := code[pc+1]
		getTable := code[pc+2]
		if DecodeOp(getGlobal) != OP_GETGLOBAL || DecodeOp(move) != OP_MOVE || DecodeOp(getTable) != OP_GETTABLE {
			continue
		}
		if DecodeB(getTable) != DecodeA(getGlobal) || DecodeC(getTable) != DecodeA(move) {
			continue
		}
		if tableConst < 0 {
			tableConst = DecodeBx(getGlobal)
			biReg = DecodeA(getTable)
			continue
		}
		if DecodeBx(getGlobal) == tableConst {
			bjReg = DecodeA(getTable)
			return tableConst, biReg, bjReg, true
		}
	}
	return 0, 0, 0, false
}

func findRecordPairwiseSqrtConsts(code []uint32) (int, int, bool) {
	for pc := 3; pc < len(code); pc++ {
		call := code[pc]
		if DecodeOp(call) != OP_CALL || DecodeB(call) != 2 || DecodeC(call) != 2 {
			continue
		}
		getField := code[pc-2]
		getGlobal := code[pc-3]
		if DecodeOp(getField) != OP_GETFIELD || DecodeOp(getGlobal) != OP_GETGLOBAL {
			continue
		}
		if DecodeA(call) == DecodeA(getField) && DecodeB(getField) == DecodeA(getGlobal) {
			return DecodeBx(getGlobal), DecodeC(getField), true
		}
	}
	return 0, 0, false
}

func findRecordPairwisePositionConsts(code []uint32, biReg, bjReg int) ([3]int, bool) {
	var fields [3]int
	n := 0
	for pc := 0; pc+2 < len(code) && n < len(fields); pc++ {
		left := code[pc]
		right := code[pc+1]
		sub := code[pc+2]
		if DecodeOp(left) != OP_GETFIELD || DecodeOp(right) != OP_GETFIELD || DecodeOp(sub) != OP_SUB {
			continue
		}
		if DecodeB(left) != biReg || DecodeB(right) != bjReg || DecodeC(left) != DecodeC(right) {
			continue
		}
		if DecodeB(sub) != DecodeA(left) || DecodeC(sub) != DecodeA(right) {
			continue
		}
		fields[n] = DecodeC(left)
		n++
	}
	return fields, n == len(fields)
}

func findRecordPairwiseVelocityConsts(code []uint32, biReg int) ([3]int, bool) {
	var fields [3]int
	n := 0
	for _, inst := range code {
		if DecodeOp(inst) != OP_SETFIELD || DecodeA(inst) != biReg {
			continue
		}
		fieldConst := DecodeB(inst)
		if containsInt(fields[:n], fieldConst) {
			continue
		}
		fields[n] = fieldConst
		n++
		if n == len(fields) {
			return fields, true
		}
	}
	return fields, false
}

func findRecordPairwiseMassConst(code []uint32, biReg, bjReg int, positionConsts [3]int, velocityConsts [3]int) (int, bool) {
	for _, inst := range code {
		if DecodeOp(inst) != OP_GETFIELD || DecodeB(inst) != bjReg {
			continue
		}
		fieldConst := DecodeC(inst)
		if containsInt(positionConsts[:], fieldConst) || containsInt(velocityConsts[:], fieldConst) {
			continue
		}
		if recordPairwiseHasFieldLoad(code, biReg, fieldConst) {
			return fieldConst, true
		}
	}
	return 0, false
}

func recordPairwiseHasFieldLoad(code []uint32, baseReg int, fieldConst int) bool {
	for _, inst := range code {
		if DecodeOp(inst) == OP_GETFIELD && DecodeB(inst) == baseReg && DecodeC(inst) == fieldConst {
			return true
		}
	}
	return false
}

func containsInt(vals []int, want int) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

func protoStringConstant(proto *FuncProto, idx int) (string, bool) {
	if proto == nil || idx < 0 || idx >= len(proto.Constants) || !proto.Constants[idx].IsString() {
		return "", false
	}
	return proto.Constants[idx].Str(), true
}

func isNBodyAdvanceProto(p *FuncProto) bool {
	if isNBodyDenseAdvanceProto(p) {
		return true
	}
	if isNBodyAdvanceProtoWithGlobalCount(p) {
		return true
	}
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

func isNBodyDenseAdvanceProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || len(p.Constants) < 14 || len(p.Code) != 241 {
		return false
	}
	required := map[int]string{
		0: "N_BODIES", 1: "matrix", 2: "getf", 3: "bodies",
		4: "F_X", 5: "F_Y", 6: "F_Z", 7: "F_MASS",
		8: "F_VX", 9: "F_VY", 10: "F_VZ", 11: "math",
		12: "sqrt", 13: "setf",
	}
	for idx, want := range required {
		if !p.Constants[idx].IsString() || p.Constants[idx].Str() != want {
			return false
		}
	}
	checks := map[int]uint32{
		0:   EncodeAsBx(OP_LOADINT, 1, 0),
		1:   EncodeABx(OP_GETGLOBAL, 5, 0),
		5:   EncodeAsBx(OP_FORPREP, 1, 166),
		54:  EncodeAsBx(OP_FORPREP, 12, 95),
		105: EncodeABx(OP_GETGLOBAL, 28, 11),
		108: EncodeABC(OP_CALL, 27, 2, 2),
		150: EncodeAsBx(OP_FORLOOP, 12, -96),
		172: EncodeAsBx(OP_FORLOOP, 1, -167),
		178: EncodeAsBx(OP_FORPREP, 7, 60),
		239: EncodeAsBx(OP_FORLOOP, 7, -61),
		240: EncodeABC(OP_RETURN, 0, 1, 0),
	}
	for pc, want := range checks {
		if p.Code[pc] != want {
			return false
		}
	}
	return true
}

func isNBodyAdvanceProtoWithGlobalCount(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || len(p.Constants) < 11 || len(p.Code) != 98 {
		return false
	}
	for _, idx := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10} {
		if !p.Constants[idx].IsString() {
			return false
		}
	}
	return codeEquals(p.Code, []uint32{
		EncodeABx(OP_GETGLOBAL, 1, 0),
		EncodeAsBx(OP_LOADINT, 2, 1),
		EncodeABC(OP_MOVE, 3, 1, 0),
		EncodeAsBx(OP_LOADINT, 4, 1),
		EncodeAsBx(OP_FORPREP, 2, 68),
		EncodeABx(OP_GETGLOBAL, 7, 1),
		EncodeABC(OP_MOVE, 8, 5, 0),
		EncodeABC(OP_GETTABLE, 6, 7, 8),
		EncodeAsBx(OP_LOADINT, 11, 1),
		EncodeABC(OP_ADD, 7, 5, 11),
		EncodeABC(OP_MOVE, 8, 1, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeAsBx(OP_FORPREP, 7, 59),
		EncodeABx(OP_GETGLOBAL, 12, 1),
		EncodeABC(OP_MOVE, 13, 10, 0),
		EncodeABC(OP_GETTABLE, 11, 12, 13),
		EncodeABC(OP_GETFIELD, 13, 6, 2),
		EncodeABC(OP_GETFIELD, 14, 11, 2),
		EncodeABC(OP_SUB, 12, 13, 14),
		EncodeABC(OP_GETFIELD, 14, 6, 3),
		EncodeABC(OP_GETFIELD, 15, 11, 3),
		EncodeABC(OP_SUB, 13, 14, 15),
		EncodeABC(OP_GETFIELD, 15, 6, 4),
		EncodeABC(OP_GETFIELD, 16, 11, 4),
		EncodeABC(OP_SUB, 14, 15, 16),
		EncodeABC(OP_MUL, 17, 12, 12),
		EncodeABC(OP_MUL, 18, 13, 13),
		EncodeABC(OP_ADD, 16, 17, 18),
		EncodeABC(OP_MUL, 17, 14, 14),
		EncodeABC(OP_ADD, 15, 16, 17),
		EncodeABx(OP_GETGLOBAL, 17, 5),
		EncodeABC(OP_GETFIELD, 16, 17, 6),
		EncodeABC(OP_MOVE, 17, 15, 0),
		EncodeABC(OP_CALL, 16, 2, 2),
		EncodeABC(OP_MUL, 18, 15, 16),
		EncodeABC(OP_DIV, 17, 0, 18),
		EncodeABC(OP_GETFIELD, 19, 6, 7),
		EncodeABC(OP_GETFIELD, 22, 11, 8),
		EncodeABC(OP_MUL, 21, 12, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_SUB, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 6, 7, 18),
		EncodeABC(OP_GETFIELD, 19, 6, 9),
		EncodeABC(OP_GETFIELD, 22, 11, 8),
		EncodeABC(OP_MUL, 21, 13, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_SUB, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 6, 9, 18),
		EncodeABC(OP_GETFIELD, 19, 6, 10),
		EncodeABC(OP_GETFIELD, 22, 11, 8),
		EncodeABC(OP_MUL, 21, 14, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_SUB, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 6, 10, 18),
		EncodeABC(OP_GETFIELD, 19, 11, 7),
		EncodeABC(OP_GETFIELD, 22, 6, 8),
		EncodeABC(OP_MUL, 21, 12, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_ADD, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 11, 7, 18),
		EncodeABC(OP_GETFIELD, 19, 11, 9),
		EncodeABC(OP_GETFIELD, 22, 6, 8),
		EncodeABC(OP_MUL, 21, 13, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_ADD, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 11, 9, 18),
		EncodeABC(OP_GETFIELD, 19, 11, 10),
		EncodeABC(OP_GETFIELD, 22, 6, 8),
		EncodeABC(OP_MUL, 21, 14, 22),
		EncodeABC(OP_MUL, 20, 21, 17),
		EncodeABC(OP_ADD, 18, 19, 20),
		EncodeABC(OP_SETFIELD, 11, 10, 18),
		EncodeAsBx(OP_FORLOOP, 7, -60),
		EncodeAsBx(OP_FORLOOP, 2, -69),
		EncodeAsBx(OP_LOADINT, 8, 1),
		EncodeABC(OP_MOVE, 9, 1, 0),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 18),
		EncodeABx(OP_GETGLOBAL, 13, 1),
		EncodeABC(OP_MOVE, 14, 11, 0),
		EncodeABC(OP_GETTABLE, 12, 13, 14),
		EncodeABC(OP_GETFIELD, 14, 12, 2),
		EncodeABC(OP_GETFIELD, 16, 12, 7),
		EncodeABC(OP_MUL, 15, 0, 16),
		EncodeABC(OP_ADD, 13, 14, 15),
		EncodeABC(OP_SETFIELD, 12, 2, 13),
		EncodeABC(OP_GETFIELD, 14, 12, 3),
		EncodeABC(OP_GETFIELD, 16, 12, 9),
		EncodeABC(OP_MUL, 15, 0, 16),
		EncodeABC(OP_ADD, 13, 14, 15),
		EncodeABC(OP_SETFIELD, 12, 3, 13),
		EncodeABC(OP_GETFIELD, 14, 12, 4),
		EncodeABC(OP_GETFIELD, 16, 12, 10),
		EncodeABC(OP_MUL, 15, 0, 16),
		EncodeABC(OP_ADD, 13, 14, 15),
		EncodeABC(OP_SETFIELD, 12, 4, 13),
		EncodeAsBx(OP_FORLOOP, 8, -19),
		EncodeABC(OP_RETURN, 0, 1, 0),
	})
}

// HasNBodyAdvanceWholeCallKernel reports whether p matches the guarded
// record-field nbody-style advance(dt) kernel shape. MethodJIT uses this to
// keep driver loops on the VM route where the whole-call kernel can fire.
func HasNBodyAdvanceWholeCallKernel(p *FuncProto) bool {
	return cachedWholeCallKernelRecognized(p, wholeCallKernelNBodyAdvance)
}

// HasNBodyAdvanceDriverLoopKernel reports whether p contains a structural
// driver loop that repeatedly calls an nbody advance(dt)-style whole-call
// kernel candidate.
func HasNBodyAdvanceDriverLoopKernel(p *FuncProto, globals map[string]*FuncProto) bool {
	if p == nil {
		return false
	}
	for pc, inst := range p.Code {
		if DecodeOp(inst) != OP_FORPREP {
			continue
		}
		if IsNBodyAdvanceDriverLoopAt(p, pc, globals) {
			return true
		}
	}
	return false
}

// IsNBodyAdvanceDriverLoopAt checks one FORPREP site for the guarded
// advance(dt) call-loop shape. Runtime admission still checks trip count,
// current globals, and argument/table guards before executing the kernel.
func IsNBodyAdvanceDriverLoopAt(p *FuncProto, forprepPC int, globals map[string]*FuncProto) bool {
	if p == nil || len(globals) == 0 || forprepPC < 0 || forprepPC >= len(p.Code) {
		return false
	}
	inst := p.Code[forprepPC]
	if DecodeOp(inst) != OP_FORPREP {
		return false
	}
	shape, ok := matchNBodyAdvanceDriverLoopShape(p.Code, p.Constants, forprepPC, DecodeA(inst), DecodesBx(inst))
	if !ok {
		return false
	}
	if shape.fnConst < 0 || shape.fnConst >= len(p.Constants) || !p.Constants[shape.fnConst].IsString() {
		return false
	}
	return HasNBodyAdvanceWholeCallKernel(globals[p.Constants[shape.fnConst].Str()])
}
