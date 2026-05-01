// Extended benchmark: coroutine producer/consumer pipeline with table payloads.

func make_producer(n) {
    return coroutine.create(func() {
        for i := 1; i <= n; i++ {
            kind := "view"
            if i % 13 == 0 {
                kind = "error"
            } elseif i % 5 == 0 {
                kind = "click"
            }
            event := {
                id: i,
                account: (i * 17) % 257,
                shard: (i * 7) % 16 + 1,
                kind: kind,
                value: (i * 29) % 1000
            }
            coroutine.yield(event)
        }
        return nil
    })
}

func consume(n) {
    co := make_producer(n)
    by_shard := {}
    for i := 1; i <= 16; i++ {
        by_shard[i] = {count: 0, value: 0, errors: 0}
    }

    checksum := 0
    for i := 1; i <= n; i++ {
        ok, event := coroutine.resume(co)
        if !ok {
            return checksum
        }
        agg := by_shard[event.shard]
        agg.count = agg.count + 1
        agg.value = agg.value + event.value
        if event.kind == "error" {
            agg.errors = agg.errors + 1
            checksum = (checksum + event.account * 5 + agg.errors) % 1000000007
        } else {
            checksum = (checksum + event.value + agg.count + #event.kind) % 1000000007
        }
    }

    for i := 1; i <= 16; i++ {
        agg := by_shard[i]
        checksum = (checksum + agg.count * 3 + agg.value + agg.errors * 101) % 1000000007
    }
    return checksum
}

N := 650000

t0 := time.now()
checksum := consume(N)
elapsed := time.since(t0)

print(string.format("producer_consumer_pipeline events=%d", N))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
