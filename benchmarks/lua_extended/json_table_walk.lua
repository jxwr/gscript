-- Extended benchmark: JSON-like table construction and nested traversal.

local function make_document(i)
    local kind = "article"
    if i % 5 == 0 then
        kind = "invoice"
    elseif i % 7 == 0 then
        kind = "event"
    end

    return {
        id = i,
        kind = kind,
        active = i % 4 ~= 0,
        user = {id = i % 503, tier = i % 6, region = (i % 9) + 1},
        metrics = {views = i * 3 % 1000, clicks = i * 7 % 251, errors = i % 13},
        tags = {first = string.format("tag%d", i % 11), second = string.format("team%d", i % 5), third = string.format("r%d", i % 9)}
    }
end

local function build_documents(n)
    local docs = {}
    for i = 1, n do
        docs[i] = make_document(i)
    end
    return docs
end

local function walk_documents(docs, n, passes)
    local checksum = 0
    for pass = 1, passes do
        for i = 1, n do
            local doc = docs[i]
            local metrics = doc.metrics
            local user = doc.user
            local tags = doc.tags
            local tag = tags.first
            local tag_choice = (i + pass) % 3
            if tag_choice == 1 then
                tag = tags.second
            elseif tag_choice == 2 then
                tag = tags.third
            end

            if doc.active then
                local score = metrics.views + metrics.clicks * 3 - metrics.errors * 5 + user.tier + #doc.kind + #tag
                checksum = (checksum + score + user.region) % 1000000007
                if pass % 3 == 0 then
                    metrics.views = (metrics.views + user.tier + i) % 2000
                end
            else
                checksum = (checksum + doc.id + metrics.errors + #tags.first) % 1000000007
            end
        end
    end
    return checksum
end

local N = 18000
local PASSES = 80

local t0 = os.clock()
local docs = build_documents(N)
local checksum = walk_documents(docs, N, PASSES)
local elapsed = os.clock() - t0

print(string.format("json_table_walk documents=%d passes=%d", N, PASSES))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
