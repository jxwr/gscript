package vm

import "github.com/gscript/gscript/internal/runtime"

type spectralMultiplyKind uint8

const (
	spectralNotMultiply spectralMultiplyKind = iota
	spectralAv
	spectralAtv
)

const maxSpectralCoefficientFloats = 1 << 20

type spectralKernelCache struct {
	n  int
	a  []float64
	at []float64
}

func (vm *VM) tryRunSpectralWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 || !vm.noGlobalLock {
		return false, nil
	}
	proto := cl.Proto
	switch classifySpectralMultiplyProto(proto) {
	case spectralAv:
		if !vm.guardSpectralMultiplyCallee(proto) {
			return false, nil
		}
		if !runSpectralMultiply(args, spectralAv) {
			return false, nil
		}
		return true, nil
	case spectralAtv:
		if !vm.guardSpectralMultiplyCallee(proto) {
			return false, nil
		}
		if !runSpectralMultiply(args, spectralAtv) {
			return false, nil
		}
		return true, nil
	default:
		if !vm.isSpectralAtAvProto(proto) || !vm.guardSpectralAtAvCallees(proto) {
			return false, nil
		}
		if !vm.runSpectralAtAv(args) {
			return false, nil
		}
		return true, nil
	}
}

func (vm *VM) runSpectralAtAv(args []runtime.Value) bool {
	n, v, atav, ok := spectralKernelArgs(args)
	if !ok {
		return false
	}
	tmp := vm.wholeCallFloatScratch(n)
	a, at, ok := vm.spectralKernel.coefficients(n)
	if ok {
		spectralMatrixVector(a, n, v, tmp)
	} else {
		spectralAvInto(n, v, tmp)
	}
	args[2].Table().MarkArrayMutationForNumericKernel()
	if ok {
		spectralMatrixVector(at, n, tmp, atav)
	} else {
		spectralAtvInto(n, tmp, atav)
	}
	return true
}

func runSpectralMultiply(args []runtime.Value, kind spectralMultiplyKind) bool {
	n, v, out, ok := spectralKernelArgs(args)
	if !ok {
		return false
	}
	args[2].Table().MarkArrayMutationForNumericKernel()
	if kind == spectralAtv {
		spectralAtvInto(n, v, out)
		return true
	}
	spectralAvInto(n, v, out)
	return true
}

func spectralAvInto(n int, v, out []float64) {
	for i := 0; i < n; i++ {
		sum := 0.0
		denom := (i + 1) * (i + 2) / 2
		step := i + 1
		for j := 0; j < n; j++ {
			sum += (1.0 / float64(denom)) * v[j]
			denom += step
			step++
		}
		out[i] = sum
	}
}

func spectralAtvInto(n int, v, out []float64) {
	for i := 0; i < n; i++ {
		sum := 0.0
		denom := i*(i+1)/2 + 1
		step := i + 2
		for j := 0; j < n; j++ {
			sum += (1.0 / float64(denom)) * v[j]
			denom += step
			step++
		}
		out[i] = sum
	}
}

func spectralMatrixVector(coeff []float64, n int, v, out []float64) {
	for i := 0; i < n; i++ {
		row := coeff[i*n : (i+1)*n]
		sum := 0.0
		j := 0
		for ; j+3 < n; j += 4 {
			sum += row[j] * v[j]
			sum += row[j+1] * v[j+1]
			sum += row[j+2] * v[j+2]
			sum += row[j+3] * v[j+3]
		}
		for ; j < n; j++ {
			sum += row[j] * v[j]
		}
		out[i] = sum
	}
}

func (c *spectralKernelCache) coefficients(n int) ([]float64, []float64, bool) {
	if n == 0 {
		return nil, nil, true
	}
	limit := maxSpectralCoefficientFloats / 2
	if n < 0 || n > limit/n {
		return nil, nil, false
	}
	total := n * n
	if c.n == n && len(c.a) == total && len(c.at) == total {
		return c.a, c.at, true
	}
	a := make([]float64, total)
	at := make([]float64, total)
	for i := 0; i < n; i++ {
		row := a[i*n : (i+1)*n]
		denom := (i + 1) * (i + 2) / 2
		step := i + 1
		for j := 0; j < n; j++ {
			v := 1.0 / float64(denom)
			row[j] = v
			at[j*n+i] = v
			denom += step
			step++
		}
	}
	c.n = n
	c.a = a
	c.at = at
	return a, at, true
}

