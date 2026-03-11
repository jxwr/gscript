package runtime

import (
	"math"
	"testing"
	"time"
)

// timeInterp creates an interpreter with the time library manually registered.
func timeInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "time", buildTimeLib())
}

// ==================================================================
// Time library tests
// ==================================================================

func TestTimeNow(t *testing.T) {
	before := time.Now()
	interp := timeInterp(t, `
		t := time.now()
		year := t.year
		month := t.month
		day := t.day
		hour := t.hour
		unixTs := t.unix
		tz := t.tz
	`)
	after := time.Now()

	year := interp.GetGlobal("year")
	if !year.IsInt() || year.Int() < 2020 {
		t.Errorf("expected year >= 2020, got %v", year)
	}

	month := interp.GetGlobal("month")
	if !month.IsInt() || month.Int() < 1 || month.Int() > 12 {
		t.Errorf("expected month 1-12, got %v", month)
	}

	day := interp.GetGlobal("day")
	if !day.IsInt() || day.Int() < 1 || day.Int() > 31 {
		t.Errorf("expected day 1-31, got %v", day)
	}

	hour := interp.GetGlobal("hour")
	if !hour.IsInt() || hour.Int() < 0 || hour.Int() > 23 {
		t.Errorf("expected hour 0-23, got %v", hour)
	}

	unixTs := interp.GetGlobal("unixTs")
	if !unixTs.IsFloat() {
		t.Errorf("expected unix to be float, got %s", unixTs.TypeName())
	}
	unixF := unixTs.Number()
	if unixF < float64(before.Unix()) || unixF > float64(after.Unix())+1 {
		t.Errorf("expected unix timestamp between %d and %d, got %f", before.Unix(), after.Unix(), unixF)
	}

	tz := interp.GetGlobal("tz")
	if !tz.IsString() || tz.Str() == "" {
		t.Errorf("expected non-empty tz string, got %v", tz)
	}
}

func TestTimeNowFields(t *testing.T) {
	interp := timeInterp(t, `
		t := time.now()
		weekday := t.weekday
		yearday := t.yearday
		min := t.min
		sec := t.sec
		nsec := t.nsec
	`)

	weekday := interp.GetGlobal("weekday")
	if !weekday.IsInt() || weekday.Int() < 0 || weekday.Int() > 6 {
		t.Errorf("expected weekday 0-6, got %v", weekday)
	}

	yearday := interp.GetGlobal("yearday")
	if !yearday.IsInt() || yearday.Int() < 1 || yearday.Int() > 366 {
		t.Errorf("expected yearday 1-366, got %v", yearday)
	}

	min := interp.GetGlobal("min")
	if !min.IsInt() || min.Int() < 0 || min.Int() > 59 {
		t.Errorf("expected min 0-59, got %v", min)
	}

	sec := interp.GetGlobal("sec")
	if !sec.IsInt() || sec.Int() < 0 || sec.Int() > 59 {
		t.Errorf("expected sec 0-59, got %v", sec)
	}

	nsec := interp.GetGlobal("nsec")
	if !nsec.IsInt() || nsec.Int() < 0 {
		t.Errorf("expected nsec >= 0, got %v", nsec)
	}
}

func TestTimeSleep(t *testing.T) {
	before := time.Now()
	_ = timeInterp(t, `
		time.sleep(0.05)
	`)
	elapsed := time.Since(before)
	if elapsed < 40*time.Millisecond {
		t.Errorf("sleep(0.05) should take at least ~50ms, got %v", elapsed)
	}
}

func TestTimeSince(t *testing.T) {
	interp := timeInterp(t, `
		t := time.now()
		time.sleep(0.05)
		elapsed := time.since(t)
	`)
	elapsed := interp.GetGlobal("elapsed")
	if !elapsed.IsFloat() || elapsed.Number() < 0.03 {
		t.Errorf("expected elapsed >= 0.03 seconds, got %v", elapsed)
	}
}

func TestTimeUnix(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		year := t.year
		month := t.month
		day := t.day
		hour := t.hour
		min := t.min
		sec := t.sec
	`)

	// Unix epoch in UTC is 1970-01-01 00:00:00
	year := interp.GetGlobal("year")
	if year.Int() != 1970 {
		t.Errorf("expected year=1970, got %v", year)
	}
	month := interp.GetGlobal("month")
	if month.Int() != 1 {
		t.Errorf("expected month=1, got %v", month)
	}
	day := interp.GetGlobal("day")
	if day.Int() != 1 {
		t.Errorf("expected day=1, got %v", day)
	}
}

func TestTimeUnixWithNsec(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0, 500000000)
		nsec := t.nsec
	`)
	nsec := interp.GetGlobal("nsec")
	if nsec.Int() != 500000000 {
		t.Errorf("expected nsec=500000000, got %v", nsec)
	}
}

