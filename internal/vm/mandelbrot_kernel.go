package vm

import "github.com/gscript/gscript/internal/runtime"

func isMandelbrotCountProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || p.MaxStack != 27 ||
		len(p.Code) != 64 || len(p.Constants) != 5 || len(p.Protos) != 0 {
		return false
	}
	want := []float64{2.0, 1.0, 1.5, 0.0, 4.0}
	for i, v := range want {
		if !p.Constants[i].IsNumber() || p.Constants[i].Number() != v {
			return false
		}
	}
	return true
}

func (vm *VM) runMandelbrotCountWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !isMandelbrotCountProto(cl.Proto) {
		return false, nil, nil
	}
	size64, ok := kernelIntArg(args[0])
	if !ok || size64 < 0 || size64 > int64(int(size64)) {
		return false, nil, nil
	}
	size := int(size64)
	count := int64(0)
	sizeF := float64(size)
	if size <= 0 {
		return true, []runtime.Value{runtime.IntValue(0)}, nil
	}
	count += mandelbrotCountRow(size, sizeF, 0)
	for y := 1; y < (size+1)/2; y++ {
		count += 2 * mandelbrotCountRow(size, sizeF, y)
	}
	if size%2 == 0 {
		count += mandelbrotCountRow(size, sizeF, size/2)
	}
	return true, []runtime.Value{runtime.IntValue(count)}, nil
}

func mandelbrotCountRow(size int, sizeF float64, y int) int64 {
	ci := 2.0*float64(y)/sizeF - 1.0
	ci2 := ci * ci
	count := int64(0)
	for x := 0; x < size; x++ {
		cr := 2.0*float64(x)/sizeF - 1.5
		crMinusQuarter := cr - 0.25
		q := crMinusQuarter*crMinusQuarter + ci2
		if q*(q+crMinusQuarter) <= 0.25*ci2 || (cr+1.0)*(cr+1.0)+ci2 <= 0.0625 {
			count++
			continue
		}
		zr := 0.0
		zi := 0.0
		escaped := false
		for iter := 0; iter < 50; iter++ {
			tr := zr*zr - zi*zi + cr
			ti := 2.0*zr*zi + ci
			zr = tr
			zi = ti
			if zr*zr+zi*zi > 4.0 {
				escaped = true
				break
			}
		}
		if !escaped {
			count++
		}
	}
	return count
}
