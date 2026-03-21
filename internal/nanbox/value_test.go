package nanbox

import (
	"math"
	"math/rand"
	"testing"
	"unsafe"
)

// =========================================================================
// Float64 roundtrip
// =========================================================================

func TestFloat64Roundtrip(t *testing.T) {
	cases := []float64{
		0, 1, -1, 0.5, -0.5,
		3.14159265358979323846,
		math.Pi, math.E,
		math.Inf(1), math.Inf(-1),
		math.MaxFloat64, math.SmallestNonzeroFloat64,
		-math.MaxFloat64, -math.SmallestNonzeroFloat64,
		1e-308, 1e308,
		// Subnormals
		5e-324, -5e-324,
		// Negative zero
		math.Copysign(0, -1),
	}

	for _, f := range cases {
		v := FromFloat64(f)
		if !v.IsFloat() {
			t.Errorf("FromFloat64(%g): IsFloat() = false", f)
			continue
		}
		if v.IsInt() || v.IsBool() || v.IsNil() || v.IsPointer() {
			t.Errorf("FromFloat64(%g): wrongly classified as non-float", f)
		}
		got := v.ToFloat64()
		if math.IsNaN(f) {
			if !math.IsNaN(got) {
				t.Errorf("FromFloat64(NaN): ToFloat64() = %g, want NaN", got)
			}
		} else if got != f {
			// Use bits comparison for negative zero
			if math.Float64bits(got) != math.Float64bits(f) {
				t.Errorf("FromFloat64(%g): ToFloat64() = %g (bits: %016X vs %016X)",
					f, got, math.Float64bits(f), math.Float64bits(got))
			}
		}
	}
}

func TestNegativeZero(t *testing.T) {
	nz := math.Copysign(0, -1)
	v := FromFloat64(nz)
	if !v.IsFloat() {
		t.Fatal("negative zero: IsFloat() = false")
	}
	got := v.ToFloat64()
	if math.Float64bits(got) != math.Float64bits(nz) {
		t.Errorf("negative zero roundtrip failed: got bits %016X, want %016X",
			math.Float64bits(got), math.Float64bits(nz))
	}
}

// =========================================================================
// NaN handling
// =========================================================================

func TestNaNHandling(t *testing.T) {
	v := FromFloat64(math.NaN())
	if !v.IsFloat() {
		t.Fatal("NaN: IsFloat() = false")
	}
	if !math.IsNaN(v.ToFloat64()) {
		t.Errorf("NaN: ToFloat64() = %g, want NaN", v.ToFloat64())
	}
}

func TestNaNCanonicalization(t *testing.T) {
	// Create exotic NaN patterns that might collide with tag space.
	// All should be canonicalized.
	exoticNaNs := []uint64{
		// Various NaN patterns with bits 50-62 set (would collide with tags)
		0x7FFC000000000001, // qNaN with bit 50 set + payload
		0x7FFC000000000042,
		0x7FFD000000000000, // would look like tagBool if sign were 1
		0x7FFE000000000000,
		0x7FFFFFFFFFFFFFFF, // all bits set (positive)
		0xFFFC000000000000, // matches tagNil
		0xFFFD000000000001, // matches tagBool|1 = valTrue
		0xFFFE000000000123, // matches tagInt
		0xFFFF000000000ABC, // matches tagPtr
		0xFFFFFFFFFFFFFFFF, // all bits set
	}

	for _, bits := range exoticNaNs {
		f := math.Float64frombits(bits)
		if !math.IsNaN(f) {
			continue // skip if not actually NaN (shouldn't happen for these patterns)
		}
		v := FromFloat64(f)
		if !v.IsFloat() {
			t.Errorf("exotic NaN (0x%016X): IsFloat() = false", bits)
			continue
		}
		if !math.IsNaN(v.ToFloat64()) {
			t.Errorf("exotic NaN (0x%016X): ToFloat64() = %g, want NaN", bits, v.ToFloat64())
		}
		// Must be canonicalized to the standard NaN
		if uint64(v) != canonicalNaN {
			t.Errorf("exotic NaN (0x%016X): not canonicalized, got 0x%016X, want 0x%016X",
				bits, uint64(v), canonicalNaN)
		}
		// Must NOT be classified as any tagged type
		if v.IsInt() || v.IsBool() || v.IsNil() || v.IsPointer() {
			t.Errorf("exotic NaN (0x%016X): misclassified as tagged type", bits)
		}
	}
}