func TestTimeFormat(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		result := time.format(t, "%Y-%m-%d %H:%M:%S")
	`)
	result := interp.GetGlobal("result")
	if result.Str() != "1970-01-01 00:00:00" {
		t.Errorf("expected '1970-01-01 00:00:00', got '%s'", result.Str())
	}
}

func TestTimeFormatGoLayout(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		result := time.format(t, "2006-01-02 15:04:05")
	`)
	result := interp.GetGlobal("result")
	if result.Str() != "1970-01-01 00:00:00" {
		t.Errorf("expected '1970-01-01 00:00:00', got '%s'", result.Str())
	}
}

func TestTimeFormatWeekday(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		result := time.format(t, "%A")
	`)
	result := interp.GetGlobal("result")
	if result.Str() != "Thursday" {
		t.Errorf("expected 'Thursday', got '%s'", result.Str())
	}
}

func TestTimeFormatMonth(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		result := time.format(t, "%B")
	`)
	result := interp.GetGlobal("result")
	if result.Str() != "January" {
		t.Errorf("expected 'January', got '%s'", result.Str())
	}
}

func TestTimeFormatYear(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		result := time.format(t, "%Y")
	`)
	result := interp.GetGlobal("result")
	if result.Str() != "1970" {
		t.Errorf("expected '1970', got '%s'", result.Str())
	}
}

func TestTimeParse(t *testing.T) {
	interp := timeInterp(t, `
		t, err := time.parse("2023-06-15 10:30:00", "%Y-%m-%d %H:%M:%S")
		year := t.year
		month := t.month
		day := t.day
		hour := t.hour
		min := t.min
		sec := t.sec
	`)
	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("year").Int() != 2023 {
		t.Errorf("expected year=2023, got %v", interp.GetGlobal("year"))
	}
	if interp.GetGlobal("month").Int() != 6 {
		t.Errorf("expected month=6, got %v", interp.GetGlobal("month"))
	}
	if interp.GetGlobal("day").Int() != 15 {
		t.Errorf("expected day=15, got %v", interp.GetGlobal("day"))
	}
	if interp.GetGlobal("hour").Int() != 10 {
		t.Errorf("expected hour=10, got %v", interp.GetGlobal("hour"))
	}
	if interp.GetGlobal("min").Int() != 30 {
		t.Errorf("expected min=30, got %v", interp.GetGlobal("min"))
	}
	if interp.GetGlobal("sec").Int() != 0 {
		t.Errorf("expected sec=0, got %v", interp.GetGlobal("sec"))
	}
}

func TestTimeParseError(t *testing.T) {
	interp := timeInterp(t, `
		t, err := time.parse("not-a-date", "%Y-%m-%d")
	`)
	if !interp.GetGlobal("t").IsNil() {
		t.Errorf("expected nil time on parse error, got %v", interp.GetGlobal("t"))
	}
	if interp.GetGlobal("err").IsNil() {
		t.Errorf("expected non-nil error string on parse error")
	}
}

func TestTimeDiff(t *testing.T) {
	interp := timeInterp(t, `
		t1 := time.unix(1000)
		t2 := time.unix(1060)
		d := time.diff(t1, t2)
	`)
	d := interp.GetGlobal("d")
	if math.Abs(d.Number()-60.0) > 0.001 {
		t.Errorf("expected diff=60, got %v", d)
	}
}

func TestTimeDiffNegative(t *testing.T) {
	interp := timeInterp(t, `
		t1 := time.unix(1060)
		t2 := time.unix(1000)
		d := time.diff(t1, t2)
	`)
	d := interp.GetGlobal("d")
	if math.Abs(d.Number()-(-60.0)) > 0.001 {
		t.Errorf("expected diff=-60, got %v", d)
	}
}

func TestTimeAdd(t *testing.T) {
	interp := timeInterp(t, `
		t1 := time.unix(1000)
		t2 := time.add(t1, 60)
		unix2 := t2.unix
	`)
	unix2 := interp.GetGlobal("unix2")
	if math.Abs(unix2.Number()-1060.0) > 0.001 {
		t.Errorf("expected unix=1060, got %v", unix2)
	}
}

func TestTimeDate(t *testing.T) {
	interp := timeInterp(t, `
		t := time.date(2023, 6, 15)
		year := t.year
		month := t.month
		day := t.day
		hour := t.hour
		min := t.min
		sec := t.sec
	`)
	if interp.GetGlobal("year").Int() != 2023 {
		t.Errorf("expected year=2023, got %v", interp.GetGlobal("year"))
	}
	if interp.GetGlobal("month").Int() != 6 {
		t.Errorf("expected month=6, got %v", interp.GetGlobal("month"))
	}
	if interp.GetGlobal("day").Int() != 15 {
		t.Errorf("expected day=15, got %v", interp.GetGlobal("day"))
	}
	if interp.GetGlobal("hour").Int() != 0 {
		t.Errorf("expected hour=0, got %v", interp.GetGlobal("hour"))
	}
	if interp.GetGlobal("min").Int() != 0 {
		t.Errorf("expected min=0, got %v", interp.GetGlobal("min"))
	}
	if interp.GetGlobal("sec").Int() != 0 {
		t.Errorf("expected sec=0, got %v", interp.GetGlobal("sec"))
	}
}

func TestTimeDateWithTime(t *testing.T) {
	interp := timeInterp(t, `
		t := time.date(2023, 6, 15, 10, 30, 45)
		hour := t.hour
		min := t.min
		sec := t.sec
	`)
	if interp.GetGlobal("hour").Int() != 10 {
		t.Errorf("expected hour=10, got %v", interp.GetGlobal("hour"))
	}
	if interp.GetGlobal("min").Int() != 30 {
		t.Errorf("expected min=30, got %v", interp.GetGlobal("min"))
	}
	if interp.GetGlobal("sec").Int() != 45 {
		t.Errorf("expected sec=45, got %v", interp.GetGlobal("sec"))
	}
}

func TestTimeWeekday(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		wd := time.weekday(t)
	`)
	wd := interp.GetGlobal("wd")
	if wd.Str() != "Thursday" {
		t.Errorf("expected 'Thursday' (Unix epoch), got '%s'", wd.Str())
	}
}

