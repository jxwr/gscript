package methodjit

import (
	"strings"
	"testing"
)

func TestIntrinsic_MatrixDenseLowersToOp(t *testing.T) {
	proto := compile(t, `
func f(rows, cols) {
	return matrix.dense(rows, cols)
}
`)
	fn := BuildGraph(proto)
	fn, notes := IntrinsicPass(fn)
	fn, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}
	ir := Print(fn)
	if got := countOpHelper(fn, OpMatrixDense); got != 1 {
		t.Fatalf("MatrixDense count=%d, want 1\n%s", got, ir)
	}
	if got := countOpHelper(fn, OpCall); got != 0 {
		t.Fatalf("matrix.dense call should be eliminated\n%s", ir)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "matrix.dense") {
		t.Fatalf("missing matrix.dense intrinsic note: %#v", notes)
	}
}