// =========================================================================
// Int roundtrip
// =========================================================================

func TestIntRoundtrip(t *testing.T) {
	cases := []int64{
		0, 1, -1, 42, -42,
		100, -100, 1000, -1000,
		1 << 16, -(1 << 16),
		1 << 31, -(1 << 31),
		1<<47 - 1, // maxInt48
		-(1 << 47), // minInt48
		12345678901234,
		-12345678901234,
	}

	for _, i := range cases {
		v := FromInt(i)
		if !v.IsInt() {
			t.Errorf("FromInt(%d): IsInt() = false", i)
			continue
		}
		if v.IsFloat() || v.IsBool() || v.IsNil() || v.IsPointer() {
			t.Errorf("FromInt(%d): wrongly classified as non-int", i)
		}
		got := v.ToInt()
		if got != i {
			t.Errorf("FromInt(%d): ToInt() = %d", i, got)
		}
	}
}

func TestIntBoundary(t *testing.T) {
	// Exact boundary values
	v := FromInt(maxInt48)
	if !v.IsInt() || v.ToInt() != maxInt48 {
		t.Errorf("maxInt48: got %d, want %d (IsInt=%v)", v.ToInt(), maxInt48, v.IsInt())
	}

	v = FromInt(minInt48)
	if !v.IsInt() || v.ToInt() != minInt48 {
		t.Errorf("minInt48: got %d, want %d (IsInt=%v)", v.ToInt(), minInt48, v.IsInt())
	}
}

func TestIntOverflowPromotesToFloat(t *testing.T) {
	// Values outside 48-bit range should be promoted to float64.
	overflows := []int64{
		1 << 47,        // maxInt48 + 1
		-(1<<47) - 1,   // minInt48 - 1
		1 << 62,
		-(1 << 62),
		math.MaxInt64,
		math.MinInt64,
	}

	for _, i := range overflows {
		v := FromInt(i)
		if v.IsInt() {
			t.Errorf("FromInt(%d): expected promotion to float, but IsInt()=true", i)
			continue
		}
		if !v.IsFloat() {
			t.Errorf("FromInt(%d): expected IsFloat()=true after promotion", i)
			continue
		}
		// The float value should equal float64(i) (may lose precision for large ints)
		expected := float64(i)
		got := v.ToFloat64()
		if got != expected {
			t.Errorf("FromInt(%d) promoted to float: got %g, want %g", i, got, expected)
		}
	}
}

// =========================================================================
// Bool roundtrip
// =========================================================================

func TestBoolRoundtrip(t *testing.T) {
	vTrue := FromBool(true)
	if !vTrue.IsBool() {
		t.Fatal("FromBool(true): IsBool() = false")
	}
	if vTrue.IsFloat() || vTrue.IsInt() || vTrue.IsNil() || vTrue.IsPointer() {
		t.Error("FromBool(true): wrongly classified as non-bool")
	}
	if !vTrue.ToBool() {
		t.Error("FromBool(true): ToBool() = false")
	}

	vFalse := FromBool(false)
	if !vFalse.IsBool() {
		t.Fatal("FromBool(false): IsBool() = false")
	}
	if vFalse.ToBool() {
		t.Error("FromBool(false): ToBool() = true")
	}
}

func TestBoolConstants(t *testing.T) {
	if True != FromBool(true) {
		t.Error("True != FromBool(true)")
	}
	if False != FromBool(false) {
		t.Error("False != FromBool(false)")
	}
	if True == False {
		t.Error("True == False")
	}
}

// =========================================================================
// Nil
// =========================================================================

func TestNil(t *testing.T) {
	v := FromNil()
	if !v.IsNil() {
		t.Fatal("FromNil(): IsNil() = false")
	}
	if v.IsFloat() || v.IsInt() || v.IsBool() || v.IsPointer() {
		t.Error("FromNil(): wrongly classified as non-nil")
	}
	if v != Nil {
		t.Error("FromNil() != Nil constant")
	}
}

