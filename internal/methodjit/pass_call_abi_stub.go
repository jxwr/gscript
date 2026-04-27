//go:build !darwin || !arm64

package methodjit

// AnnotateCallABIsPass is a no-op on platforms without the raw-int specialized
// ABI implementation.
func AnnotateCallABIsPass(config CallABIAnnotationConfig) PassFunc {
	return func(fn *Function) (*Function, error) {
		return fn, nil
	}
}

// AnnotateCallABIs is a no-op on platforms without the raw-int specialized ABI
// implementation.
func AnnotateCallABIs(fn *Function, config CallABIAnnotationConfig) *Function {
	return fn
}
