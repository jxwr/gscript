// Structural variant: closure accumulators with non-unit integer delta and
// fractional fallback state.

func make_accumulator(start, delta) {
    value := start
    return func() {
        value = value + delta
        return value
    }
}

func run_int_accumulator() {
    acc := make_accumulator(7, 3)
    total := 0
    for i := 1; i <= 5000000; i++ {
        total = total + acc()
    }
    return total
}

func run_float_accumulator() {
    acc := make_accumulator(0.5, 1.25)
    total := 0.0
    for i := 1; i <= 2000000; i++ {
        total = total + acc()
    }
    return total
}

t0 := time.now()
int_result := run_int_accumulator()
t1 := time.since(t0)

t0 = time.now()
float_result := run_float_accumulator()
t2 := time.since(t0)

total := t1 + t2
print(string.format("int_delta accumulator: %.3fs (result=%d)", t1, int_result))
print(string.format("float_delta accumulator: %.3fs (result=%.3f)", t2, float_result))
print(string.format("Time: %.3fs", total))
