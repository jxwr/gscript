// feedback_test.go tests type feedback collection in the VM interpreter.
// Each test compiles GScript source, enables feedback on the proto,
// runs via VM, then inspects proto.Feedback to verify types match expectations.
package vm

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

// compileProto compiles GScript source and returns the proto.
func compileProto(t *testing.T, src string) *FuncProto {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return proto
}

// runWithFeedback enables feedback on the proto, executes, and returns it.
func runWithFeedback(t *testing.T, proto *FuncProto) {
	t.Helper()
	globals := runtime.NewInterpreterGlobals()
	v := New(globals)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
}

// compileFeedback compiles source, enables feedback, runs it, returns proto.
func compileFeedback(t *testing.T, src string) *FuncProto {
	t.Helper()
	proto := compileProto(t, src)
	proto.EnsureFeedback()
	runWithFeedback(t, proto)
	return proto
}

// compileFeedbackNested enables feedback on the main proto AND all nested protos.
func compileFeedbackNested(t *testing.T, src string) *FuncProto {
	t.Helper()
	proto := compileProto(t, src)
	proto.EnsureFeedback()
	for _, child := range proto.Protos {
		child.EnsureFeedback()
	}
	runWithFeedback(t, proto)
	return proto
}

// findFeedbackForOp scans the proto's code and feedback for the first instruction
// matching the given opcode, returning its TypeFeedback.
func findFeedbackForOp(t *testing.T, proto *FuncProto, op Opcode) TypeFeedback {
	t.Helper()
	if proto.Feedback == nil {
		t.Fatalf("no feedback vector on proto")
	}
	for i, inst := range proto.Code {
		if DecodeOp(inst) == op {
			return proto.Feedback[i]
		}
	}
	t.Fatalf("opcode %s not found in proto", OpName(op))
	return TypeFeedback{}
}

// --- Lattice unit tests ---

func TestFeedback_Lattice(t *testing.T) {
	var ft FeedbackType

	// Starts unobserved
	if ft != FBUnobserved {
		t.Fatalf("expected FBUnobserved, got %d", ft)
	}

	// Observe int -> FBInt
	ft.Observe(runtime.TypeInt)
	if ft != FBInt {
		t.Fatalf("after int: expected FBInt, got %d", ft)
	}

	// Observe int again -> still FBInt
	ft.Observe(runtime.TypeInt)
	if ft != FBInt {
		t.Fatalf("after int+int: expected FBInt, got %d", ft)
	}

	// Observe float -> FBAny (different from int)
	ft.Observe(runtime.TypeFloat)
	if ft != FBAny {
		t.Fatalf("after int+float: expected FBAny, got %d", ft)
	}

	// FBAny is sticky -- observe int again, should still be FBAny
	ft.Observe(runtime.TypeInt)
	if ft != FBAny {
		t.Fatalf("FBAny should be sticky, got %d", ft)
	}
}

func TestFeedback_LatticeHomogeneous(t *testing.T) {
	// Observing the same type repeatedly stays monomorphic; mixing widens to Any
	for _, tc := range []struct {
		vt   runtime.ValueType
		want FeedbackType
	}{
		{runtime.TypeInt, FBInt}, {runtime.TypeFloat, FBFloat},
		{runtime.TypeString, FBString}, {runtime.TypeBool, FBBool},
		{runtime.TypeTable, FBTable}, {runtime.TypeFunction, FBFunction},
	} {
		var ft FeedbackType
		ft.Observe(tc.vt)
		if ft != tc.want {
			t.Fatalf("type %d: first observe: expected %d, got %d", tc.vt, tc.want, ft)
		}
		ft.Observe(tc.vt) // same type again
		if ft != tc.want {
			t.Fatalf("type %d: second observe: expected %d, got %d", tc.vt, tc.want, ft)
		}
	}
	// Mixed types -> FBAny
	var ft FeedbackType
	ft.Observe(runtime.TypeString)
	ft.Observe(runtime.TypeBool)
	if ft != FBAny {
		t.Fatalf("mixed string+bool: expected FBAny, got %d", ft)
	}
}

// --- Integration tests with VM execution ---

func TestFeedback_IntAdd(t *testing.T) {
	proto := compileFeedback(t, `
		a := 10
		b := 20
		c := a + b
	`)
	fb := findFeedbackForOp(t, proto, OP_ADD)
	if fb.Left != FBInt {
		t.Errorf("ADD left: expected FBInt, got %d", fb.Left)
	}
	if fb.Right != FBInt {
		t.Errorf("ADD right: expected FBInt, got %d", fb.Right)
	}
	if fb.Result != FBInt {
		t.Errorf("ADD result: expected FBInt, got %d", fb.Result)
	}
}

func TestFeedback_FloatAdd(t *testing.T) {
	proto := compileFeedback(t, `
		a := 1.5
		b := 2.5
		c := a + b
	`)
	fb := findFeedbackForOp(t, proto, OP_ADD)
	if fb.Left != FBFloat {
		t.Errorf("ADD left: expected FBFloat, got %d", fb.Left)
	}
	if fb.Right != FBFloat {
		t.Errorf("ADD right: expected FBFloat, got %d", fb.Right)
	}
	if fb.Result != FBFloat {
		t.Errorf("ADD result: expected FBFloat, got %d", fb.Result)
	}
}

