package runtime

import (
	"fmt"
	"strings"
	"time"
)

// goTimeToTable converts a Go time.Time to a GScript time table.
func goTimeToTable(t time.Time) *Table {
	tbl := NewTable()
	tbl.RawSet(StringValue("year"), IntValue(int64(t.Year())))
	tbl.RawSet(StringValue("month"), IntValue(int64(t.Month())))
	tbl.RawSet(StringValue("day"), IntValue(int64(t.Day())))
	tbl.RawSet(StringValue("hour"), IntValue(int64(t.Hour())))
	tbl.RawSet(StringValue("min"), IntValue(int64(t.Minute())))
	tbl.RawSet(StringValue("sec"), IntValue(int64(t.Second())))
	tbl.RawSet(StringValue("nsec"), IntValue(int64(t.Nanosecond())))
	// unix timestamp as float64 with nanosecond precision
	unix := float64(t.Unix()) + float64(t.Nanosecond())/1e9
	tbl.RawSet(StringValue("unix"), FloatValue(unix))
	tbl.RawSet(StringValue("weekday"), IntValue(int64(t.Weekday())))
	tbl.RawSet(StringValue("yearday"), IntValue(int64(t.YearDay())))
	name, _ := t.Zone()
	tbl.RawSet(StringValue("tz"), StringValue(name))
	return tbl
}

// tableToGoTime converts a GScript time table Value to a Go time.Time.
func tableToGoTime(v Value) (time.Time, error) {
	if !v.IsTable() {
		return time.Time{}, fmt.Errorf("expected time table, got %s", v.TypeName())
	}
	tbl := v.Table()

	// If we have a unix field, use it for precise reconstruction
	unixVal := tbl.RawGet(StringValue("unix"))
	if unixVal.IsFloat() || unixVal.IsInt() {
		f := unixVal.Number()
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC(), nil
	}

	// Otherwise reconstruct from fields
	year := int(tbl.RawGet(StringValue("year")).Int())
	month := time.Month(tbl.RawGet(StringValue("month")).Int())
	day := int(tbl.RawGet(StringValue("day")).Int())
	hour := int(tbl.RawGet(StringValue("hour")).Int())
	min := int(tbl.RawGet(StringValue("min")).Int())
	sec := int(tbl.RawGet(StringValue("sec")).Int())
	nsec := int(tbl.RawGet(StringValue("nsec")).Int())

	return time.Date(year, month, day, hour, min, sec, nsec, time.UTC), nil
}

func timeSinceValue(v Value) (Value, error) {
	goTime, err := tableToGoTime(v)
	if err != nil {
		return NilValue(), fmt.Errorf("bad argument #1 to 'time.since': %v", err)
	}
	return FloatValue(time.Since(goTime).Seconds()), nil
}

// strftimeToGo converts strftime-style format specifiers to Go layout strings.
func strftimeToGo(layout string) string {
	// Check if it contains strftime-style % directives
	if !strings.Contains(layout, "%") {
		return layout // assume it's already a Go layout
	}

	replacements := []struct {
		from string
		to   string
	}{
		{"%Y", "2006"},
		{"%m", "01"},
		{"%d", "02"},
		{"%H", "15"},
		{"%M", "04"},
		{"%S", "05"},
		{"%A", "Monday"},
		{"%B", "January"},
		{"%Z", "MST"},
		{"%%", "%"},
	}

	result := layout
	for _, r := range replacements {
		result = strings.ReplaceAll(result, r.from, r.to)
	}
	return result
}

