// Extended benchmark: nested group-by aggregation with dynamic string keys.

func make_event(i, regions, products, channels) {
    region := regions[(i * 3) % #regions + 1]
    product := products[(i * 5) % #products + 1]
    channel := channels[(i * 7) % #channels + 1]
    qty := (i * 17) % 9 + 1
    price := (i * 31) % 200 + 50
    return {region: region, product: product, channel: channel, qty: qty, revenue: qty * price, id: i}
}

func aggregate(n, passes) {
    regions := {"na", "eu", "apac", "latam", "mea"}
    products := {"core", "pro", "team", "edge", "ai", "data", "ops"}
    channels := {"web", "partner", "sales", "market"}
    totals := {}
    checksum := 0

    for pass := 1; pass <= passes; pass++ {
        for i := 1; i <= n; i++ {
            ev := make_event(i + pass * 97, regions, products, channels)
            by_region := totals[ev.region]
            if by_region == nil {
                by_region = {}
                totals[ev.region] = by_region
            }
            agg := by_region[ev.product]
            if agg == nil {
                agg = {count: 0, qty: 0, revenue: 0, web: 0, partner: 0, sales: 0, market: 0}
                by_region[ev.product] = agg
            }

            agg.count = agg.count + 1
            agg.qty = agg.qty + ev.qty
            agg.revenue = agg.revenue + ev.revenue
            if ev.channel == "web" {
                agg.web = agg.web + ev.qty
            } elseif ev.channel == "partner" {
                agg.partner = agg.partner + ev.qty
            } elseif ev.channel == "sales" {
                agg.sales = agg.sales + ev.qty
            } else {
                agg.market = agg.market + ev.qty
            }

            checksum = (checksum + agg.count * 3 + agg.qty + agg.revenue + #ev.region + #ev.product) % 1000000007
        }
    }

    for r := 1; r <= #regions; r++ {
        by_region := totals[regions[r]]
        for p := 1; p <= #products; p++ {
            agg := by_region[products[p]]
            checksum = (checksum + agg.count + agg.qty * 7 + agg.web * 11 + agg.market * 13) % 1000000007
        }
    }
    return checksum
}

N := 60000
PASSES := 8

t0 := time.now()
checksum := aggregate(N, PASSES)
elapsed := time.since(t0)

print(string.format("groupby_nested_agg events=%d passes=%d", N, PASSES))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
