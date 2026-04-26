package runtime

import (
	"testing"
)

// ==================================================================
// Type-specialized array tests
// ==================================================================

func TestArrayKindPromotionInt(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(42))
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected IntValue(42), got %v", v)
	}
}

func TestArrayKindPromotionEmptyTableKeyZero(t *testing.T) {
	cases := []struct {
		name string
		val  Value
		want Value
		kind ArrayKind
	}{
		{name: "int", val: IntValue(10), want: IntValue(10), kind: ArrayInt},
		{name: "float", val: FloatValue(1.25), want: FloatValue(1.25), kind: ArrayFloat},
		{name: "bool", val: BoolValue(false), want: BoolValue(false), kind: ArrayBool},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tbl := NewTable()
			tbl.RawSetInt(0, tc.val)
			if tbl.arrayKind != tc.kind {
				t.Fatalf("arrayKind = %d, want %d", tbl.arrayKind, tc.kind)
			}
			got := tbl.RawGetInt(0)
			if got != tc.want {
				t.Fatalf("RawGetInt(0) = %v, want %v", got, tc.want)
			}
			if got1 := tbl.RawGetInt(1); !got1.IsNil() {
				t.Fatalf("RawGetInt(1) = %v, want nil", got1)
			}
		})
	}
}

func TestArrayKindPromotionBool(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(true))
	// Bool promotes to ArrayBool ([]byte, 1B/element, no GC pointers).
	// Preserves truthiness: BoolValue(false).Truthy() == false.
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.Truthy() {
		t.Errorf("expected truthy for true, got %v", v)
	}
}

func TestArrayKindFloat(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, FloatValue(3.14))
	if tbl.arrayKind != ArrayFloat {
		t.Errorf("expected ArrayFloat, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsFloat() || v.Float() != 3.14 {
		t.Errorf("expected FloatValue(3.14), got %v", v)
	}
}

func TestArrayKindDemotionIntToMixed(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(1))
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	tbl.RawSetInt(2, StringValue("hello"))
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed after string, got %d", tbl.arrayKind)
	}
	// Check both values are readable after demotion
	v1 := tbl.RawGetInt(1)
	if !v1.IsInt() || v1.Int() != 1 {
		t.Errorf("expected IntValue(1) after demotion, got %v", v1)
	}
	v2 := tbl.RawGetInt(2)
	if !v2.IsString() || v2.Str() != "hello" {
		t.Errorf("expected StringValue(hello) after demotion, got %v", v2)
	}
}

func TestArrayKindDemotionFloatToMixed(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, FloatValue(1.5))
	if tbl.arrayKind != ArrayFloat {
		t.Errorf("expected ArrayFloat, got %d", tbl.arrayKind)
	}
	tbl.RawSetInt(2, IntValue(42))
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed after int->float demotion, got %d", tbl.arrayKind)
	}
	v1 := tbl.RawGetInt(1)
	if !v1.IsFloat() || v1.Float() != 1.5 {
		t.Errorf("expected FloatValue(1.5) after demotion, got %v", v1)
	}
	v2 := tbl.RawGetInt(2)
	if !v2.IsInt() || v2.Int() != 42 {
		t.Errorf("expected IntValue(42) after demotion, got %v", v2)
	}
}

func TestArrayKindDemotionNilWrite(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(10))
	tbl.RawSetInt(2, IntValue(20))
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	// Writing nil demotes to mixed
	tbl.RawSetInt(1, NilValue())
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed after nil write, got %d", tbl.arrayKind)
	}
}

func TestArrayKindSievePattern(t *testing.T) {
	// Simulate sieve: fill with true, mark false, count.
	// Bools promote to ArrayBool ([]byte, 1B/element, no GC pointers).
	// Preserves truthiness: BoolValue(false).Truthy() == false.
	tbl := NewTable()
	for i := int64(1); i <= 100; i++ {
		tbl.RawSetInt(i, BoolValue(true))
	}
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool for bool sieve, got %d", tbl.arrayKind)
	}
	for i := int64(1); i <= 100; i++ {
		v := tbl.RawGetInt(i)
		if !v.Truthy() {
			t.Errorf("expected truthy at %d, got %v", i, v)
		}
	}
	// Mark some as false
	for i := int64(2); i <= 100; i += 2 {
		tbl.RawSetInt(i, BoolValue(false))
	}
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool after false writes, got %d", tbl.arrayKind)
	}
	// Check odd positions are still truthy
	for i := int64(1); i <= 100; i += 2 {
		v := tbl.RawGetInt(i)
		if !v.Truthy() {
			t.Errorf("expected truthy at odd %d, got %v", i, v)
		}
	}
	// Check even positions are falsy
	for i := int64(2); i <= 100; i += 2 {
		v := tbl.RawGetInt(i)
		if v.Truthy() {
			t.Errorf("expected falsy at even %d, got %v", i, v)
		}
	}
}

