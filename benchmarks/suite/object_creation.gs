// Benchmark: Object Creation
// Tests: table object allocation with fields, method_dispatch-like pattern
// Stresses table creation, field initialization, and GC pressure

func new_vec3(x, y, z) {
    return {x: x, y: y, z: z}
}

func vec3_add(a, b) {
    return new_vec3(a.x + b.x, a.y + b.y, a.z + b.z)
}

func vec3_scale(v, s) {
    return new_vec3(v.x * s, v.y * s, v.z * s)
}

func vec3_length_sq(v) {
    return v.x * v.x + v.y * v.y + v.z * v.z
}

// Test 1: Create many objects, accumulate results
func create_and_sum(n) {
    total := new_vec3(0.0, 0.0, 0.0)
    for i := 1; i <= n; i++ {
        v := new_vec3(1.0 * i, 2.0 * i, 3.0 * i)
        total = vec3_add(total, v)
    }
    return vec3_length_sq(total)
}

// Test 2: Object pool pattern -- create, transform, discard
func transform_chain(n) {
    v := new_vec3(1.0, 0.0, 0.0)
    for i := 1; i <= n; i++ {
        offset := new_vec3(0.001, 0.002, 0.003)
        v = vec3_add(v, offset)
        v = vec3_scale(v, 0.9999)
    }
    return vec3_length_sq(v)
}

// Test 3: Create objects with many fields
func complex_objects(n) {
    total := 0.0
    for i := 1; i <= n; i++ {
        obj := {
            name: "particle",
            id: i,
            x: 1.0 * i,
            y: 2.0 * i,
            z: 3.0 * i,
            vx: 0.1,
            vy: 0.2,
            vz: 0.3,
            mass: 1.0,
            active: true
        }
        total = total + obj.x + obj.y + obj.z + obj.mass
    }
    return total
}

N1 := 200000
N2 := 500000
N3 := 100000

t0 := time.now()
r1 := create_and_sum(N1)
t1 := time.since(t0)

t0 = time.now()
r2 := transform_chain(N2)
t2 := time.since(t0)

t0 = time.now()
r3 := complex_objects(N3)
t3 := time.since(t0)

total := t1 + t2 + t3

print(string.format("create_and_sum(%d):   %.3fs (len_sq=%.2f)", N1, t1, r1))
print(string.format("transform_chain(%d):  %.3fs (len_sq=%.6f)", N2, t2, r2))
print(string.format("complex_objects(%d):  %.3fs (total=%.2f)", N3, t3, r3))
print(string.format("Time: %.3fs", total))
