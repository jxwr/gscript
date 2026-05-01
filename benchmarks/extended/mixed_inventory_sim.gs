// Extended benchmark: mixed numeric, table, and formatting workload.

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

func run_orders(inv, item_count, orders) {
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

ITEMS := 5000
ORDERS := 650000

t0 := time.now()
inv := build_inventory(ITEMS)
checksum := run_orders(inv, ITEMS, ORDERS)
elapsed := time.since(t0)

print(string.format("mixed_inventory_sim items=%d orders=%d", ITEMS, ORDERS))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