func TestArrayKindLengthInt(t *testing.T) {
	tbl := NewTable()
	for i := int64(1); i <= 50; i++ {
		tbl.RawSetInt(i, IntValue(i))
	}
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	if tbl.Length() != 50 {
		t.Errorf("expected length 50, got %d", tbl.Length())
	}
}

func TestArrayKindLengthFloat(t *testing.T) {
	tbl := NewTable()
	for i := int64(1); i <= 30; i++ {
		tbl.RawSetInt(i, FloatValue(float64(i)*0.5))
	}
	if tbl.arrayKind != ArrayFloat {
		t.Errorf("expected ArrayFloat, got %d", tbl.arrayKind)
	}
	if tbl.Length() != 30 {
		t.Errorf("expected length 30, got %d", tbl.Length())
	}
}

func TestArrayKindSequentialAppend(t *testing.T) {
	// Test sequential append (key == len(array)) path
	tbl := NewTable()
	for i := int64(1); i <= 10; i++ {
		tbl.RawSetInt(i, IntValue(i*10))
	}
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	for i := int64(1); i <= 10; i++ {
		v := tbl.RawGetInt(i)
		if !v.IsInt() || v.Int() != i*10 {
			t.Errorf("expected %d at key %d, got %v", i*10, i, v)
		}
	}
}

func TestArrayKindOverwrite(t *testing.T) {
	// Overwriting same-type value should stay specialized
	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(10))
	tbl.RawSetInt(2, IntValue(20))
	tbl.RawSetInt(1, IntValue(100)) // overwrite
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt after overwrite, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsInt() || v.Int() != 100 {
		t.Errorf("expected 100, got %v", v)
	}
}

func TestArrayKindIterationInt(t *testing.T) {
	// Test Next() iteration with int array
	tbl := NewTable()
	for i := int64(1); i <= 5; i++ {
		tbl.RawSetInt(i, IntValue(i*10))
	}
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	count := 0
	key := NilValue()
	for {
		k, v, ok := tbl.Next(key)
		if !ok {
			break
		}
		count++
		idx := k.Int()
		if !v.IsInt() || v.Int() != idx*10 {
			t.Errorf("expected %d at key %d, got %v", idx*10, idx, v)
		}
		key = k
	}
	if count != 5 {
		t.Errorf("expected 5 iterations, got %d", count)
	}
}

func TestArrayKindIterationFloat(t *testing.T) {
	tbl := NewTable()
	for i := int64(1); i <= 3; i++ {
		tbl.RawSetInt(i, FloatValue(float64(i)*1.5))
	}
	if tbl.arrayKind != ArrayFloat {
		t.Errorf("expected ArrayFloat, got %d", tbl.arrayKind)
	}
	count := 0
	key := NilValue()
	for {
		k, v, ok := tbl.Next(key)
		if !ok {
			break
		}
		count++
		idx := k.Int()
		if !v.IsFloat() || v.Float() != float64(idx)*1.5 {
			t.Errorf("expected %f at key %d, got %v", float64(idx)*1.5, idx, v)
		}
		key = k
	}
	if count != 3 {
		t.Errorf("expected 3 iterations, got %d", count)
	}
}

func TestArrayKindMixedFromStart(t *testing.T) {
	// First write is a string → stays Mixed
	tbl := NewTable()
	tbl.RawSetInt(1, StringValue("hello"))
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed for string, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsString() || v.Str() != "hello" {
		t.Errorf("expected string hello, got %v", v)
	}
}

func TestArrayKindSparseExpansion(t *testing.T) {
	// Test sparse array expansion with typed arrays
	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(10))
	tbl.RawSetInt(5, IntValue(50)) // sparse - should trigger expansion
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt, got %d", tbl.arrayKind)
	}
	v1 := tbl.RawGetInt(1)
	if !v1.IsInt() || v1.Int() != 10 {
		t.Errorf("expected 10, got %v", v1)
	}
	v5 := tbl.RawGetInt(5)
	if !v5.IsInt() || v5.Int() != 50 {
		t.Errorf("expected 50, got %v", v5)
	}
	// Check gaps return 0 (int zero, which is falsy like nil for sieve)
	v2 := tbl.RawGetInt(2)
	if !v2.IsInt() || v2.Int() != 0 {
		t.Errorf("expected IntValue(0) for gap, got %v", v2)
	}
}

