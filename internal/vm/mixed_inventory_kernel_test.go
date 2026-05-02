package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

func TestMixedInventoryOrdersKernelMatchesFallback(t *testing.T) {
	src := `
func build_inventory(n) {
    items := {}
    by_sku := {}
    for i := 1; i <= n; i++ {
        sku := string.format("SKU%05d", i)
        item := {
            sku: sku,
            stock: 80 + (i * 19) % 220,
            reserved: i % 13,
            price: 5.0 + (i % 97) * 0.75,
            reorder: 30 + i % 40,
            sold: 0
        }
        items[i] = item
        by_sku[sku] = item
    }
    return {items: items, by_sku: by_sku}
}

func run_orders_fast(inv, item_count, orders) {
    checksum := 0
    reports := {}
    for i := 1; i <= orders; i++ {
        idx := (i * 37) % item_count + 1
        sku := string.format("SKU%05d", idx)
        item := inv.by_sku[sku]
        qty := (i * 11) % 6 + 1
        available := item.stock - item.reserved

        if available >= qty {
            item.stock = item.stock - qty
            item.sold = item.sold + qty
            checksum = (checksum + math.floor(item.price * qty * 100.0) + item.sold + idx) % 1000000007
        } else {
            item.reserved = math.floor(item.reserved * 0.5)
            checksum = (checksum + idx + item.stock + item.reserved) % 1000000007
        }

        if item.stock < item.reorder {
            item.stock = item.stock + 120 + (idx % 30)
            checksum = (checksum + item.stock * 3) % 1000000007
        }

        if i % 2500 == 0 {
            report := string.format("%s:%d:%d:%.2f", item.sku, item.stock, item.sold, item.price)
            reports[#reports + 1] = report
            checksum = (checksum + #report * 17) % 1000000007
        }
    }

    for i := 1; i <= #reports; i++ {
        checksum = (checksum + #reports[i] + i * 31) % 1000000007
    }
    return checksum
}

func run_orders_slow(inv, item_count, orders) {
    checksum := 0
    reports := {}
    marker := 0
    for i := 1; i <= orders; i++ {
        idx := (i * 37) % item_count + 1
        sku := string.format("SKU%05d", idx)
        item := inv.by_sku[sku]
        qty := (i * 11) % 6 + 1
        available := item.stock - item.reserved

        if available >= qty {
            item.stock = item.stock - qty
            item.sold = item.sold + qty
            checksum = (checksum + math.floor(item.price * qty * 100.0) + item.sold + idx) % 1000000007
        } else {
            item.reserved = math.floor(item.reserved * 0.5)
            checksum = (checksum + idx + item.stock + item.reserved) % 1000000007
        }

        if item.stock < item.reorder {
            item.stock = item.stock + 120 + (idx % 30)
            checksum = (checksum + item.stock * 3) % 1000000007
        }

        if i % 2500 == 0 {
            report := string.format("%s:%d:%d:%.2f", item.sku, item.stock, item.sold, item.price)
            reports[#reports + 1] = report
            checksum = (checksum + #report * 17) % 1000000007
        }
    }

    for i := 1; i <= #reports; i++ {
        checksum = (checksum + #reports[i] + i * 31) % 1000000007
    }
    return checksum + marker
}

inv_fast := build_inventory(64)
inv_slow := build_inventory(64)
fast_result := run_orders_fast(inv_fast, 64, 6000)
slow_result := run_orders_slow(inv_slow, 64, 6000)
`
	proto := compileMixedInventoryKernelTestProgram(t, src)
	if len(proto.Protos) != 3 {
		t.Fatalf("nested protos = %d, want 3", len(proto.Protos))
	}
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[1]), "mixed_inventory_orders")
	rejectKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[2]), "mixed_inventory_orders")

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
		t.Fatalf("mixed inventory kernel did not mark fast proto entered")
	}
}

func compileMixedInventoryKernelTestProgram(t *testing.T, src string) *FuncProto {
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