func TestFeedback_MixedAdd(t *testing.T) {
	// Call a function with ints first, then floats.
	// The ADD inside should widen to FBAny.
	proto := compileFeedbackNested(t, `
		func add(a, b) {
			return a + b
		}
		add(1, 2)
		add(1.5, 2.5)
	`)
	// The ADD is inside the nested proto (the function)
	child := proto.Protos[0]
	fb := findFeedbackForOp(t, child, OP_ADD)
	// After int+int then float+float, left should be FBAny
	if fb.Left != FBAny {
		t.Errorf("mixed ADD left: expected FBAny, got %d", fb.Left)
	}
	if fb.Right != FBAny {
		t.Errorf("mixed ADD right: expected FBAny, got %d", fb.Right)
	}
}

func TestFeedback_LazyInit(t *testing.T) {
	proto := compileProto(t, `a := 1 + 2`)

	// Before EnsureFeedback, Feedback should be nil
	if proto.Feedback != nil {
		t.Fatalf("expected nil Feedback before EnsureFeedback()")
	}

	// After EnsureFeedback, should be allocated with correct length
	fv := proto.EnsureFeedback()
	if fv == nil || len(fv) != len(proto.Code) {
		t.Fatalf("feedback vector length %d != code length %d", len(fv), len(proto.Code))
	}

	// All entries should be unobserved
	for i, fb := range fv {
		if fb.Left != FBUnobserved || fb.Right != FBUnobserved || fb.Result != FBUnobserved {
			t.Errorf("feedback[%d] should be all FBUnobserved, got L=%d R=%d Res=%d",
				i, fb.Left, fb.Right, fb.Result)
		}
	}

	// Idempotent: second call returns same-length vector
	if fv2 := proto.EnsureFeedback(); len(fv2) != len(fv) {
		t.Fatalf("second EnsureFeedback returned different length")
	}
	if proto.TableKeyFeedback == nil || len(proto.TableKeyFeedback) != len(proto.Code) {
		t.Fatalf("table key feedback vector not initialized with code length")
	}
}

func TestFeedback_ForLoop(t *testing.T) {
	proto := compileFeedback(t, `
		sum := 0
		for i := 1; i <= 10; i++ {
			sum = sum + i
		}
	`)
	// The ADD inside the loop should see ints (sum starts as int, i is int)
	fb := findFeedbackForOp(t, proto, OP_ADD)
	if fb.Left != FBInt {
		t.Errorf("for-loop ADD left: expected FBInt, got %d", fb.Left)
	}
	if fb.Right != FBInt {
		t.Errorf("for-loop ADD right: expected FBInt, got %d", fb.Right)
	}
	if fb.Result != FBInt {
		t.Errorf("for-loop ADD result: expected FBInt, got %d", fb.Result)
	}
}

func TestFeedback_Comparison(t *testing.T) {
	proto := compileFeedback(t, `
		a := 10
		b := 20
		if a < b {
			c := 1
		}
	`)
	fb := findFeedbackForOp(t, proto, OP_LT)
	if fb.Left != FBInt {
		t.Errorf("LT left: expected FBInt, got %d", fb.Left)
	}
	if fb.Right != FBInt {
		t.Errorf("LT right: expected FBInt, got %d", fb.Right)
	}
}

func TestFeedback_TableAccess(t *testing.T) {
	proto := compileFeedback(t, `
		t := {x: 1, y: 2}
		v := t.x
	`)
	// GETFIELD should record the value type (int)
	fb := findFeedbackForOp(t, proto, OP_GETFIELD)
	if fb.Result != FBInt {
		t.Errorf("GETFIELD result: expected FBInt, got %d", fb.Result)
	}
}

func TestTableKeyFeedback_ObserveIntKey(t *testing.T) {
	var tk TableKeyFeedback
	tk.ObserveIntKey(runtime.StringValue("x"))
	tk.ObserveIntKey(runtime.IntValue(-1))
	if tk.HasIntKey {
		t.Fatal("non-int and negative keys should not be recorded")
	}

	tk.ObserveIntKey(runtime.IntValue(7))
	tk.ObserveIntKey(runtime.IntValue(3))
	tk.ObserveIntKey(runtime.IntValue(42))
	if !tk.HasIntKey || tk.MaxIntKey != 42 {
		t.Fatalf("expected max int key 42, got has=%v max=%d", tk.HasIntKey, tk.MaxIntKey)
	}
}

func TestTableKeyFeedback_ObserveDenseMatrix(t *testing.T) {
	var tk TableKeyFeedback
	ordinary := runtime.NewTable()
	dense := runtime.NewDenseMatrix(2, runtime.AutoDenseMatrixMinStride)

	tk.ObserveDenseMatrix(dense)
	tk.ObserveDenseMatrix(dense)
	if tk.DenseMatrix != FBDenseMatrixYes {
		t.Fatalf("dense feedback = %d, want yes", tk.DenseMatrix)
	}
	tk.ObserveDenseMatrix(ordinary)
	if tk.DenseMatrix != FBDenseMatrixPolymorphic {
		t.Fatalf("mixed dense feedback = %d, want polymorphic", tk.DenseMatrix)
	}
}

