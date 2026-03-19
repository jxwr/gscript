// Benchmark: OOP Method Dispatch via Tables
// Tests: table-as-object pattern, field access for methods, "this" binding

func new_point(x, y) {
    p := {x: x, y: y}
    return p
}

func point_distance(p1, p2) {
    dx := p1.x - p2.x
    dy := p1.y - p2.y
    return math.sqrt(dx * dx + dy * dy)
}

func point_translate(p, dx, dy) {
    return new_point(p.x + dx, p.y + dy)
}

func point_scale(p, factor) {
    return new_point(p.x * factor, p.y * factor)
}

// Test: create points, compute distances, transform
func test_points(n) {
    total_dist := 0.0
    p := new_point(0.0, 0.0)
    for i := 1; i <= n; i++ {
        q := new_point(1.0 * i, 2.0 * i)
        total_dist = total_dist + point_distance(p, q)
        p = point_translate(p, 0.1, 0.2)
        p = point_scale(p, 0.999)
    }
    return total_dist
}

N := 100000

t0 := time.now()
result := test_points(N)
elapsed := time.since(t0)

print(string.format("method_dispatch(%d): dist=%.4f", N, result))
print(string.format("Time: %.3fs", elapsed))