// buildTimeLib creates the "time" standard library table.
func buildTimeLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "time." + name,
			Fn:   fn,
		}))
	}
	setFast1 := func(name string, fn func([]Value) ([]Value, error), fast func([]Value) (Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name:  "time." + name,
			Fn:    fn,
			Fast1: fast,
		}))
	}
	setFastArg1 := func(name string, fn func([]Value) ([]Value, error), fast func(Value) (Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name:     "time." + name,
			Fn:       fn,
			FastArg1: fast,
		}))
	}

	// Constants
	t.RawSet(StringValue("SECOND"), FloatValue(1.0))
	t.RawSet(StringValue("MINUTE"), FloatValue(60.0))
	t.RawSet(StringValue("HOUR"), FloatValue(3600.0))
	t.RawSet(StringValue("DAY"), FloatValue(86400.0))

	// time.now() -> time table
	setFast1("now", func(args []Value) ([]Value, error) {
		return []Value{TableValue(goTimeToTable(time.Now()))}, nil
	}, func(args []Value) (Value, error) {
		return TableValue(goTimeToTable(time.Now())), nil
	})

	// time.sleep(seconds) -> nil
	set("sleep", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'time.sleep'")
		}
		secs := toFloat(args[0])
		time.Sleep(time.Duration(secs * float64(time.Second)))
		return nil, nil
	})

	// time.since(t) -> float seconds elapsed
	setFastArg1("since", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'time.since'")
		}
		v, err := timeSinceValue(args[0])
		return []Value{v}, err
	}, timeSinceValue)

	// time.unix(sec [, nsec]) -> time table
	set("unix", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'time.unix'")
		}
		sec := toInt(args[0])
		var nsec int64
		if len(args) >= 2 {
			nsec = toInt(args[1])
		}
		goTime := time.Unix(sec, nsec).UTC()
		return []Value{TableValue(goTimeToTable(goTime))}, nil
	})

	// time.format(t, layout) -> string
	set("format", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'time.format'")
		}
		goTime, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.format': %v", err)
		}
		layout := strftimeToGo(args[1].Str())
		return []Value{StringValue(goTime.Format(layout))}, nil
	})

	// time.parse(str, layout) -> time table, nil | nil, errMsg
	set("parse", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'time.parse'")
		}
		str := args[0].Str()
		layout := strftimeToGo(args[1].Str())
		goTime, err := time.Parse(layout, str)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		return []Value{TableValue(goTimeToTable(goTime)), NilValue()}, nil
	})

	// time.diff(t1, t2) -> float seconds (t2 - t1)
	set("diff", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'time.diff'")
		}
		t1, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.diff': %v", err)
		}
		t2, err := tableToGoTime(args[1])
		if err != nil {
			return nil, fmt.Errorf("bad argument #2 to 'time.diff': %v", err)
		}
		diff := t2.Sub(t1).Seconds()
		return []Value{FloatValue(diff)}, nil
	})

	// time.add(t, seconds) -> time table
	set("add", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'time.add'")
		}
		goTime, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.add': %v", err)
		}
		secs := toFloat(args[1])
		newTime := goTime.Add(time.Duration(secs * float64(time.Second)))
		return []Value{TableValue(goTimeToTable(newTime))}, nil
	})

	// time.date(year, month, day [, hour [, min [, sec]]]) -> time table
	set("date", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'time.date'")
		}
		year := int(toInt(args[0]))
		month := time.Month(toInt(args[1]))
		day := int(toInt(args[2]))
		hour, min, sec := 0, 0, 0
		if len(args) >= 4 {
			hour = int(toInt(args[3]))
		}
		if len(args) >= 5 {
			min = int(toInt(args[4]))
		}
		if len(args) >= 6 {
			sec = int(toInt(args[5]))
		}
		goTime := time.Date(year, month, day, hour, min, sec, 0, time.UTC)
		return []Value{TableValue(goTimeToTable(goTime))}, nil
	})

	// time.weekday(t) -> string (e.g. "Monday")
	set("weekday", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'time.weekday'")
		}
		goTime, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.weekday': %v", err)
		}
		return []Value{StringValue(goTime.Weekday().String())}, nil
	})

	// time.month(t) -> string (e.g. "January")
	set("month", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'time.month'")
		}
		goTime, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.month': %v", err)
		}
		return []Value{StringValue(goTime.Month().String())}, nil
	})

	// time.isBefore(t1, t2) -> bool
	set("isBefore", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'time.isBefore'")
		}
		t1, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.isBefore': %v", err)
		}
		t2, err := tableToGoTime(args[1])
		if err != nil {
			return nil, fmt.Errorf("bad argument #2 to 'time.isBefore': %v", err)
		}
		return []Value{BoolValue(t1.Before(t2))}, nil
	})

	// time.isAfter(t1, t2) -> bool
	set("isAfter", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'time.isAfter'")
		}
		t1, err := tableToGoTime(args[0])
		if err != nil {
			return nil, fmt.Errorf("bad argument #1 to 'time.isAfter': %v", err)
		}
		t2, err := tableToGoTime(args[1])
		if err != nil {
			return nil, fmt.Errorf("bad argument #2 to 'time.isAfter': %v", err)
		}
		return []Value{BoolValue(t1.After(t2))}, nil
	})

	return t
}