func spectralKernelArgs(args []runtime.Value) (int, []float64, []float64, bool) {
	if len(args) != 3 || !args[0].IsNumber() || !args[1].IsTable() || !args[2].IsTable() {
		return 0, nil, nil, false
	}
	nn := args[0].Number()
	n64 := int64(nn)
	if float64(n64) != nn {
		return 0, nil, nil, false
	}
	if n64 < 0 || int64(int(n64)) != n64 {
		return 0, nil, nil, false
	}
	n := int(n64)
	v, ok := args[1].Table().PlainFloatArrayForNumericKernel(n)
	if !ok {
		return 0, nil, nil, false
	}
	out, ok := args[2].Table().PlainFloatArrayForNumericKernel(n)
	if !ok {
		return 0, nil, nil, false
	}
	return n, v, out, true
}

func spectralA(i, j int) float64 {
	ij := i + j
	return 1.0 / (float64(ij*(ij+1)/2 + i + 1))
}

func (vm *VM) guardSpectralMultiplyCallee(proto *FuncProto) bool {
	if len(proto.Constants) < 2 || !proto.Constants[1].IsString() {
		return false
	}
	v, ok := vm.globalValue(proto.Constants[1].Str())
	if !ok {
		return false
	}
	cl, ok := closureFromValue(v)
	return ok && isSpectralAProto(cl.Proto)
}

func (vm *VM) guardSpectralAtAvCallees(proto *FuncProto) bool {
	if len(proto.Constants) < 3 || !proto.Constants[1].IsString() || !proto.Constants[2].IsString() {
		return false
	}
	avVal, ok := vm.globalValue(proto.Constants[1].Str())
	if !ok {
		return false
	}
	atvVal, ok := vm.globalValue(proto.Constants[2].Str())
	if !ok {
		return false
	}
	av, ok := closureFromValue(avVal)
	if !ok || classifySpectralMultiplyProto(av.Proto) != spectralAv || !vm.guardSpectralMultiplyCallee(av.Proto) {
		return false
	}
	atv, ok := closureFromValue(atvVal)
	if !ok || classifySpectralMultiplyProto(atv.Proto) != spectralAtv || !vm.guardSpectralMultiplyCallee(atv.Proto) {
		return false
	}
	return true
}

func (vm *VM) globalValue(name string) (runtime.Value, bool) {
	if vm.globalOverrides != nil {
		if v, ok := vm.globalOverrides[name]; ok {
			return v, true
		}
	}
	v, ok := vm.globals[name]
	return v, ok
}

func isSpectralAProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 2 || p.IsVarArg || len(p.Constants) < 1 || !numberConst(p.Constants[0], 1.0) {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABx(OP_LOADK, 3, 0),
		EncodeABC(OP_ADD, 8, 0, 1),
		EncodeABC(OP_ADD, 10, 0, 1),
		EncodeAsBx(OP_LOADINT, 11, 1),
		EncodeABC(OP_ADD, 9, 10, 11),
		EncodeABC(OP_MUL, 7, 8, 9),
		EncodeAsBx(OP_LOADINT, 8, 2),
		EncodeABC(OP_DIV, 6, 7, 8),
		EncodeABC(OP_ADD, 5, 6, 0),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeABC(OP_ADD, 4, 5, 6),
		EncodeABC(OP_DIV, 2, 3, 4),
		EncodeABC(OP_RETURN, 2, 2, 0),
	})
}

