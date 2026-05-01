-- Extended benchmark: coroutine producer/consumer pipeline with table payloads.

local function make_producer(n)
    return coroutine.create(function()
        for i = 1, n do
            local kind = "view"
            if i % 13 == 0 then
                kind = "error"
            elseif i % 5 == 0 then
                kind = "click"
            end
            local event = {
                id = i,
                account = (i * 17) % 257,
                shard = (i * 7) % 16 + 1,
                kind = kind,
                value = (i * 29) % 1000
            }
            coroutine.yield(event)
        end
        return nil
    end)
end

local function consume(n)
    local co = make_producer(n)
    local by_shard = {}
    for i = 1, 16 do
        by_shard[i] = {count = 0, value = 0, errors = 0}
    end

    local checksum = 0
    for _ = 1, n do
        local ok, event = coroutine.resume(co)
        if not ok then
            return checksum
        end
        local agg = by_shard[event.shard]
        agg.count = agg.count + 1
        agg.value = agg.value + event.value
        if event.kind == "error" then
            agg.errors = agg.errors + 1
            checksum = (checksum + event.account * 5 + agg.errors) % 1000000007
        else
            checksum = (checksum + event.value + agg.count + #event.kind) % 1000000007
        end
    end

    for i = 1, 16 do
        local agg = by_shard[i]
        checksum = (checksum + agg.count * 3 + agg.value + agg.errors * 101) % 1000000007
    end
    return checksum
end

local N = 650000

local t0 = os.clock()
local checksum = consume(N)
local elapsed = os.clock() - t0

print(string.format("producer_consumer_pipeline events=%d", N))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
