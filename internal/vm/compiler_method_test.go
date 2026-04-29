package vm

import "testing"

func TestCompiler_MethodCallUsesGetFieldForEncodableMethodName(t *testing.T) {
	proto := compileProto(t, `
obj := {}
obj.value = 41
obj.inc = func(self, n) { return self.value + n }
result := obj:inc(1)
`)

	foundGetField := false
	for pc, inst := range proto.Code {
		if DecodeOp(inst) == OP_SELF {
			t.Fatalf("compiled encodable method call emitted OP_SELF at pc %d; want MOVE+GETFIELD", pc)
		}
		if DecodeOp(inst) != OP_GETFIELD {
			continue
		}
		c := DecodeC(inst)
		if c >= len(proto.Constants) {
			t.Fatalf("OP_GETFIELD constant index %d out of bounds", c)
		}
		if !proto.Constants[c].IsString() || proto.Constants[c].Str() != "inc" {
			continue
		}
		foundGetField = true
		if pc == 0 || DecodeOp(proto.Code[pc-1]) != OP_MOVE {
			t.Fatalf("method GETFIELD at pc %d was not preceded by receiver MOVE", pc)
		}
		move := proto.Code[pc-1]
		if DecodeA(move) != DecodeA(inst)+1 || DecodeB(move) != DecodeB(inst) {
			t.Fatalf("receiver MOVE = A%d B%d, want A%d B%d", DecodeA(move), DecodeB(move), DecodeA(inst)+1, DecodeB(inst))
		}
	}
	if !foundGetField {
		t.Fatal("compiled method call did not emit OP_GETFIELD")
	}
}
