// Extended benchmark: log-line formatting, tokenization, and metric extraction.

func make_line(i) {
    status := 200
    if i % 17 == 0 {
        status = 500
    } elseif i % 11 == 0 {
        status = 404
    } elseif i % 5 == 0 {
        status = 302
    }
    route := string.format("/v1/items/%d/detail", i % 97)
    trace := string.format("trace%04d-%03d", i % 10000, (i * 13) % 997)
    return string.format("ts=%d|svc=api%d|route=%s|status=%d|bytes=%d|trace=%s", 1700000000 + i, i % 9, route, status, 400 + (i * 23) % 9000, trace)
}

func build_lines(n) {
    lines := {}
    for i := 1; i <= n; i++ {
        lines[i] = make_line(i)
    }
    return lines
}

func parse_lines(lines, n, passes) {
    checksum := 0
    for pass := 1; pass <= passes; pass++ {
        for i := 1; i <= n; i++ {
            parts := string.split(lines[i], "|")
            svc := string.sub(parts[2], 5)
            route := string.sub(parts[3], 7)
            status := tonumber(string.sub(parts[4], 8))
            bytes := tonumber(string.sub(parts[5], 7))
            trace := string.sub(parts[6], 7)

            class := math.floor(status / 100)
            shaped := string.format("%s:%d:%s:%d", svc, class, string.sub(route, 1, 9), bytes % 97)
            checksum = (checksum + #shaped + #trace + status + bytes % 4096 + pass) % 1000000007
        }
    }
    return checksum
}

N := 18000
PASSES := 8

t0 := time.now()
lines := build_lines(N)
checksum := parse_lines(lines, N, PASSES)
elapsed := time.since(t0)

print(string.format("log_tokenize_format lines=%d passes=%d", N, PASSES))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
