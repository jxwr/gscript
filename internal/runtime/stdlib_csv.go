package runtime

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// buildCSVLib creates the "csv" standard library table.
func buildCSVLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "csv." + name,
			Fn:   fn,
		}))
	}

	// csv.parse(str [, opts]) -- parse CSV string -> table of rows
	// opts: {sep=",", comment="#", trimSpace=true, lazyQuotes=false}
	set("parse", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'csv.parse' (string expected)")
		}
		r := csv.NewReader(strings.NewReader(args[0].Str()))
		r.FieldsPerRecord = -1 // variable number of fields

		if len(args) >= 2 && args[1].IsTable() {
			opts := args[1].Table()
			if v := opts.RawGet(StringValue("sep")); v.IsString() && len(v.Str()) > 0 {
				r.Comma = rune(v.Str()[0])
			}
			if v := opts.RawGet(StringValue("comment")); v.IsString() && len(v.Str()) > 0 {
				r.Comment = rune(v.Str()[0])
			}
			if v := opts.RawGet(StringValue("trimSpace")); v.IsBool() {
				r.TrimLeadingSpace = v.Bool()
			}
			if v := opts.RawGet(StringValue("lazyQuotes")); v.IsBool() {
				r.LazyQuotes = v.Bool()
			}
		}

		result := NewTable()
		rowIdx := int64(1)
		for {
			record, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("csv.parse: %v", err)
			}
			row := NewTable()
			for i, field := range record {
				row.RawSet(IntValue(int64(i+1)), StringValue(field))
			}
			result.RawSet(IntValue(rowIdx), TableValue(row))
			rowIdx++
		}
		return []Value{TableValue(result)}, nil
	})

	// csv.parseWithHeaders(str [, opts]) -- parse CSV, first row is headers
	set("parseWithHeaders", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return nil, fmt.Errorf("bad argument #1 to 'csv.parseWithHeaders' (string expected)")
		}
		r := csv.NewReader(strings.NewReader(args[0].Str()))
		r.FieldsPerRecord = -1

		if len(args) >= 2 && args[1].IsTable() {
			opts := args[1].Table()
			if v := opts.RawGet(StringValue("sep")); v.IsString() && len(v.Str()) > 0 {
				r.Comma = rune(v.Str()[0])
			}
			if v := opts.RawGet(StringValue("comment")); v.IsString() && len(v.Str()) > 0 {
				r.Comment = rune(v.Str()[0])
			}
			if v := opts.RawGet(StringValue("trimSpace")); v.IsBool() {
				r.TrimLeadingSpace = v.Bool()
			}
			if v := opts.RawGet(StringValue("lazyQuotes")); v.IsBool() {
				r.LazyQuotes = v.Bool()
			}
		}

		// Read header row
		headers, err := r.Read()
		if err != nil {
			if err == io.EOF {
				return []Value{TableValue(NewTable())}, nil
			}
			return nil, fmt.Errorf("csv.parseWithHeaders: %v", err)
		}

		result := NewTable()
		rowIdx := int64(1)
		for {
			record, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("csv.parseWithHeaders: %v", err)
			}
			row := NewTable()
			for i, field := range record {
				if i < len(headers) {
					row.RawSet(StringValue(headers[i]), StringValue(field))
				}
			}
			result.RawSet(IntValue(rowIdx), TableValue(row))
			rowIdx++
		}
		return []Value{TableValue(result)}, nil
	})

	// csv.encode(rows [, opts]) -- encode table of rows to CSV string
	// opts: {sep=","}
	set("encode", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'csv.encode' (table expected)")
		}
		rows := args[0].Table()
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)

		if len(args) >= 2 && args[1].IsTable() {
			opts := args[1].Table()
			if v := opts.RawGet(StringValue("sep")); v.IsString() && len(v.Str()) > 0 {
				w.Comma = rune(v.Str()[0])
			}
		}

		length := rows.Length()
		for i := int64(1); i <= int64(length); i++ {
			rowVal := rows.RawGet(IntValue(i))
			if !rowVal.IsTable() {
				continue
			}
			row := rowVal.Table()
			rowLen := row.Length()
			record := make([]string, rowLen)
			for j := int64(1); j <= int64(rowLen); j++ {
				record[j-1] = row.RawGet(IntValue(j)).String()
			}
			if err := w.Write(record); err != nil {
				return nil, fmt.Errorf("csv.encode: %v", err)
			}
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, fmt.Errorf("csv.encode: %v", err)
		}
		return []Value{StringValue(buf.String())}, nil
	})

	// csv.encodeWithHeaders(rows, headers [, opts]) -- encode with header row first
	set("encodeWithHeaders", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsTable() {
			return nil, fmt.Errorf("bad argument to 'csv.encodeWithHeaders'")
		}
		rows := args[0].Table()
		headersTbl := args[1].Table()
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)

		if len(args) >= 3 && args[2].IsTable() {
			opts := args[2].Table()
			if v := opts.RawGet(StringValue("sep")); v.IsString() && len(v.Str()) > 0 {
				w.Comma = rune(v.Str()[0])
			}
		}

		// Write header row
		headersLen := headersTbl.Length()
		headerNames := make([]string, headersLen)
		for i := int64(1); i <= int64(headersLen); i++ {
			headerNames[i-1] = headersTbl.RawGet(IntValue(i)).String()
		}
		if err := w.Write(headerNames); err != nil {
			return nil, fmt.Errorf("csv.encodeWithHeaders: %v", err)
		}

		// Write data rows
		length := rows.Length()
		for i := int64(1); i <= int64(length); i++ {
			rowVal := rows.RawGet(IntValue(i))
			if !rowVal.IsTable() {
				continue
			}
			row := rowVal.Table()
			record := make([]string, headersLen)
			for j, h := range headerNames {
				record[j] = row.RawGet(StringValue(h)).String()
			}
			if err := w.Write(record); err != nil {
				return nil, fmt.Errorf("csv.encodeWithHeaders: %v", err)
			}
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, fmt.Errorf("csv.encodeWithHeaders: %v", err)
		}
		return []Value{StringValue(buf.String())}, nil
	})

	return t
}
