package runtime

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// buildBytesLib creates the "bytes" standard library table.
func buildBytesLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "bytes." + name,
			Fn:   fn,
		}))
	}

	// createBufferTable wraps a bytes.Buffer in a GScript table with methods
	createBufferTable := func(buf *bytes.Buffer) *Table {
		bt := NewTable()

		setMethod := func(name string, fn func([]Value) ([]Value, error)) {
			bt.RawSet(StringValue(name), FunctionValue(&GoFunction{
				Name: "buffer." + name,
				Fn:   fn,
			}))
		}

		// buf.write(s) -- append string to buffer
		setMethod("write", func(args []Value) ([]Value, error) {
			if len(args) < 1 || !args[0].IsString() {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.write' (string expected)")
			}
			buf.WriteString(args[0].Str())
			return nil, nil
		})

		// buf.writeByte(n) -- append single byte (int 0-255)
		setMethod("writeByte", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeByte'")
			}
			n := toInt(args[0])
			if n < 0 || n > 255 {
				return nil, fmt.Errorf("buffer.writeByte: value %d out of range [0, 255]", n)
			}
			buf.WriteByte(byte(n))
			return nil, nil
		})

		// buf.writeInt8(n)
		setMethod("writeInt8", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeInt8'")
			}
			buf.WriteByte(byte(toInt(args[0])))
			return nil, nil
		})

		// buf.writeInt16(n)
		setMethod("writeInt16", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeInt16'")
			}
			b := make([]byte, 2)
			binary.LittleEndian.PutUint16(b, uint16(toInt(args[0])))
			buf.Write(b)
			return nil, nil
		})

		// buf.writeInt32(n)
		setMethod("writeInt32", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeInt32'")
			}
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, uint32(toInt(args[0])))
			buf.Write(b)
			return nil, nil
		})

		// buf.writeInt64(n)
		setMethod("writeInt64", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeInt64'")
			}
			b := make([]byte, 8)
			binary.LittleEndian.PutUint64(b, uint64(toInt(args[0])))
			buf.Write(b)
			return nil, nil
		})

		// buf.writeFloat32(n)
		setMethod("writeFloat32", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeFloat32'")
			}
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(float32(toFloat(args[0]))))
			buf.Write(b)
			return nil, nil
		})

		// buf.writeFloat64(n)
		setMethod("writeFloat64", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.writeFloat64'")
			}
			b := make([]byte, 8)
			binary.LittleEndian.PutUint64(b, math.Float64bits(toFloat(args[0])))
			buf.Write(b)
			return nil, nil
		})

		// buf.toString() -- get buffer content as string
		setMethod("toString", func(args []Value) ([]Value, error) {
			return []Value{StringValue(buf.String())}, nil
		})

		// buf.toHex() -- get buffer content as hex string
		setMethod("toHex", func(args []Value) ([]Value, error) {
			return []Value{StringValue(hex.EncodeToString(buf.Bytes()))}, nil
		})

		// buf.len() -- buffer length in bytes
		setMethod("len", func(args []Value) ([]Value, error) {
			return []Value{IntValue(int64(buf.Len()))}, nil
		})

		// buf.reset() -- clear buffer
		setMethod("reset", func(args []Value) ([]Value, error) {
			buf.Reset()
			return nil, nil
		})

		// buf.bytes() -- return table of byte values (ints)
		setMethod("bytes", func(args []Value) ([]Value, error) {
			data := buf.Bytes()
			tbl := NewTable()
			for i, b := range data {
				tbl.RawSet(IntValue(int64(i+1)), IntValue(int64(b)))
			}
			return []Value{TableValue(tbl)}, nil
		})

		// buf.readString(from, to) -- read substring from byte positions (1-based)
		setMethod("readString", func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("bad argument to 'buffer.readString'")
			}
			from := int(toInt(args[0])) - 1 // convert to 0-based
			to := int(toInt(args[1]))        // 1-based inclusive, so to becomes exclusive in Go
			data := buf.Bytes()
			if from < 0 {
				from = 0
			}
			if to > len(data) {
				to = len(data)
			}
			if from >= to {
				return []Value{StringValue("")}, nil
			}
			return []Value{StringValue(string(data[from:to]))}, nil
		})

		// buf.readByte(pos) -- read byte at position (1-based)
		setMethod("readByte", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'buffer.readByte'")
			}
			pos := int(toInt(args[0])) - 1 // convert to 0-based
			data := buf.Bytes()
			if pos < 0 || pos >= len(data) {
				return []Value{NilValue()}, nil
			}
			return []Value{IntValue(int64(data[pos]))}, nil
		})

		return bt
	}

	// bytes.new() -- create a new buffer
	set("new", func(args []Value) ([]Value, error) {
		buf := &bytes.Buffer{}
		return []Value{TableValue(createBufferTable(buf))}, nil
	})

	// bytes.fromString(s) -- create buffer from string
	set("fromString", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'bytes.fromString' (string expected)")
		}
		buf := bytes.NewBufferString(args[0].Str())
		return []Value{TableValue(createBufferTable(buf))}, nil
	})

	// bytes.fromHex(hexStr) -- create buffer from hex string
	set("fromHex", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'bytes.fromHex' (string expected)")
		}
		data, err := hex.DecodeString(args[0].Str())
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		buf := bytes.NewBuffer(data)
		return []Value{TableValue(createBufferTable(buf))}, nil
	})

	// bytes.toHex(s) -- convert string to hex
	set("toHex", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'bytes.toHex' (string expected)")
		}
		return []Value{StringValue(hex.EncodeToString([]byte(args[0].Str())))}, nil
	})

	// bytes.xor(s1, s2) -- XOR two byte strings of equal length
	set("xor", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'bytes.xor' (string expected)")
		}
		s1 := []byte(args[0].Str())
		s2 := []byte(args[1].Str())
		if len(s1) != len(s2) {
			return nil, fmt.Errorf("bytes.xor: strings must have equal length (got %d and %d)", len(s1), len(s2))
		}
		result := make([]byte, len(s1))
		for i := range s1 {
			result[i] = s1[i] ^ s2[i]
		}
		return []Value{StringValue(string(result))}, nil
	})

	// bytes.compare(s1, s2) -- -1, 0, 1 (lexicographic comparison)
	set("compare", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() || !args[1].IsString() {
			return nil, fmt.Errorf("bad argument to 'bytes.compare' (string expected)")
		}
		cmp := bytes.Compare([]byte(args[0].Str()), []byte(args[1].Str()))
		return []Value{IntValue(int64(cmp))}, nil
	})

	// bytes.repeat(s, n) -- repeat byte string n times
	set("repeat", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument to 'bytes.repeat'")
		}
		s := args[0].Str()
		n := int(toInt(args[1]))
		if n <= 0 {
			return []Value{StringValue("")}, nil
		}
		return []Value{StringValue(strings.Repeat(s, n))}, nil
	})

	// bytes.concat(...) -- concatenate multiple strings/buffers
	set("concat", func(args []Value) ([]Value, error) {
		var buf bytes.Buffer
		for _, a := range args {
			if a.IsString() {
				buf.WriteString(a.Str())
			} else if a.IsTable() {
				// Try to call toString on the table (buffer)
				toStr := a.Table().RawGet(StringValue("toString"))
				if toStr.IsFunction() {
					if gf := toStr.GoFunction(); gf != nil {
						results, err := gf.Fn(nil)
						if err != nil {
							return nil, err
						}
						if len(results) > 0 {
							buf.WriteString(results[0].String())
						}
					}
				}
			} else {
				buf.WriteString(a.String())
			}
		}
		return []Value{StringValue(buf.String())}, nil
	})

	return t
}