func TestArrayKindTableValue(t *testing.T) {
	// Table values should go to Mixed
	tbl := NewTable()
	inner := NewTable()
	tbl.RawSetInt(1, TableValue(inner))
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed for table value, got %d", tbl.arrayKind)
	}
}

func TestArrayKindNewTableStartsMixed(t *testing.T) {
	// A brand new table before any int-key write should be ArrayMixed (default)
	tbl := NewTable()
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed for new table, got %d", tbl.arrayKind)
	}
}

func TestArrayKindRawGetViaNonArrayPath(t *testing.T) {
	// RawGet with IntValue should route through typed array
	tbl := NewTable()
	tbl.RawSetInt(1, IntValue(42))
	v := tbl.RawGet(IntValue(1))
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42 via RawGet, got %v", v)
	}
}

func TestArrayKindAppend(t *testing.T) {
	// Test Append() which uses RawSet → RawSetInt
	tbl := NewTable()
	tbl.Append(IntValue(1))
	tbl.Append(IntValue(2))
	tbl.Append(IntValue(3))
	if tbl.arrayKind != ArrayInt {
		t.Errorf("expected ArrayInt after appends, got %d", tbl.arrayKind)
	}
	if tbl.Length() != 3 {
		t.Errorf("expected length 3, got %d", tbl.Length())
	}
	for i := int64(1); i <= 3; i++ {
		v := tbl.RawGetInt(i)
		if !v.IsInt() || v.Int() != i {
			t.Errorf("expected %d at key %d, got %v", i, i, v)
		}
	}
}

func TestArrayKindFloatOverwrite(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, FloatValue(1.0))
	tbl.RawSetInt(2, FloatValue(2.0))
	tbl.RawSetInt(1, FloatValue(99.9)) // overwrite
	if tbl.arrayKind != ArrayFloat {
		t.Errorf("expected ArrayFloat after float overwrite, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsFloat() || v.Float() != 99.9 {
		t.Errorf("expected 99.9, got %v", v)
	}
}

func TestArrayKindBoolPromotesToArrayBool(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(false))
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool for bool false, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	// BoolValue(false) round-trips correctly through ArrayBool
	if v.Truthy() {
		t.Errorf("expected falsy for bool false, got %v", v)
	}
	if !v.IsBool() {
		t.Errorf("expected bool type, got %v", v.Type())
	}
}

func TestArrayKindMatmulPattern(t *testing.T) {
	// Simulate matmul: fill rows with float arrays
	n := int64(10)
	tbl := NewTable()
	for i := int64(1); i <= n; i++ {
		row := NewTable()
		for j := int64(1); j <= n; j++ {
			row.RawSetInt(j, FloatValue(float64(i*n+j)))
		}
		if row.arrayKind != ArrayFloat {
			t.Errorf("row %d: expected ArrayFloat, got %d", i, row.arrayKind)
		}
		tbl.RawSetInt(i, TableValue(row))
	}
	// Outer table has table values → mixed
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed for outer table, got %d", tbl.arrayKind)
	}
	// Verify inner table reads
	row3 := tbl.RawGetInt(3).Table()
	v := row3.RawGetInt(5)
	if !v.IsFloat() || v.Float() != float64(3*n+5) {
		t.Errorf("expected %f, got %v", float64(3*n+5), v)
	}
}

func TestArrayKindBoolNilWrite(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(true))
	tbl.RawSetInt(2, BoolValue(false))
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool, got %d", tbl.arrayKind)
	}
	// Writing nil uses sentinel encoding (0 = nil), stays ArrayBool
	tbl.RawSetInt(1, NilValue())
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool after nil write (sentinel), got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsNil() {
		t.Errorf("expected nil after nil write, got %v", v)
	}
	// Key 2 should still be false
	v2 := tbl.RawGetInt(2)
	if !v2.IsBool() || v2.Bool() {
		t.Errorf("expected BoolValue(false) at key 2, got %v", v2)
	}
}