// =========================================================================
// Pointer roundtrip
// =========================================================================

func TestPointerRoundtrip(t *testing.T) {
	x := new(int)
	*x = 42
	p := unsafe.Pointer(x)

	v := FromPointer(p)
	if !v.IsPointer() {
		t.Fatal("FromPointer: IsPointer() = false")
	}
	if v.IsFloat() || v.IsInt() || v.IsBool() || v.IsNil() {
		t.Error("FromPointer: wrongly classified as non-pointer")
	}

	got := v.ToPointer()
	if got != p {
		t.Errorf("FromPointer: ToPointer() = %p, want %p", got, p)
	}

	// Verify the pointed-to value is still accessible
	gotVal := *(*int)(got)
	if gotVal != 42 {
		t.Errorf("pointer dereference: got %d, want 42", gotVal)
	}
}

func TestPointerMultiple(t *testing.T) {
	// Test several different pointer values
	a := new(int64)
	b := new(string)
	c := new([100]byte)

	ptrs := []unsafe.Pointer{
		unsafe.Pointer(a),
		unsafe.Pointer(b),
		unsafe.Pointer(c),
	}

	for _, p := range ptrs {
		v := FromPointer(p)
		if !v.IsPointer() {
			t.Errorf("pointer %p: IsPointer() = false", p)
			continue
		}
		if v.ToPointer() != p {
			t.Errorf("pointer %p: roundtrip failed, got %p", p, v.ToPointer())
		}
	}
}

func TestNullPointer(t *testing.T) {
	v := FromPointer(nil)
	if !v.IsPointer() {
		t.Fatal("FromPointer(nil): IsPointer() = false")
	}
	if v.ToPointer() != nil {
		t.Errorf("FromPointer(nil): ToPointer() = %p, want nil", v.ToPointer())
	}
	// Null pointer is NOT nil Value
	if v.IsNil() {
		t.Error("FromPointer(nil) should not be IsNil()")
	}
}

// =========================================================================
// Truthy
// =========================================================================

func TestTruthy(t *testing.T) {
	cases := []struct {
		v    Value
		want bool
		name string
	}{
		{Nil, false, "nil"},
		{False, false, "false"},
		{True, true, "true"},
		{FromInt(0), true, "int(0)"},
		{FromInt(1), true, "int(1)"},
		{FromFloat64(0), true, "float(0)"},
		{FromFloat64(1), true, "float(1)"},
		{FromFloat64(math.NaN()), true, "float(NaN)"},
	}

	for _, tc := range cases {
		if tc.v.Truthy() != tc.want {
			t.Errorf("Truthy(%s) = %v, want %v", tc.name, tc.v.Truthy(), tc.want)
		}
	}
}

// =========================================================================
// IsNumber
// =========================================================================

func TestIsNumber(t *testing.T) {
	if !FromFloat64(3.14).IsNumber() {
		t.Error("float should be IsNumber()")
	}
	if !FromInt(42).IsNumber() {
		t.Error("int should be IsNumber()")
	}
	if FromBool(true).IsNumber() {
		t.Error("bool should not be IsNumber()")
	}
	if Nil.IsNumber() {
		t.Error("nil should not be IsNumber()")
	}
}

// =========================================================================
// ToNumber
// =========================================================================

func TestToNumber(t *testing.T) {
	v := FromFloat64(3.14)
	if v.ToNumber() != 3.14 {
		t.Errorf("ToNumber(float(3.14)) = %g", v.ToNumber())
	}

	v = FromInt(42)
	if v.ToNumber() != 42.0 {
		t.Errorf("ToNumber(int(42)) = %g", v.ToNumber())
	}
}

// =========================================================================
// String (debug output)
// =========================================================================

