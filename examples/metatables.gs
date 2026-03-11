// metatables.gs - Metatable showcase in GScript
// Demonstrates: Vector2D, Matrix, Observable, Proxy, Read-only, Default value tables

print("=== Metatable Showcase ===")
print()

// -------------------------------------------------------
// 1. Vector2D with __add, __sub, __mul, __eq, __tostring
// -------------------------------------------------------
print("--- Vector2D ---")

VecMeta := {}

func Vec2(x, y) {
    v := {x: x, y: y}
    setmetatable(v, VecMeta)
    return v
}

VecMeta.__add = func(a, b) {
    return Vec2(a.x + b.x, a.y + b.y)
}

VecMeta.__sub = func(a, b) {
    return Vec2(a.x - b.x, a.y - b.y)
}

VecMeta.__mul = func(a, b) {
    // scalar * vector or vector * scalar
    if type(a) == "table" && type(b) != "table" {
        return Vec2(a.x * b, a.y * b)
    }
    if type(a) != "table" && type(b) == "table" {
        return Vec2(a * b.x, a * b.y)
    }
    // dot product for vector * vector
    return a.x * b.x + a.y * b.y
}

VecMeta.__eq = func(a, b) {
    return a.x == b.x && a.y == b.y
}

VecMeta.__unm = func(v) {
    return Vec2(-v.x, -v.y)
}

// Helper to display a vector
func vecStr(v) {
    return "Vec2(" .. tostring(v.x) .. ", " .. tostring(v.y) .. ")"
}

// Vector utility functions
func vecLen(v) {
    return math.sqrt(v.x * v.x + v.y * v.y)
}

func vecNormalize(v) {
    len := vecLen(v)
    if len == 0 { return Vec2(0, 0) }
    return Vec2(v.x / len, v.y / len)
}

a := Vec2(3, 4)
b := Vec2(1, 2)

print("  a =", vecStr(a))
print("  b =", vecStr(b))
print("  a + b =", vecStr(a + b))
print("  a - b =", vecStr(a - b))
print("  a * 2 =", vecStr(a * 2))
print("  -a =", vecStr(-a))
print("  a dot b =", a * b)
print("  |a| =", string.format("%.4f", vecLen(a)))
print("  normalize(a) =", vecStr(vecNormalize(a)))
print("  a == Vec2(3,4):", a == Vec2(3, 4))
print("  a == b:", a == b)
print()

// -------------------------------------------------------
// 2. Matrix multiplication with __mul
// -------------------------------------------------------
print("--- Matrix ---")

MatMeta := {}

func Mat(rows) {
    m := {rows: rows, nrows: #rows, ncols: #rows[1]}
    setmetatable(m, MatMeta)
    return m
}

MatMeta.__mul = func(a, b) {
    if a.ncols != b.nrows {
        error("matrix dimension mismatch")
    }
    result := {}
    for i := 1; i <= a.nrows; i++ {
        result[i] = {}
        for j := 1; j <= b.ncols; j++ {
            sum := 0
            for k := 1; k <= a.ncols; k++ {
                sum = sum + a.rows[i][k] * b.rows[k][j]
            }
            result[i][j] = sum
        }
    }
    return Mat(result)
}

// Helper to display a matrix
func matStr(m) {
    lines := {}
    for i := 1; i <= m.nrows; i++ {
        parts := {}
        for j := 1; j <= m.ncols; j++ {
            table.insert(parts, string.format("%6.1f", m.rows[i][j]))
        }
        table.insert(lines, "    [" .. table.concat(parts, ", ") .. "]")
    }
    return table.concat(lines, "\n")
}

m1 := Mat({{1, 2}, {3, 4}})
m2 := Mat({{5, 6}, {7, 8}})
m3 := m1 * m2

print("  A =")
print(matStr(m1))
print("  B =")
print(matStr(m2))
print("  A * B =")
print(matStr(m3))

// Identity matrix test
identity := Mat({{1, 0}, {0, 1}})
m4 := m1 * identity
print("  A * I =")
print(matStr(m4))
print()

// -------------------------------------------------------
// 3. Observable/reactive values with __newindex
// -------------------------------------------------------
print("--- Observable Table ---")

func observable(initial) {
    data := {}
    listeners := {}

    // Copy initial values
    if initial != nil {
        for k, v := range initial {
            data[k] = v
        }
    }

    proxy := {}
    meta := {
        __index: func(t, key) {
            return data[key]
        },
        __newindex: func(t, key, value) {
            oldVal := data[key]
            data[key] = value
            // Notify listeners
            if listeners[key] != nil {
                for i := 1; i <= #listeners[key]; i++ {
                    listeners[key][i](key, oldVal, value)
                }
            }
            // Notify wildcard listeners
            if listeners["*"] != nil {
                for i := 1; i <= #listeners["*"]; i++ {
                    listeners["*"][i](key, oldVal, value)
                }
            }
        }
    }
    setmetatable(proxy, meta)

    // Attach a watcher
    proxy._watch = func(key, callback) {
        if listeners[key] == nil {
            listeners[key] = {}
        }
        table.insert(listeners[key], callback)
    }

    return proxy
}

