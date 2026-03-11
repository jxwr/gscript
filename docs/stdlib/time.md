# time

The `time` library provides date/time operations.

## Time Tables

A "time table" is a GScript table with the following fields:

| Field     | Type   | Description                                    |
|-----------|--------|------------------------------------------------|
| `year`    | int    | Year (e.g. 2024)                               |
| `month`   | int    | Month (1-12)                                   |
| `day`     | int    | Day of month (1-31)                            |
| `hour`    | int    | Hour (0-23)                                    |
| `min`     | int    | Minute (0-59)                                  |
| `sec`     | int    | Second (0-59)                                  |
| `nsec`    | int    | Nanosecond (0-999999999)                       |
| `unix`    | float  | Unix timestamp with nanosecond precision       |
| `weekday` | int    | Day of week (0=Sunday, 6=Saturday)             |
| `yearday` | int    | Day of year (1-366)                            |
| `tz`      | string | Timezone name (e.g. "UTC", "EST")              |

## Functions

### time.now()

Returns a time table representing the current local time.

```
t := time.now()
print(t.year, t.month, t.day)
```

### time.sleep(seconds)

Blocks execution for the given number of seconds. Accepts floats for sub-second precision.

```
time.sleep(0.5)   // sleep 500ms
time.sleep(2)     // sleep 2 seconds
```

### time.since(t)

Returns the number of seconds (float) elapsed since time table `t`.

```
start := time.now()
// ... do work ...
elapsed := time.since(start)
print("Took " .. elapsed .. " seconds")
```

### time.unix(sec [, nsec])

Creates a time table from a Unix timestamp. Optionally accepts nanoseconds.

```
epoch := time.unix(0)           // 1970-01-01 00:00:00 UTC
t := time.unix(1700000000)      // specific timestamp
t2 := time.unix(0, 500000000)   // with nanoseconds
```

### time.format(t, layout)

Formats a time table as a string. Supports both strftime-style and Go layout strings.

Strftime directives:
- `%Y` - 4-digit year
- `%m` - 2-digit month (01-12)
- `%d` - 2-digit day (01-31)
- `%H` - 2-digit hour (00-23)
- `%M` - 2-digit minute (00-59)
- `%S` - 2-digit second (00-59)
- `%A` - full weekday name (e.g. "Monday")
- `%B` - full month name (e.g. "January")
- `%Z` - timezone abbreviation
- `%%` - literal percent sign

```
t := time.now()
s := time.format(t, "%Y-%m-%d %H:%M:%S")
// Go layout also works:
s2 := time.format(t, "2006-01-02 15:04:05")
```

### time.parse(str, layout)

Parses a date string into a time table. Returns `timeTable, nil` on success or `nil, errorMessage` on failure.

```
t, err := time.parse("2024-03-15 10:30:00", "%Y-%m-%d %H:%M:%S")
if err != nil {
    print("Parse error: " .. err)
}
```

### time.diff(t1, t2)

Returns the difference in seconds (float) between two time tables: `t2 - t1`. Can be negative.

```
t1 := time.unix(1000)
t2 := time.unix(1060)
d := time.diff(t1, t2)  // 60.0
```

### time.add(t, seconds)

Returns a new time table by adding `seconds` to time table `t`.

```
t := time.now()
later := time.add(t, 3600)  // one hour later
```

### time.date(year, month, day [, hour [, min [, sec]]])

Creates a time table from date components. Hour, minute, and second default to 0.

```
t := time.date(2024, 12, 25)           // Christmas, midnight
t2 := time.date(2024, 12, 25, 10, 30)  // 10:30 AM
```

### time.weekday(t)

Returns the weekday name as a string (e.g. "Monday").

```
t := time.now()
print(time.weekday(t))  // e.g. "Wednesday"
```

### time.month(t)

Returns the month name as a string (e.g. "January").

```
t := time.now()
print(time.month(t))  // e.g. "March"
```

### time.isBefore(t1, t2)

Returns `true` if `t1` is before `t2`.

```
t1 := time.unix(1000)
t2 := time.unix(2000)
print(time.isBefore(t1, t2))  // true
```

### time.isAfter(t1, t2)

Returns `true` if `t1` is after `t2`.

```
t1 := time.unix(2000)
t2 := time.unix(1000)
print(time.isAfter(t1, t2))  // true
```

## Constants

| Constant      | Value    | Description          |
|---------------|----------|----------------------|
| `time.SECOND` | 1.0      | One second           |
| `time.MINUTE` | 60.0     | Sixty seconds        |
| `time.HOUR`   | 3600.0   | 3600 seconds         |
| `time.DAY`    | 86400.0  | 86400 seconds        |

```
// Sleep for 2 minutes
time.sleep(2 * time.MINUTE)

// Add one day
tomorrow := time.add(time.now(), time.DAY)
```