func TestString(t *testing.T) {
	cases := []struct {
		v    Value
		want string
	}{
		{Nil, "nil"},
		{True, "true"},
		{False, "false"},
		{FromInt(42), "int(42)"},
		{FromInt(-1), "int(-1)"},
		{FromFloat64(3.14), "float(3.14)"},
	}

	for _, tc := range cases {
		got := tc.v.String()
		if got != tc.want {
			t.Errorf("String(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

// =========================================================================
// No collisions (fuzz-like)
// =========================================================================

func TestNoCollisions_RandomFloats(t *testing.T) {
	// Generate 1M random float64 values and verify none of them are
	// misclassified as tagged types.
	rng := rand.New(rand.NewSource(12345))

	for i := 0; i < 1_000_000; i++ {
		// Generate random float64 (including special values via bit manipulation)
		var f float64
		if i%10 == 0 {
			// Random bit pattern -- may be NaN, Inf, subnormal, etc.
			bits := rng.Uint64()
			f = math.Float64frombits(bits)
		} else {
			// Normal random float
			f = rng.NormFloat64() * 1e100
		}

		v := FromFloat64(f)

		// Must be classified as float
		if !v.IsFloat() {
			t.Fatalf("iteration %d: FromFloat64(%g / 0x%016X) IsFloat()=false, raw=0x%016X",
				i, f, math.Float64bits(f), uint64(v))
		}

		// Must NOT be classified as any tagged type
		if v.IsInt() {
			t.Fatalf("iteration %d: float %g misclassified as int", i, f)
		}
		if v.IsBool() {
			t.Fatalf("iteration %d: float %g misclassified as bool", i, f)
		}
		if v.IsNil() {
			t.Fatalf("iteration %d: float %g misclassified as nil", i, f)
		}
		if v.IsPointer() {
			t.Fatalf("iteration %d: float %g misclassified as pointer", i, f)
		}

		// Roundtrip must preserve value (or canonicalize NaN)
		got := v.ToFloat64()
		if math.IsNaN(f) {
			if !math.IsNaN(got) {
				t.Fatalf("iteration %d: NaN roundtrip produced %g", i, got)
			}
		} else {
			if math.Float64bits(got) != math.Float64bits(v.ToFloat64()) {
				t.Fatalf("iteration %d: float %g roundtrip corrupted", i, f)
			}
		}
	}
}

func TestNoCollisions_AllTypes(t *testing.T) {
	// Build a set of Values of all types and verify no two different
	// types share the same bit pattern.
	values := []struct {
		v    Value
		typ  string
	}{
		{Nil, "nil"},
		{True, "bool"},
		{False, "bool"},
		{FromInt(0), "int"},
		{FromInt(1), "int"},
		{FromInt(-1), "int"},
		{FromInt(maxInt48), "int"},
		{FromInt(minInt48), "int"},
		{FromFloat64(0), "float"},
		{FromFloat64(1), "float"},
		{FromFloat64(-1), "float"},
		{FromFloat64(math.Inf(1)), "float"},
		{FromFloat64(math.Inf(-1)), "float"},
		{FromFloat64(math.NaN()), "float"},
		{FromFloat64(math.Pi), "float"},
	}

	for i, a := range values {
		for j, b := range values {
			if i == j {
				continue
			}
			if uint64(a.v) == uint64(b.v) && (a.typ != b.typ || a.v.String() != b.v.String()) {
				t.Errorf("collision: %s (%s, 0x%016X) == %s (%s, 0x%016X)",
					a.v.String(), a.typ, uint64(a.v),
					b.v.String(), b.typ, uint64(b.v))
			}
		}
	}
}

// =========================================================================
// Cross-type discrimination
// =========================================================================

func TestCrossTypeDiscrimination(t *testing.T) {
	// Every type check should be exclusive (except IsNumber which is a union).
	allValues := []Value{
		Nil,
		True,
		False,
		FromInt(0),
		FromInt(1),
		FromInt(-1),
		FromInt(maxInt48),
		FromInt(minInt48),
		FromFloat64(0),
		FromFloat64(1),
		FromFloat64(-1),
		FromFloat64(math.Inf(1)),
		FromFloat64(math.NaN()),
	}

	for _, v := range allValues {
		count := 0
		if v.IsFloat() {
			count++
		}
		if v.IsInt() {
			count++
		}
		if v.IsBool() {
			count++
		}
		if v.IsNil() {
			count++
		}
		if v.IsPointer() {
			count++
		}

		if count != 1 {
			t.Errorf("value %s (0x%016X): matched %d type checks (should be exactly 1)",
				v.String(), uint64(v), count)
		}
	}
}

// =========================================================================
// Edge cases
// =========================================================================

func TestInfinity(t *testing.T) {
	posInf := FromFloat64(math.Inf(1))
	if !posInf.IsFloat() {
		t.Error("+Inf: IsFloat() = false")
	}
	if !math.IsInf(posInf.ToFloat64(), 1) {
		t.Errorf("+Inf: ToFloat64() = %g", posInf.ToFloat64())
	}

	negInf := FromFloat64(math.Inf(-1))
	if !negInf.IsFloat() {
		t.Error("-Inf: IsFloat() = false")
	}
	if !math.IsInf(negInf.ToFloat64(), -1) {
		t.Errorf("-Inf: ToFloat64() = %g", negInf.ToFloat64())
	}
}

func TestIntZeroVsFloatZero(t *testing.T) {
	intZero := FromInt(0)
	floatZero := FromFloat64(0)

	// They should have different bit patterns
	if uint64(intZero) == uint64(floatZero) {
		t.Error("int(0) and float(0) have same bit pattern -- should be different")
	}

	// But each should roundtrip correctly
	if !intZero.IsInt() || intZero.ToInt() != 0 {
		t.Error("int(0) roundtrip failed")
	}
	if !floatZero.IsFloat() || floatZero.ToFloat64() != 0 {
		t.Error("float(0) roundtrip failed")
	}
}

func TestIntSignExtension(t *testing.T) {
	// Test that negative integers are correctly sign-extended.
	negatives := []int64{-1, -2, -100, -1000000, minInt48}

	for _, i := range negatives {
		v := FromInt(i)
		got := v.ToInt()
		if got != i {
			t.Errorf("sign extension: FromInt(%d).ToInt() = %d", i, got)
		}
	}
}

// =========================================================================
// Raw access
// =========================================================================

func TestRaw(t *testing.T) {
	v := FromInt(42)
	raw := v.Raw()
	v2 := FromRaw(raw)
	if v != v2 {
		t.Errorf("Raw roundtrip: %v != %v", v, v2)
	}
}

// =========================================================================
// Encoding invariants from design doc (Appendix C)
// =========================================================================

func TestEncodingInvariants(t *testing.T) {
	// Invariant 1: FloatValue(f).Float() == f for all non-NaN f
	for _, f := range []float64{0, 1, -1, math.Pi, math.Inf(1), math.Inf(-1), math.MaxFloat64} {
		v := FromFloat64(f)
		if v.ToFloat64() != f {
			t.Errorf("invariant 1: FromFloat64(%g).ToFloat64() = %g", f, v.ToFloat64())
		}
	}

	// Invariant 2: FloatValue(NaN).Float() is NaN
	if !math.IsNaN(FromFloat64(math.NaN()).ToFloat64()) {
		t.Error("invariant 2: NaN roundtrip failed")
	}

	// Invariant 3: IntValue(i).Int() == i for |i| < 2^47
	for _, i := range []int64{0, 1, -1, maxInt48, minInt48, 123456789} {
		if FromInt(i).ToInt() != i {
			t.Errorf("invariant 3: FromInt(%d).ToInt() = %d", i, FromInt(i).ToInt())
		}
	}

	// Invariant 4: BoolValue(true).Bool() == true
	if !FromBool(true).ToBool() {
		t.Error("invariant 4 failed")
	}

	// Invariant 5: BoolValue(false).Bool() == false
	if FromBool(false).ToBool() {
		t.Error("invariant 5 failed")
	}

	// Invariant 6: NilValue().IsNil() == true
	if !FromNil().IsNil() {
		t.Error("invariant 6 failed")
	}

	// Invariant 7: PtrValue(p).Ptr() == p
	x := new(int)
	p := unsafe.Pointer(x)
	if FromPointer(p).ToPointer() != p {
		t.Error("invariant 7 failed")
	}

	// Invariant 8: IsFloat(FloatValue(f)) for all non-NaN f
	for _, f := range []float64{0, 1, -1, math.Pi} {
		if !FromFloat64(f).IsFloat() {
			t.Errorf("invariant 8: FromFloat64(%g).IsFloat() = false", f)
		}
	}

	// Invariant 9: !IsFloat(IntValue(i))
	if FromInt(42).IsFloat() {
		t.Error("invariant 9 failed")
	}

	// Invariant 10: !IsFloat(PtrValue(p))
	if FromPointer(p).IsFloat() {
		t.Error("invariant 10 failed")
	}

	// Invariant 11: tested by TestNoCollisions_RandomFloats
}

// =========================================================================
// Bit-level verification
// =========================================================================

func TestBitPatterns(t *testing.T) {
	// Verify specific bit patterns match the design doc.

	// Nil: must be tagNil = 0xFFFC000000000000
	if uint64(Nil) != 0xFFFC000000000000 {
		t.Errorf("Nil bits: 0x%016X, want 0xFFFC000000000000", uint64(Nil))
	}

	// True: must be tagBool | 1 = 0xFFFD000000000001
	if uint64(True) != 0xFFFD000000000001 {
		t.Errorf("True bits: 0x%016X, want 0xFFFD000000000001", uint64(True))
	}

	// False: must be tagBool = 0xFFFD000000000000
	if uint64(False) != 0xFFFD000000000000 {
		t.Errorf("False bits: 0x%016X, want 0xFFFD000000000000", uint64(False))
	}

	// Int(0): must be tagInt = 0xFFFE000000000000
	if uint64(FromInt(0)) != 0xFFFE000000000000 {
		t.Errorf("Int(0) bits: 0x%016X, want 0xFFFE000000000000", uint64(FromInt(0)))
	}

	// Int(1): must be tagInt | 1 = 0xFFFE000000000001
	if uint64(FromInt(1)) != 0xFFFE000000000001 {
		t.Errorf("Int(1) bits: 0x%016X, want 0xFFFE000000000001", uint64(FromInt(1)))
	}

	// Float(0): must be 0x0000000000000000 (positive zero)
	if uint64(FromFloat64(0)) != 0x0000000000000000 {
		t.Errorf("Float(0) bits: 0x%016X, want 0x0000000000000000", uint64(FromFloat64(0)))
	}

	// Float(1.0): must be 0x3FF0000000000000
	if uint64(FromFloat64(1.0)) != 0x3FF0000000000000 {
		t.Errorf("Float(1.0) bits: 0x%016X, want 0x3FF0000000000000", uint64(FromFloat64(1.0)))
	}
}

// =========================================================================
// Size verification
// =========================================================================

func TestValueSize(t *testing.T) {
	if unsafe.Sizeof(Value(0)) != 8 {
		t.Errorf("sizeof(Value) = %d, want 8", unsafe.Sizeof(Value(0)))
	}
}

// =========================================================================
// Benchmarks
// =========================================================================

func BenchmarkFromFloat64(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = FromFloat64(3.14)
	}
}

func BenchmarkFromInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = FromInt(int64(i))
	}
}

func BenchmarkIsFloat(b *testing.B) {
	v := FromFloat64(3.14)
	for i := 0; i < b.N; i++ {
		_ = v.IsFloat()
	}
}

func BenchmarkIsInt(b *testing.B) {
	v := FromInt(42)
	for i := 0; i < b.N; i++ {
		_ = v.IsInt()
	}
}

func BenchmarkToFloat64(b *testing.B) {
	v := FromFloat64(3.14)
	for i := 0; i < b.N; i++ {
		_ = v.ToFloat64()
	}
}

func BenchmarkToInt(b *testing.B) {
	v := FromInt(42)
	for i := 0; i < b.N; i++ {
		_ = v.ToInt()
	}
}

func BenchmarkTruthy(b *testing.B) {
	v := FromInt(42)
	for i := 0; i < b.N; i++ {
		_ = v.Truthy()
	}
}
