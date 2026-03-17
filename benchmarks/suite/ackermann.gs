// Benchmark: Ackermann Function
// Tests: deep recursion, function call overhead, integer comparison
// ack(3,12) = 32765, generates ~67 million calls

func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}

// Note: ack(3,n) requires deep recursion. ack(3,4)=125 fits in default stack.
REPS := 500
t0 := time.now()
result := 0
for r := 1; r <= REPS; r++ {
    result = ack(3, 4)
}
elapsed := time.since(t0)

print(string.format("ack(3,4) = %d (%d reps)", result, REPS))
print(string.format("Time: %.3fs", elapsed))
