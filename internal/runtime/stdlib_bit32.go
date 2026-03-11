package runtime

import (
	"fmt"
	"math/bits"
)

// buildBit32Lib creates the "bit32" standard library table.
func buildBit32Lib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "bit32." + name,
			Fn:   fn,
		}))
	}

	// bit32.band(...) → uint32
	set("band", func(args []Value) ([]Value, error) {
		if len(args) == 0 {
			return []Value{IntValue(int64(uint32(0xFFFFFFFF)))}, nil
		}
		result := uint32(toInt(args[0]))
		for _, arg := range args[1:] {
			result &= uint32(toInt(arg))
		}
		return []Value{IntValue(int64(result))}, nil
	})

	// bit32.bor(...) → uint32
	set("bor", func(args []Value) ([]Value, error) {
		if len(args) == 0 {
			return []Value{IntValue(0)}, nil
		}
		result := uint32(toInt(args[0]))
		for _, arg := range args[1:] {
			result |= uint32(toInt(arg))
		}
		return []Value{IntValue(int64(result))}, nil
	})

	// bit32.bxor(...) → uint32
	set("bxor", func(args []Value) ([]Value, error) {
		if len(args) == 0 {
			return []Value{IntValue(0)}, nil
		}
		result := uint32(toInt(args[0]))
		for _, arg := range args[1:] {
			result ^= uint32(toInt(arg))
		}
		return []Value{IntValue(int64(result))}, nil
	})

	// bit32.bnot(n) → uint32
	set("bnot", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'bit32.bnot'")
		}
		n := uint32(toInt(args[0]))
		return []Value{IntValue(int64(^n))}, nil
	})

	// bit32.lshift(n, disp) → uint32
	set("lshift", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.lshift'")
		}
		n := uint32(toInt(args[0]))
		disp := toInt(args[1])
		if disp < 0 {
			// Negative displacement means right shift
			return []Value{IntValue(int64(n >> uint(-disp)))}, nil
		}
		if disp >= 32 {
			return []Value{IntValue(0)}, nil
		}
		return []Value{IntValue(int64(n << uint(disp)))}, nil
	})

	// bit32.rshift(n, disp) → uint32
	set("rshift", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.rshift'")
		}
		n := uint32(toInt(args[0]))
		disp := toInt(args[1])
		if disp < 0 {
			return []Value{IntValue(int64(n << uint(-disp)))}, nil
		}
		if disp >= 32 {
			return []Value{IntValue(0)}, nil
		}
		return []Value{IntValue(int64(n >> uint(disp)))}, nil
	})

	// bit32.arshift(n, disp) → int32 (arithmetic right shift, sign-extending)
	set("arshift", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.arshift'")
		}
		n := int32(uint32(toInt(args[0])))
		disp := toInt(args[1])
		if disp < 0 {
			return []Value{IntValue(int64(uint32(n) << uint(-disp)))}, nil
		}
		if disp >= 32 {
			if n < 0 {
				return []Value{IntValue(int64(int32(-1)))}, nil
			}
			return []Value{IntValue(0)}, nil
		}
		return []Value{IntValue(int64(n >> uint(disp)))}, nil
	})

	// bit32.test(n, pos) → bool
	set("test", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.test'")
		}
		n := uint32(toInt(args[0]))
		pos := uint(toInt(args[1]))
		return []Value{BoolValue((n & (1 << pos)) != 0)}, nil
	})

	// bit32.set(n, pos) → uint32
	set("set", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.set'")
		}
		n := uint32(toInt(args[0]))
		pos := uint(toInt(args[1]))
		return []Value{IntValue(int64(n | (1 << pos)))}, nil
	})

	// bit32.clear(n, pos) → uint32
	set("clear", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.clear'")
		}
		n := uint32(toInt(args[0]))
		pos := uint(toInt(args[1]))
		return []Value{IntValue(int64(n &^ (1 << pos)))}, nil
	})

	// bit32.toggle(n, pos) → uint32
	set("toggle", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'bit32.toggle'")
		}
		n := uint32(toInt(args[0]))
		pos := uint(toInt(args[1]))
		return []Value{IntValue(int64(n ^ (1 << pos)))}, nil
	})

	// bit32.extract(n, field, width) → uint32
	set("extract", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'bit32.extract'")
		}
		n := uint32(toInt(args[0]))
		field := uint(toInt(args[1]))
		width := uint(toInt(args[2]))
		mask := uint32((1 << width) - 1)
		return []Value{IntValue(int64((n >> field) & mask))}, nil
	})

	// bit32.replace(n, v, field, width) → uint32
	set("replace", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("bad argument to 'bit32.replace'")
		}
		n := uint32(toInt(args[0]))
		v := uint32(toInt(args[1]))
		field := uint(toInt(args[2]))
		width := uint(toInt(args[3]))
		mask := uint32((1 << width) - 1)
		// Clear the target bits, then set them
		n = (n &^ (mask << field)) | ((v & mask) << field)
		return []Value{IntValue(int64(n))}, nil
	})

	// bit32.countbits(n) → int (popcount)
	set("countbits", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'bit32.countbits'")
		}
		n := uint32(toInt(args[0]))
		return []Value{IntValue(int64(bits.OnesCount32(n)))}, nil
	})

	// bit32.highbit(n) → int (position of highest set bit, 0-based; -1 if n=0)
	set("highbit", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'bit32.highbit'")
		}
		n := uint32(toInt(args[0]))
		if n == 0 {
			return []Value{IntValue(-1)}, nil
		}
		return []Value{IntValue(int64(31 - bits.LeadingZeros32(n)))}, nil
	})

	// bit32.toHex(n [, digits]) → string
	set("toHex", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'bit32.toHex'")
		}
		n := uint32(toInt(args[0]))
		if len(args) >= 2 {
			digits := int(toInt(args[1]))
			return []Value{StringValue(fmt.Sprintf("0x%0*X", digits, n))}, nil
		}
		return []Value{StringValue(fmt.Sprintf("0x%X", n))}, nil
	})

	return t
}