func TestTimeMonth(t *testing.T) {
	interp := timeInterp(t, `
		t := time.unix(0)
		m := time.month(t)
	`)
	m := interp.GetGlobal("m")
	if m.Str() != "January" {
		t.Errorf("expected 'January' (Unix epoch), got '%s'", m.Str())
	}
}

func TestTimeIsBefore(t *testing.T) {
	interp := timeInterp(t, `
		t1 := time.unix(1000)
		t2 := time.unix(2000)
		result := time.isBefore(t1, t2)
		result2 := time.isBefore(t2, t1)
	`)
	if !interp.GetGlobal("result").Bool() {
		t.Errorf("expected isBefore(1000, 2000) = true")
	}
	if interp.GetGlobal("result2").Truthy() {
		t.Errorf("expected isBefore(2000, 1000) = false")
	}
}

func TestTimeIsAfter(t *testing.T) {
	interp := timeInterp(t, `
		t1 := time.unix(2000)
		t2 := time.unix(1000)
		result := time.isAfter(t1, t2)
		result2 := time.isAfter(t2, t1)
	`)
	if !interp.GetGlobal("result").Bool() {
		t.Errorf("expected isAfter(2000, 1000) = true")
	}
	if interp.GetGlobal("result2").Truthy() {
		t.Errorf("expected isAfter(1000, 2000) = false")
	}
}

func TestTimeConstants(t *testing.T) {
	interp := timeInterp(t, `
		s := time.SECOND
		m := time.MINUTE
		h := time.HOUR
		d := time.DAY
	`)
	if interp.GetGlobal("s").Number() != 1.0 {
		t.Errorf("expected SECOND=1, got %v", interp.GetGlobal("s"))
	}
	if interp.GetGlobal("m").Number() != 60.0 {
		t.Errorf("expected MINUTE=60, got %v", interp.GetGlobal("m"))
	}
	if interp.GetGlobal("h").Number() != 3600.0 {
		t.Errorf("expected HOUR=3600, got %v", interp.GetGlobal("h"))
	}
	if interp.GetGlobal("d").Number() != 86400.0 {
		t.Errorf("expected DAY=86400, got %v", interp.GetGlobal("d"))
	}
}

func TestTimeRegistered(t *testing.T) {
	interp := timeInterp(t, `
		result := type(time)
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "table" {
		t.Errorf("expected time to be 'table', got %s", v.Str())
	}
}

func TestTimeFunctions(t *testing.T) {
	interp := timeInterp(t, `
		a := type(time.now)
		b := type(time.sleep)
		c := type(time.since)
		d := type(time.unix)
		e := type(time.format)
		f := type(time.parse)
		g := type(time.diff)
		h := type(time.add)
		i := type(time.date)
		j := type(time.weekday)
		k := type(time.month)
		l := type(time.isBefore)
		m := type(time.isAfter)
	`)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"} {
		v := interp.GetGlobal(name)
		if v.Str() != "function" {
			t.Errorf("expected time function '%s' to be 'function', got '%s'", name, v.Str())
		}
	}
}