config := observable({theme: "dark", fontSize: 14})

// Watch specific key
config._watch("theme", func(key, old, new) {
    print(string.format("    Theme changed: %s -> %s", tostring(old), tostring(new)))
})

// Watch all changes
config._watch("*", func(key, old, new) {
    print(string.format("    [any] %s: %s -> %s", key, tostring(old), tostring(new)))
})

print("  Setting theme to 'light':")
config.theme = "light"
print("  Setting fontSize to 16:")
config.fontSize = 16
print("  Current theme:", config.theme)
print("  Current fontSize:", config.fontSize)
print()

// -------------------------------------------------------
// 4. Proxy table with __index/__newindex
// -------------------------------------------------------
print("--- Proxy Table ---")

// A proxy that logs all reads and writes
func loggedProxy(name, target) {
    proxy := {}
    log := {}

    meta := {
        __index: func(t, key) {
            table.insert(log, "GET " .. name .. "." .. tostring(key))
            return target[key]
        },
        __newindex: func(t, key, value) {
            table.insert(log, "SET " .. name .. "." .. tostring(key) .. " = " .. tostring(value))
            target[key] = value
        }
    }
    rawset(proxy, "_getLog", func() { return log })
    setmetatable(proxy, meta)

    return proxy
}

data := {x: 10, y: 20}
p := loggedProxy("data", data)

// Perform operations through proxy
val := p.x
p.z = 30
val2 := p.y
p.x = 100

print("  Proxy operation log:")
proxyLog := p._getLog()
for i := 1; i <= #proxyLog; i++ {
    print("    " .. proxyLog[i])
}
print("  Underlying data.x:", data.x)
print("  Underlying data.z:", data.z)
print()

// -------------------------------------------------------
// 5. Read-only table
// -------------------------------------------------------
print("--- Read-Only Table ---")

func readOnly(tbl) {
    proxy := {}
    meta := {
        __index: tbl,
        __newindex: func(t, key, value) {
            error("attempt to modify read-only table (key: " .. tostring(key) .. ")")
        }
    }
    setmetatable(proxy, meta)
    return proxy
}

constants := readOnly({
    PI: 3.14159265,
    E: 2.71828182,
    TAU: 6.28318530
})

print("  PI:", constants.PI)
print("  E:", constants.E)
print("  TAU:", constants.TAU)

// Try to modify - should fail
ok, err := pcall(func() {
    constants.PI = 3
})
print("  Modify PI - ok:", ok, "err:", err)
print("  PI is still:", constants.PI)
print()

// -------------------------------------------------------
// 6. Default value table
// -------------------------------------------------------
print("--- Default Value Table ---")

func withDefaults(defaults) {
    tbl := {}
    meta := {
        __index: func(t, key) {
            return defaults[key]
        }
    }
    setmetatable(tbl, meta)
    return tbl
}

// Config with defaults
defaultConfig := {
    host: "localhost",
    port: 8080,
    debug: false,
    maxRetries: 3,
    timeout: 30
}

config2 := withDefaults(defaultConfig)
config2.host = "example.com"
config2.debug = true

print("  Config with defaults:")
print("    host:", config2.host)           // overridden
print("    port:", config2.port)           // default
print("    debug:", config2.debug)         // overridden
print("    maxRetries:", config2.maxRetries) // default
print("    timeout:", config2.timeout)     // default
print()

// Counter table - defaults to 0 for any key
func counterTable() {
    counts := {}
    proxy := {}
    meta := {
        __index: func(t, key) {
            v := counts[key]
            if v == nil { return 0 }
            return v
        },
        __newindex: func(t, key, value) {
            counts[key] = value
        }
    }
    setmetatable(proxy, meta)
    return proxy
}

counter := counterTable()
print("  Counter table (defaults to 0):")
print("    apple (before):", counter.apple)
counter.apple = counter.apple + 1
counter.apple = counter.apple + 1
counter.banana = counter.banana + 3
print("    apple (after 2 increments):", counter.apple)
print("    banana (after +3):", counter.banana)
print("    cherry (never set):", counter.cherry)
print()

// -------------------------------------------------------
// 7. Callable table with __call
// -------------------------------------------------------
print("--- Callable Table ---")

func adder(initial) {
    t := {value: initial}
    meta := {
        __call: func(self, n) {
            self.value = self.value + n
            return self.value
        }
    }
    setmetatable(t, meta)
    return t
}

func adderStr(a) {
    return "Adder(" .. tostring(a.value) .. ")"
}

acc := adder(0)
print("  acc =", adderStr(acc))
print("  acc(10) =", acc(10))
print("  acc(20) =", acc(20))
print("  acc(5) =", acc(5))
print("  acc =", adderStr(acc))
print()

print("=== Done ===")
