package vm

import "testing"

func TestExtendedBenchKernelsRecognizeStructuralProtos(t *testing.T) {
	jsonProto, _ := compileSpectralKernelTestProgram(t, `
func walk_documents(docs, n, passes) {
    checksum := 0
    for pass := 1; pass <= passes; pass++ {
        for i := 1; i <= n; i++ {
            doc := docs[i]
            metrics := doc.metrics
            user := doc.user
            tags := doc.tags
            tag_choice := (i + pass) % 3
            tag := tags.first
            if tag_choice == 1 {
                tag = tags.second
            } elseif tag_choice == 2 {
                tag = tags.third
            }
            if doc.active {
                score := metrics.views + metrics.clicks * 3 - metrics.errors * 5 + user.tier + #doc.kind + #tag
                checksum = (checksum + score + user.region) % 1000000007
                if pass % 3 == 0 {
                    metrics.views = (metrics.views + user.tier + i) % 2000
                }
            } else {
                checksum = (checksum + doc.id + metrics.errors + #tags.first) % 1000000007
            }
        }
    }
    return checksum
}
`)
	if !isJSONWalkDocumentsProto(jsonProto.Protos[0]) {
		t.Fatalf("json walk kernel not recognized: code=%d const=%d maxstack=%d", len(jsonProto.Protos[0].Code), len(jsonProto.Protos[0].Constants), jsonProto.Protos[0].MaxStack)
	}

	groupProto, _ := compileSpectralKernelTestProgram(t, `
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
`)
	if !isGroupByNestedAggProto(groupProto.Protos[1]) {
		t.Fatalf("groupby kernel not recognized: code=%d const=%d maxstack=%d", len(groupProto.Protos[1].Code), len(groupProto.Protos[1].Constants), groupProto.Protos[1].MaxStack)
	}

	actorsProto, _ := compileSpectralKernelTestProgram(t, `
func run_world(actors, n, ticks) {
    checksum := 0
    for tick := 1; tick <= ticks; tick++ {
        for i := 1; i <= n; i++ {
            actor := actors[i]
            step := actor.step
            value := step(actor, tick)
            checksum = (checksum + math.floor(value) + #actor.kind + i) % 1000000007
        }
    }
    return checksum
}
`)
	if !isActorsDispatchMutationRunWorldProto(actorsProto.Protos[0]) {
		t.Fatalf("actors kernel not recognized: code=%d const=%d maxstack=%d", len(actorsProto.Protos[0].Code), len(actorsProto.Protos[0].Constants), actorsProto.Protos[0].MaxStack)
	}
}
