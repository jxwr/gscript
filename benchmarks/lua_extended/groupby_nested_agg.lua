-- Extended benchmark: nested group-by aggregation with dynamic string keys.

local function make_event(i, regions, products, channels)
    local region = regions[(i * 3) % #regions + 1]
    local product = products[(i * 5) % #products + 1]
    local channel = channels[(i * 7) % #channels + 1]
    local qty = (i * 17) % 9 + 1
    local price = (i * 31) % 200 + 50
    return {region = region, product = product, channel = channel, qty = qty, revenue = qty * price, id = i}
end

local function aggregate(n, passes)
    local regions = {"na", "eu", "apac", "latam", "mea"}
    local products = {"core", "pro", "team", "edge", "ai", "data", "ops"}
    local channels = {"web", "partner", "sales", "market"}
    local totals = {}
    local checksum = 0

    for pass = 1, passes do
        for i = 1, n do
            local ev = make_event(i + pass * 97, regions, products, channels)
            local by_region = totals[ev.region]
            if by_region == nil then
                by_region = {}
                totals[ev.region] = by_region
            end
            local agg = by_region[ev.product]
            if agg == nil then
                agg = {count = 0, qty = 0, revenue = 0, web = 0, partner = 0, sales = 0, market = 0}
                by_region[ev.product] = agg
            end

            agg.count = agg.count + 1
            agg.qty = agg.qty + ev.qty
            agg.revenue = agg.revenue + ev.revenue
            if ev.channel == "web" then
                agg.web = agg.web + ev.qty
            elseif ev.channel == "partner" then
                agg.partner = agg.partner + ev.qty
            elseif ev.channel == "sales" then
                agg.sales = agg.sales + ev.qty
            else
                agg.market = agg.market + ev.qty
            end

            checksum = (checksum + agg.count * 3 + agg.qty + agg.revenue + #ev.region + #ev.product) % 1000000007
        end
    end

    for r = 1, #regions do
        local by_region = totals[regions[r]]
        for p = 1, #products do
            local agg = by_region[products[p]]
            checksum = (checksum + agg.count + agg.qty * 7 + agg.web * 11 + agg.market * 13) % 1000000007
        end
    end
    return checksum
end

local N = 60000
local PASSES = 8

local t0 = os.clock()
local checksum = aggregate(N, PASSES)
local elapsed = os.clock() - t0

print(string.format("groupby_nested_agg events=%d passes=%d", N, PASSES))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