func TestArrayKindBoolDemotionOnTypeMismatch(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(true))
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool, got %d", tbl.arrayKind)
	}
	// Writing int demotes to mixed
	tbl.RawSetInt(2, IntValue(42))
	if tbl.arrayKind != ArrayMixed {
		t.Errorf("expected ArrayMixed after int write, got %d", tbl.arrayKind)
	}
	// Check both values survive demotion
	v1 := tbl.RawGetInt(1)
	if !v1.IsBool() || !v1.Bool() {
		t.Errorf("expected BoolValue(true) after demotion, got %v", v1)
	}
	v2 := tbl.RawGetInt(2)
	if !v2.IsInt() || v2.Int() != 42 {
		t.Errorf("expected IntValue(42) after demotion, got %v", v2)
	}
}

func TestArrayKindBoolLength(t *testing.T) {
	tbl := NewTable()
	for i := int64(1); i <= 50; i++ {
		tbl.RawSetInt(i, BoolValue(i%2 == 0))
	}
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool, got %d", tbl.arrayKind)
	}
	if tbl.Length() != 50 {
		t.Errorf("expected length 50, got %d", tbl.Length())
	}
}

func TestArrayKindBoolIteration(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(true))
	tbl.RawSetInt(2, BoolValue(false))
	tbl.RawSetInt(3, BoolValue(true))
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool, got %d", tbl.arrayKind)
	}
	expected := []bool{true, false, true}
	count := 0
	key := NilValue()
	for {
		k, v, ok := tbl.Next(key)
		if !ok {
			break
		}
		idx := k.Int()
		if !v.IsBool() || v.Bool() != expected[idx-1] {
			t.Errorf("expected %v at key %d, got %v", expected[idx-1], idx, v)
		}
		count++
		key = k
	}
	if count != 3 {
		t.Errorf("expected 3 iterations, got %d", count)
	}
}

func TestArrayKindBoolOverwrite(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(true))
	tbl.RawSetInt(2, BoolValue(true))
	tbl.RawSetInt(1, BoolValue(false)) // overwrite true → false
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool after overwrite, got %d", tbl.arrayKind)
	}
	v := tbl.RawGetInt(1)
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected BoolValue(false) after overwrite, got %v", v)
	}
}

func TestArrayKindBoolSparseExpansion(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetInt(1, BoolValue(true))
	tbl.RawSetInt(5, BoolValue(false)) // sparse
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool after sparse, got %d", tbl.arrayKind)
	}
	v1 := tbl.RawGetInt(1)
	if !v1.IsBool() || !v1.Bool() {
		t.Errorf("expected BoolValue(true), got %v", v1)
	}
	v5 := tbl.RawGetInt(5)
	if !v5.IsBool() || v5.Bool() {
		t.Errorf("expected BoolValue(false), got %v", v5)
	}
	// Gaps are nil (sentinel 0 = unset)
	v2 := tbl.RawGetInt(2)
	if !v2.IsNil() {
		t.Errorf("expected NilValue for gap, got %v", v2)
	}
}

func TestArrayKindBoolAppend(t *testing.T) {
	tbl := NewTable()
	tbl.Append(BoolValue(true))
	tbl.Append(BoolValue(false))
	tbl.Append(BoolValue(true))
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool after appends, got %d", tbl.arrayKind)
	}
	if tbl.Length() != 3 {
		t.Errorf("expected length 3, got %d", tbl.Length())
	}
}

func TestArrayKindBoolLargeSieve(t *testing.T) {
	// Simulate sieve with 10000 bools — verify no GC pointers in backing store
	n := int64(10000)
	tbl := NewTable()
	for i := int64(2); i <= n; i++ {
		tbl.RawSetInt(i, BoolValue(true))
	}
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool for large sieve, got %d", tbl.arrayKind)
	}
	// Sieve algorithm
	for i := int64(2); i*i <= n; i++ {
		v := tbl.RawGetInt(i)
		if v.IsBool() && v.Bool() {
			for j := i * i; j <= n; j += i {
				tbl.RawSetInt(j, BoolValue(false))
			}
		}
	}
	// Still ArrayBool — no demotion
	if tbl.arrayKind != ArrayBool {
		t.Errorf("expected ArrayBool after sieve, got %d", tbl.arrayKind)
	}
	// Count primes
	count := 0
	for i := int64(2); i <= n; i++ {
		v := tbl.RawGetInt(i)
		if v.IsBool() && v.Bool() {
			count++
		}
	}
	if count != 1229 { // number of primes up to 10000
		t.Errorf("expected 1229 primes, got %d", count)
	}
}
