-- Extended benchmark: mixed numeric, table, and formatting workload.

local function build_inventory(n)
    local items = {}
    local by_sku = {}
    for i = 1, n do
        local sku = string.format("SKU%05d", i)
        local item = {
            sku = sku,
            stock = 80 + (i * 19) % 220,
            reserved = i % 13,
            price = 5.0 + (i % 97) * 0.75,
            reorder = 30 + i % 40,
            sold = 0
        }
        items[i] = item
        by_sku[sku] = item
    end
    return {items = items, by_sku = by_sku}
end

local function run_orders(inv, item_count, orders)
    local checksum = 0
    local reports = {}
    for i = 1, orders do
        local idx = (i * 37) % item_count + 1
        local sku = string.format("SKU%05d", idx)
        local item = inv.by_sku[sku]
        local qty = (i * 11) % 6 + 1
        local available = item.stock - item.reserved

        if available >= qty then
            item.stock = item.stock - qty
            item.sold = item.sold + qty
            checksum = (checksum + math.floor(item.price * qty * 100.0) + item.sold + idx) % 1000000007
        else
            item.reserved = math.floor(item.reserved * 0.5)
            checksum = (checksum + idx + item.stock + item.reserved) % 1000000007
        end

        if item.stock < item.reorder then
            item.stock = item.stock + 120 + (idx % 30)
            checksum = (checksum + item.stock * 3) % 1000000007
        end

        if i % 2500 == 0 then
            local report = string.format("%s:%d:%d:%.2f", item.sku, item.stock, item.sold, item.price)
            reports[#reports + 1] = report
            checksum = (checksum + #report * 17) % 1000000007
        end
    end

    for i = 1, #reports do
        checksum = (checksum + #reports[i] + i * 31) % 1000000007
    end
    return checksum
end

local ITEMS = 5000
local ORDERS = 650000

local t0 = os.clock()
local inv = build_inventory(ITEMS)
local checksum = run_orders(inv, ITEMS, ORDERS)
local elapsed = os.clock() - t0

print(string.format("mixed_inventory_sim items=%d orders=%d", ITEMS, ORDERS))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
