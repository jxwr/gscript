package runtime

import (
	"testing"
)

// randInterp creates an interpreter with the rand library registered.
func randInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "rand", buildRandLib())
}

// ==================================================================
// rand.seed tests
// ==================================================================

func TestRandSeed(t *testing.T) {
	// Seeding with same value should produce deterministic results
	// within the same rand lib instance
	interp := randInterp(t, `
		rand.seed(42)
		a := rand.int(1000)
		rand.seed(42)
		b := rand.int(1000)
		result := a == b
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("same seed should produce same results")
	}
}

// ==================================================================
// rand.int tests
// ==================================================================

func TestRandIntNoArgs(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.int()
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer, got %s", v.TypeName())
	}
	if v.Int() < 0 {
		t.Error("rand.int() should return non-negative value")
	}
}

func TestRandIntOneArg(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.int(10)
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer, got %s", v.TypeName())
	}
	if v.Int() < 0 || v.Int() >= 10 {
		t.Errorf("rand.int(10) should be in [0,10), got %d", v.Int())
	}
}

func TestRandIntTwoArgs(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.int(5, 10)
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer, got %s", v.TypeName())
	}
	if v.Int() < 5 || v.Int() > 10 {
		t.Errorf("rand.int(5,10) should be in [5,10], got %d", v.Int())
	}
}

func TestRandIntRange(t *testing.T) {
	// Generate many values and check they're in range
	interp := randInterp(t, `
		rand.seed(42)
		min_val := 1000
		max_val := 0
		for i := 0; i < 100; i++ {
			v := rand.int(1, 10)
			if v < min_val { min_val = v }
			if v > max_val { max_val = v }
		}
	`)
	minV := interp.GetGlobal("min_val")
	maxV := interp.GetGlobal("max_val")
	if minV.Int() < 1 {
		t.Errorf("min should be >= 1, got %d", minV.Int())
	}
	if maxV.Int() > 10 {
		t.Errorf("max should be <= 10, got %d", maxV.Int())
	}
}

// ==================================================================
// rand.float tests
// ==================================================================

func TestRandFloat(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.float()
	`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %s", v.TypeName())
	}
	if v.Float() < 0.0 || v.Float() >= 1.0 {
		t.Errorf("rand.float() should be in [0,1), got %f", v.Float())
	}
}

// ==================================================================
// rand.normal tests
// ==================================================================

func TestRandNormalDefaults(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.normal()
	`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %s", v.TypeName())
	}
}

func TestRandNormalWithParams(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.normal(100, 15)
	`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %s", v.TypeName())
	}
}

// ==================================================================
// rand.exp tests
// ==================================================================

func TestRandExp(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.exp()
	`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %s", v.TypeName())
	}
	if v.Float() < 0 {
		t.Error("exponential distribution should return non-negative values")
	}
}

func TestRandExpWithRate(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.exp(2.0)
	`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %s", v.TypeName())
	}
	if v.Float() < 0 {
		t.Error("exponential distribution should return non-negative values")
	}
}

// ==================================================================
// rand.bool tests
// ==================================================================

func TestRandBool(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.bool()
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() {
		t.Errorf("expected boolean, got %s", v.TypeName())
	}
}

func TestRandBoolDistribution(t *testing.T) {
	// Over many trials, both true and false should appear
	interp := randInterp(t, `
		rand.seed(42)
		trues := 0
		falses := 0
		for i := 0; i < 100; i++ {
			if rand.bool() {
				trues = trues + 1
			} else {
				falses = falses + 1
			}
		}
	`)
	trues := interp.GetGlobal("trues")
	falses := interp.GetGlobal("falses")
	if trues.Int() == 0 {
		t.Error("should have some true values")
	}
	if falses.Int() == 0 {
		t.Error("should have some false values")
	}
}

// ==================================================================
// rand.choice tests
// ==================================================================

func TestRandChoice(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		items := {10, 20, 30, 40, 50}
		result := rand.choice(items)
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer, got %s", v.TypeName())
	}
	valid := v.Int() == 10 || v.Int() == 20 || v.Int() == 30 || v.Int() == 40 || v.Int() == 50
	if !valid {
		t.Errorf("rand.choice returned value not in list: %d", v.Int())
	}
}

