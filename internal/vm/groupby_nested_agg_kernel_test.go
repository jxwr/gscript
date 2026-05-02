package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

func TestGroupByNestedAggKernelMatchesFallback(t *testing.T) {
	src := `
func make_event(i, regions, products, channels) {
    region := regions[(i * 3) % #regions + 1]
    product := products[(i * 5) % #products + 1]
    channel := channels[(i * 7) % #channels + 1]
    qty := (i * 17) % 9 + 1
    price := (i * 31) % 200 + 50
    return {region: region, product: product, channel: channel, qty: qty, revenue: qty * price, id: i}
}

func aggregate_fast(n, passes) {
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

func aggregate_slow(n, passes) {
    marker := 0
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
    return checksum + marker
}

fast_result := aggregate_fast(210, 3)
slow_result := aggregate_slow(210, 3)
`
	proto := compileGroupByNestedAggKernelTestProgram(t, src)
	if len(proto.Protos) != 3 {
		t.Fatalf("nested protos = %d, want 3", len(proto.Protos))
	}
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[1]), "groupby_nested_agg")
	rejectKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[2]), "groupby_nested_agg")

	globals := runtime.NewInterpreterGlobals()
	vm := New(globals)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute: %v", err)
	}
	fast := globals["fast_result"]
	slow := globals["slow_result"]
	if !fast.IsInt() || !slow.IsInt() || fast.Int() != slow.Int() {
		t.Fatalf("fast_result=%v slow_result=%v, want equal ints", fast, slow)
	}
	if proto.Protos[1].EnteredTier2 == 0 {
		t.Fatalf("groupby nested agg kernel did not mark fast proto entered")
	}
}

func compileGroupByNestedAggKernelTestProgram(t *testing.T, src string) *FuncProto {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return proto
}