func TestFeedback_TableIntKeyRange(t *testing.T) {
	proto := compileFeedback(t, `
		t := {}
		t[2] = true
		t[10] = false
		v := t[10]
	`)
	if proto.TableKeyFeedback == nil {
		t.Fatal("missing table key feedback")
	}
	var sawSet, sawGet bool
	for pc, inst := range proto.Code {
		switch DecodeOp(inst) {
		case OP_SETTABLE:
			sawSet = true
			if !proto.TableKeyFeedback[pc].HasIntKey {
				t.Fatalf("SETTABLE pc=%d did not record int key", pc)
			}
		case OP_GETTABLE:
			sawGet = true
			if got := proto.TableKeyFeedback[pc].MaxIntKey; got != 10 {
				t.Fatalf("GETTABLE pc=%d max int key=%d, want 10", pc, got)
			}
		}
	}
	if !sawSet || !sawGet {
		t.Fatalf("expected both SETTABLE and GETTABLE in test bytecode")
	}
}

func TestFeedback_FunctionCall(t *testing.T) {
	proto := compileFeedbackNested(t, `
		func foo() {
			return 42
		}
		foo()
	`)
	fb := findFeedbackForOp(t, proto, OP_CALL)
	// Left records the callee type
	if fb.Left != FBFunction {
		t.Errorf("CALL callee: expected FBFunction, got %d", fb.Left)
	}
}

func TestFeedback_SubMulDiv(t *testing.T) {
	proto := compileFeedback(t, `a := 10; b := 3; s := a - b; m := a * b; d := a / b`)
	for _, op := range []Opcode{OP_SUB, OP_MUL, OP_DIV} {
		fb := findFeedbackForOp(t, proto, op)
		if fb.Left != FBInt || fb.Right != FBInt {
			t.Errorf("%s: expected FBInt operands, got L=%d R=%d", OpName(op), fb.Left, fb.Right)
		}
	}
}

func TestFeedback_NoOverheadWithoutInit(t *testing.T) {
	// Run without enabling feedback -- should not panic or allocate
	proto := compileProto(t, `a := 1 + 2`)
	runWithFeedback(t, proto) // runs WITHOUT EnsureFeedback
	if proto.Feedback != nil {
		t.Fatalf("expected nil Feedback when not initialized")
	}
}

// --- ObserveKind unit tests ---

func TestFeedbackKind_StructSize(t *testing.T) {
	// TypeFeedback must be exactly 4 bytes (Left + Right + Result + Kind).
	var tf TypeFeedback
	size := unsafe.Sizeof(tf)
	if size != 4 {
		t.Fatalf("expected TypeFeedback size=4 bytes, got %d", size)
	}
}

func TestFeedbackKind_ObserveKind_Lattice(t *testing.T) {
	var tf TypeFeedback

	// Starts unobserved
	if tf.Kind != FBKindUnobserved {
		t.Fatalf("expected FBKindUnobserved, got %d", tf.Kind)
	}

	// Observe Int array -> FBKindInt
	tf.ObserveKind(1) // ArrayInt=1
	if tf.Kind != FBKindInt {
		t.Fatalf("after ArrayInt: expected FBKindInt(%d), got %d", FBKindInt, tf.Kind)
	}

	// Observe Int again -> still FBKindInt
	tf.ObserveKind(1)
	if tf.Kind != FBKindInt {
		t.Fatalf("after Int+Int: expected FBKindInt, got %d", tf.Kind)
	}

	// Observe Float -> FBKindPolymorphic
	tf.ObserveKind(2) // ArrayFloat=2
	if tf.Kind != FBKindPolymorphic {
		t.Fatalf("after Int+Float: expected FBKindPolymorphic(0xFF), got %d", tf.Kind)
	}

	// Polymorphic is sticky
	tf.ObserveKind(0) // ArrayMixed=0
	if tf.Kind != FBKindPolymorphic {
		t.Fatalf("FBKindPolymorphic should be sticky, got %d", tf.Kind)
	}
}

func TestFeedbackKind_ObserveKind_AllKinds(t *testing.T) {
	for _, tc := range []struct {
		arrayKind uint8
		want      uint8
	}{
		{0, FBKindMixed}, // ArrayMixed
		{1, FBKindInt},   // ArrayInt
		{2, FBKindFloat}, // ArrayFloat
		{3, FBKindBool},  // ArrayBool
	} {
		var tf TypeFeedback
		tf.ObserveKind(tc.arrayKind)
		if tf.Kind != tc.want {
			t.Errorf("arrayKind=%d: expected Kind=%d, got %d", tc.arrayKind, tc.want, tf.Kind)
		}
		// Same kind again -> still monomorphic
		tf.ObserveKind(tc.arrayKind)
		if tf.Kind != tc.want {
			t.Errorf("arrayKind=%d (repeat): expected Kind=%d, got %d", tc.arrayKind, tc.want, tf.Kind)
		}
	}
}
