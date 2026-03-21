package runtime

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

// buildRandLib creates the "rand" standard library table.
// Provides a dedicated random number generation library with seeded generators,
// distributions, shuffle, choice, and other utilities.
// Inspired by Odin's math/rand package.
func buildRandLib() *Table {
	t := NewTable()

	// Local random source, seeded with current time by default
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "rand." + name,
			Fn:   fn,
		}))
	}

	// rand.seed(n) - seed the random source
	set("seed", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'rand.seed' (number expected)")
		}
		seed := toInt(args[0])
		rng.Seed(seed)
		return nil, nil
	})

	// rand.int([min, max]) - random integer
	// No args: random int48 (fits in NaN-boxed integer)
	// One arg: [0, max)
	// Two args: [min, max]
	set("int", func(args []Value) ([]Value, error) {
		if len(args) == 0 {
			// Mask to 47 bits to guarantee NaN-boxed int (not float promotion).
			return []Value{IntValue(rng.Int63() & 0x7FFFFFFFFFFF)}, nil
		}
		if len(args) == 1 {
			max := toInt(args[0])
			if max <= 0 {
				return nil, fmt.Errorf("bad argument #1 to 'rand.int' (positive number expected)")
			}
			return []Value{IntValue(rng.Int63n(max))}, nil
		}
		min := toInt(args[0])
		max := toInt(args[1])
		if min > max {
			return nil, fmt.Errorf("bad argument to 'rand.int' (min > max)")
		}
		return []Value{IntValue(min + rng.Int63n(max-min+1))}, nil
	})

	// rand.float() - random float64 in [0.0, 1.0)
	set("float", func(args []Value) ([]Value, error) {
		return []Value{FloatValue(rng.Float64())}, nil
	})

	// rand.normal([mean, stddev]) - sample from normal distribution
	// Defaults: mean=0, stddev=1
	set("normal", func(args []Value) ([]Value, error) {
		mean := 0.0
		stddev := 1.0
		if len(args) >= 1 {
			mean = toFloat(args[0])
		}
		if len(args) >= 2 {
			stddev = toFloat(args[1])
			if stddev < 0 {
				return nil, fmt.Errorf("bad argument #2 to 'rand.normal' (non-negative stddev expected)")
			}
		}
		return []Value{FloatValue(rng.NormFloat64()*stddev + mean)}, nil
	})

	// rand.exp([rate]) - sample from exponential distribution
	// Default rate=1
	set("exp", func(args []Value) ([]Value, error) {
		rate := 1.0
		if len(args) >= 1 {
			rate = toFloat(args[0])
			if rate <= 0 {
				return nil, fmt.Errorf("bad argument #1 to 'rand.exp' (positive rate expected)")
			}
		}
		return []Value{FloatValue(rng.ExpFloat64() / rate)}, nil
	})

	// rand.bool() - random boolean (50/50)
	set("bool", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rng.Intn(2) == 1)}, nil
	})

	// rand.choice(table) - pick a random element from an array-like table
	set("choice", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'rand.choice' (table expected)")
		}
		if !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'rand.choice' (table expected, got %s)", args[0].TypeName())
		}
		tbl := args[0].Table()
		length := tbl.Length()
		if length == 0 {
			return []Value{NilValue()}, nil
		}
		idx := rng.Intn(length) + 1 // 1-based indexing
		return []Value{tbl.RawGet(IntValue(int64(idx)))}, nil
	})

	// rand.shuffle(table) - shuffle an array-like table in place, returns the table
	set("shuffle", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'rand.shuffle' (table expected)")
		}
		if !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'rand.shuffle' (table expected, got %s)", args[0].TypeName())
		}
		tbl := args[0].Table()
		length := tbl.Length()
		// Fisher-Yates shuffle
		for i := length; i > 1; i-- {
			j := rng.Intn(i) + 1 // j in [1, i]
			vi := tbl.RawGet(IntValue(int64(i)))
			vj := tbl.RawGet(IntValue(int64(j)))
			tbl.RawSet(IntValue(int64(i)), vj)
			tbl.RawSet(IntValue(int64(j)), vi)
		}
		return []Value{args[0]}, nil
	})

	// rand.sample(table, n) - sample n unique elements from an array-like table
	set("sample", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad arguments to 'rand.sample' (table and count expected)")
		}
		if !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'rand.sample' (table expected, got %s)", args[0].TypeName())
		}
		tbl := args[0].Table()
		n := int(toInt(args[1]))
		length := tbl.Length()
		if n < 0 {
			return nil, fmt.Errorf("bad argument #2 to 'rand.sample' (non-negative count expected)")
		}
		if n > length {
			n = length
		}
		// Copy indices
		indices := make([]int, length)
		for i := range indices {
			indices[i] = i + 1
		}
		// Partial Fisher-Yates
		result := NewTable()
		for i := 0; i < n; i++ {
			j := i + rng.Intn(length-i)
			indices[i], indices[j] = indices[j], indices[i]
			result.RawSet(IntValue(int64(i+1)), tbl.RawGet(IntValue(int64(indices[i]))))
		}
		return []Value{TableValue(result)}, nil
	})

	// rand.uuid() - generate a random UUID v4 string
	set("uuid", func(args []Value) ([]Value, error) {
		var uuid [16]byte
		for i := range uuid {
			uuid[i] = byte(rng.Intn(256))
		}
		uuid[6] = (uuid[6] & 0x0f) | 0x40 // Version 4
		uuid[8] = (uuid[8] & 0x3f) | 0x80 // Variant
		s := fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
			uuid[0], uuid[1], uuid[2], uuid[3],
			uuid[4], uuid[5],
			uuid[6], uuid[7],
			uuid[8], uuid[9],
			uuid[10], uuid[11], uuid[12], uuid[13], uuid[14], uuid[15])
		return []Value{StringValue(s)}, nil
	})

	// rand.bytes(n) - generate n random bytes as a string
	set("bytes", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'rand.bytes' (number expected)")
		}
		n := int(toInt(args[0]))
		if n < 0 {
			return nil, fmt.Errorf("bad argument #1 to 'rand.bytes' (non-negative number expected)")
		}
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		return []Value{StringValue(string(buf))}, nil
	})

	// rand.weighted(table, weights) - pick a random element using weighted probabilities
	// weights is a table of positive numbers corresponding to each element in table
	set("weighted", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad arguments to 'rand.weighted' (table and weights expected)")
		}
		if !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'rand.weighted' (table expected)")
		}
		if !args[1].IsTable() {
			return nil, fmt.Errorf("bad argument #2 to 'rand.weighted' (table expected)")
		}
		items := args[0].Table()
		weights := args[1].Table()
		length := items.Length()
		if length == 0 {
			return []Value{NilValue()}, nil
		}

		// Calculate total weight
		total := 0.0
		for i := 1; i <= length; i++ {
			w := weights.RawGet(IntValue(int64(i)))
			if w.IsNil() {
				return nil, fmt.Errorf("rand.weighted: weight at index %d is nil", i)
			}
			wf := toFloat(w)
			if wf < 0 {
				return nil, fmt.Errorf("rand.weighted: negative weight at index %d", i)
			}
			total += wf
		}
		if total == 0 || math.IsInf(total, 0) {
			return nil, fmt.Errorf("rand.weighted: invalid total weight")
		}

		// Pick random point
		r := rng.Float64() * total
		cumulative := 0.0
		for i := 1; i <= length; i++ {
			w := weights.RawGet(IntValue(int64(i)))
			cumulative += toFloat(w)
			if r < cumulative {
				return []Value{items.RawGet(IntValue(int64(i)))}, nil
			}
		}
		// Fallback to last element (floating point edge case)
		return []Value{items.RawGet(IntValue(int64(length)))}, nil
	})

	// rand.timeSeed() - seed with current time (convenience)
	set("timeSeed", func(args []Value) ([]Value, error) {
		seed := time.Now().UnixNano()
		rng.Seed(seed)
		// Mask seed to 47 bits to guarantee NaN-boxed int (not float promotion).
		return []Value{IntValue(seed & 0x7FFFFFFFFFFF)}, nil
	})

	return t
}
