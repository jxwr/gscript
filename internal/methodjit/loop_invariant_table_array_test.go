//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestCollectStableGlobalArrayElementFactsAfterFixedTableRewrite(t *testing.T) {
	src := `
bodies := {
    {name: "sun", x: 0.0, y: 0.0, z: 0.0, vx: 0.0, vy: 0.0, vz: 0.0, mass: 39.0},
    {name: "jupiter", x: 4.0, y: -1.0, z: -0.1, vx: 0.6, vy: 2.8, vz: -0.02, mass: 0.03},
}

func advance(dt) {
    bi := bodies[1]
    bj := bodies[2]
    dx := bi.x - bj.x
    dy := bi.y - bj.y
    dz := bi.z - bj.z
    dist := math.sqrt(dx * dx + dy * dy + dz * dz)
    mag := dt / dist
    bi.vx = bi.vx - dx * bj.mass * mag
    bj.vx = bj.vx + dx * bi.mass * mag
}
`
	proto := compileTop(t, src)
	fn, _, err := RunTier2Pipeline(BuildGraph(proto), &Tier2PipelineOpts{
		InlineGlobals:   buildProtoInlineGlobals(proto),
		ProtocolGlobals: buildProtoStableGlobals(proto),
		InlineMaxSize:   1,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(<main>): %v", err)
	}
	facts := collectStableGlobalArrayElementFacts(fn)
	fact, ok := facts["bodies"]
	if !ok {
		t.Fatalf("missing bodies global array fact: %#v", facts)
	}
	if fact.ShapeID == 0 || len(fact.FieldNames) != 8 {
		t.Fatalf("bad bodies array element fact: %#v", fact)
	}
	if fact.FieldTypes["x"] != TypeFloat || fact.FieldTypes["mass"] != TypeFloat {
		t.Fatalf("missing float field types in bodies fact: %#v", fact.FieldTypes)
	}
}

func TestTier2NestedTableArrayLoadDoesNotReuseClobberedOuterInvariant(t *testing.T) {
	src := `
func make_event(i, regions, products, channels) {
    region := regions[(i * 3) % #regions + 1]
    product := products[(i * 5) % #products + 1]
    channel := channels[(i * 7) % #channels + 1]
    qty := (i * 17) % 9 + 1
    price := (i * 31) % 200 + 50
    return {region: region, product: product, channel: channel, qty: qty, revenue: qty * price, id: i}
}

func aggregate(n, passes) {
    regions := {"north", "south", "east", "west", "central"}
    products := {"core", "edge", "pro", "max", "lite", "mini", "plus"}
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

result := aggregate(2000, 2)
`
	proto := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT execute: %v", err)
	}
	if got := v.GetGlobal("result"); !got.IsInt() || got.Int() != 174325581 {
		t.Fatalf("result=%v, want 174325581", got)
	}
}

func TestNativeLoopDriverDoesNotReplayActorSideEffectsAfterDeopt(t *testing.T) {
	src := `
func step_worker(a, tick) {
    a.x = a.x + a.vx
    a.y = a.y + a.vy
    a.load = (a.load + tick + a.id) % 97
    if a.load > 70 {
        a.vx = a.vx * 0.99
        a.vy = a.vy * 1.01
    } else {
        a.vx = a.vx + 0.001
    }
    return a.x * 3.0 + a.y * 2.0 + a.load
}

func step_io(a, tick) {
    a.queue = (a.queue + tick + a.id) % 211
    a.bytes = a.bytes + a.queue * 13 + tick
    if a.queue % 5 == 0 {
        a.state = "flush"
    } else {
        a.state = "read"
    }
    return a.bytes % 100000 + #a.state
}

func step_cache(a, tick) {
    slot := (tick + a.id) % 8 + 1
    old := a.lines[slot]
    next := (old * 33 + tick + a.id) % 1009
    a.lines[slot] = next
    a.hits = a.hits + (next % 7)
    return a.hits + next
}

func new_actor(i) {
    mod := i % 3
    if mod == 0 {
        return {id: i, kind: "worker", x: i * 0.25, y: i * 0.5, vx: 0.15, vy: 0.25, load: i % 91, step: step_worker}
    } elseif mod == 1 {
        return {id: i, kind: "io", queue: i % 113, bytes: i * 17, state: "read", step: step_io}
    }
    return {id: i, kind: "cache", lines: {1, 3, 5, 7, 11, 13, 17, 19}, hits: i % 31, step: step_cache}
}

func build_actors(n) {
    actors := {}
    for i := 1; i <= n; i++ {
        actors[i] = new_actor(i)
    }
    return actors
}

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

result := run_world(build_actors(3), 3, 1)
`
	proto := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT execute: %v", err)
	}
	if got := v.GetGlobal("result"); !got.IsInt() || got.Int() != 332 {
		t.Fatalf("result=%v, want 332", got)
	}
	if !containsString(tm.Tier2Entered(), "run_world") {
		t.Fatalf("expected run_world to enter Tier2 without replaying actor mutations, entered=%v failed=%v", tm.Tier2Entered(), tm.Tier2Failed())
	}
}