func TestRandChoiceEmpty(t *testing.T) {
	interp := randInterp(t, `
		items := {}
		result := rand.choice(items)
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("rand.choice on empty table should return nil, got %s", v.TypeName())
	}
}

// ==================================================================
// rand.shuffle tests
// ==================================================================

func TestRandShuffle(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		items := {1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		rand.shuffle(items)
		first := items[1]
		sum := 0
		for i := 1; i <= 10; i++ {
			sum = sum + items[i]
		}
	`)
	// Sum should be preserved (1+2+...+10 = 55)
	sumV := interp.GetGlobal("sum")
	if sumV.Int() != 55 {
		t.Errorf("shuffle should preserve all elements, sum expected 55, got %d", sumV.Int())
	}
}

func TestRandShuffleReturnsTable(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		items := {1, 2, 3}
		result := rand.shuffle(items)
		same := result == items
	`)
	v := interp.GetGlobal("same")
	// Should return the same table reference (in-place shuffle)
	// In GScript, table equality is reference equality
	// The shuffle function returns args[0] which is the same Value
	if !v.IsBool() || !v.Bool() {
		t.Error("shuffle should return the same table")
	}
}

// ==================================================================
// rand.sample tests
// ==================================================================

func TestRandSample(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		items := {10, 20, 30, 40, 50}
		result := rand.sample(items, 3)
		count := #result
	`)
	countV := interp.GetGlobal("count")
	if countV.Int() != 3 {
		t.Errorf("sample(items, 3) should return 3 elements, got %d", countV.Int())
	}
}

func TestRandSampleMoreThanLength(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		items := {1, 2, 3}
		result := rand.sample(items, 10)
		count := #result
	`)
	countV := interp.GetGlobal("count")
	if countV.Int() != 3 {
		t.Errorf("sample with n > length should clamp to length, got %d", countV.Int())
	}
}

// ==================================================================
// rand.uuid tests
// ==================================================================

func TestRandUUID(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.uuid()
	`)
	v := interp.GetGlobal("result")
	if !v.IsString() {
		t.Errorf("expected string, got %s", v.TypeName())
	}
	// UUID v4 format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx (36 chars)
	if len(v.Str()) != 36 {
		t.Errorf("UUID should be 36 chars, got %d", len(v.Str()))
	}
	// Check version nibble is '4'
	if v.Str()[14] != '4' {
		t.Errorf("UUID v4 should have '4' at position 14, got %c", v.Str()[14])
	}
}

func TestRandUUIDUnique(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		a := rand.uuid()
		b := rand.uuid()
		result := a != b
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Error("two UUIDs should be different")
	}
}

// ==================================================================
// rand.bytes tests
// ==================================================================

func TestRandBytes(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		result := rand.bytes(16)
		length := #result
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 16 {
		t.Errorf("expected 16 bytes, got %d", lenV.Int())
	}
}

func TestRandBytesZero(t *testing.T) {
	interp := randInterp(t, `
		result := rand.bytes(0)
		length := #result
	`)
	lenV := interp.GetGlobal("length")
	if lenV.Int() != 0 {
		t.Errorf("expected 0 bytes, got %d", lenV.Int())
	}
}

// ==================================================================
// rand.weighted tests
// ==================================================================

func TestRandWeighted(t *testing.T) {
	interp := randInterp(t, `
		rand.seed(42)
		items := {"rare", "common", "legendary"}
		weights := {1, 98, 1}
		result := rand.weighted(items, weights)
	`)
	v := interp.GetGlobal("result")
	if !v.IsString() {
		t.Errorf("expected string, got %s", v.TypeName())
	}
	// Result should be one of the items
	valid := v.Str() == "rare" || v.Str() == "common" || v.Str() == "legendary"
	if !valid {
		t.Errorf("unexpected value: %s", v.Str())
	}
}

func TestRandWeightedHeavyBias(t *testing.T) {
	// With extreme weights, the result should almost always be the heavy item
	interp := randInterp(t, `
		rand.seed(42)
		items := {"a", "b"}
		weights := {0, 100}
		count_b := 0
		for i := 0; i < 50; i++ {
			if rand.weighted(items, weights) == "b" {
				count_b = count_b + 1
			}
		}
	`)
	countB := interp.GetGlobal("count_b")
	if countB.Int() != 50 {
		t.Errorf("with weight {0, 100}, all picks should be 'b', got %d", countB.Int())
	}
}

// ==================================================================
// rand.timeSeed tests
// ==================================================================

func TestRandTimeSeed(t *testing.T) {
	interp := randInterp(t, `
		result := rand.timeSeed()
	`)
	v := interp.GetGlobal("result")
	if !v.IsInt() {
		t.Errorf("expected integer seed, got %s", v.TypeName())
	}
	if v.Int() <= 0 {
		t.Error("time seed should be positive")
	}
}