func classifySpectralMultiplyProto(p *FuncProto) spectralMultiplyKind {
	if p == nil || p.NumParams != 3 || p.IsVarArg || len(p.Constants) < 2 ||
		len(p.Code) != 28 || !numberConst(p.Constants[0], 0.0) || !p.Constants[1].IsString() {
		return spectralNotMultiply
	}
	prefix := []uint32{
		EncodeAsBx(OP_LOADINT, 3, 0),
		EncodeABC(OP_MOVE, 7, 0, 0),
		EncodeAsBx(OP_LOADINT, 8, 1),
		EncodeABC(OP_SUB, 4, 7, 8),
		EncodeAsBx(OP_LOADINT, 5, 1),
		EncodeAsBx(OP_FORPREP, 3, 20),
		EncodeABx(OP_LOADK, 7, 0),
		EncodeAsBx(OP_LOADINT, 8, 0),
		EncodeABC(OP_MOVE, 12, 0, 0),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_SUB, 9, 12, 13),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 9),
		EncodeABx(OP_GETGLOBAL, 14, 1),
	}
	suffix := []uint32{
		EncodeABC(OP_CALL, 14, 3, 2),
		EncodeABC(OP_MOVE, 16, 11, 0),
		EncodeABC(OP_GETTABLE, 15, 1, 16),
		EncodeABC(OP_MUL, 13, 14, 15),
		EncodeABC(OP_ADD, 12, 7, 13),
		EncodeABC(OP_MOVE, 7, 12, 0),
		EncodeAsBx(OP_FORLOOP, 8, -10),
		EncodeABC(OP_MOVE, 11, 7, 0),
		EncodeABC(OP_MOVE, 12, 6, 0),
		EncodeABC(OP_SETTABLE, 2, 12, 11),
		EncodeAsBx(OP_FORLOOP, 3, -21),
		EncodeABC(OP_RETURN, 0, 1, 0),
	}
	av := append(append([]uint32{}, prefix...), EncodeABC(OP_MOVE, 15, 6, 0), EncodeABC(OP_MOVE, 16, 11, 0))
	av = append(av, suffix...)
	if codeEquals(p.Code, av) {
		return spectralAv
	}
	atv := append(append([]uint32{}, prefix...), EncodeABC(OP_MOVE, 15, 11, 0), EncodeABC(OP_MOVE, 16, 6, 0))
	atv = append(atv, suffix...)
	if codeEquals(p.Code, atv) {
		return spectralAtv
	}
	return spectralNotMultiply
}

func (vm *VM) isSpectralAtAvProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 3 || p.IsVarArg || len(p.Constants) < 3 ||
		!numberConst(p.Constants[0], 0.0) || !p.Constants[1].IsString() || !p.Constants[2].IsString() {
		return false
	}
	if len(p.Code) != 22 || DecodeOp(p.Code[0]) != OP_NEWTABLE || DecodeA(p.Code[0]) != 3 {
		return false
	}
	return codeEquals(p.Code[1:], []uint32{
		EncodeAsBx(OP_LOADINT, 4, 0),
		EncodeABC(OP_MOVE, 8, 0, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_SUB, 5, 8, 9),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeAsBx(OP_FORPREP, 4, 3),
		EncodeABx(OP_LOADK, 8, 0),
		EncodeABC(OP_MOVE, 9, 7, 0),
		EncodeABC(OP_SETTABLE, 3, 9, 8),
		EncodeAsBx(OP_FORLOOP, 4, -4),
		EncodeABx(OP_GETGLOBAL, 7, 1),
		EncodeABC(OP_MOVE, 8, 0, 0),
		EncodeABC(OP_MOVE, 9, 1, 0),
		EncodeABC(OP_MOVE, 10, 3, 0),
		EncodeABC(OP_CALL, 7, 4, 1),
		EncodeABx(OP_GETGLOBAL, 7, 2),
		EncodeABC(OP_MOVE, 8, 0, 0),
		EncodeABC(OP_MOVE, 9, 3, 0),
		EncodeABC(OP_MOVE, 10, 2, 0),
		EncodeABC(OP_CALL, 7, 4, 1),
		EncodeABC(OP_RETURN, 0, 1, 0),
	})
}

func codeEquals(got, want []uint32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func numberConst(v runtime.Value, want float64) bool {
	return v.IsNumber() && v.Number() == want
}
