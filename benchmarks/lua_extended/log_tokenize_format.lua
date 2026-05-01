-- Extended benchmark: log-line formatting, tokenization, and metric extraction.

local function split_plain(s, sep)
    local out = {}
    local start = 1
    while true do
        local i, j = string.find(s, sep, start, true)
        if not i then
            out[#out + 1] = string.sub(s, start)
            return out
        end
        out[#out + 1] = string.sub(s, start, i - 1)
        start = j + 1
    end
end

local function make_line(i)
    local status = 200
    if i % 17 == 0 then
        status = 500
    elseif i % 11 == 0 then
        status = 404
    elseif i % 5 == 0 then
        status = 302
    end
    local route = string.format("/v1/items/%d/detail", i % 97)
    local trace = string.format("trace%04d-%03d", i % 10000, (i * 13) % 997)
    return string.format("ts=%d|svc=api%d|route=%s|status=%d|bytes=%d|trace=%s", 1700000000 + i, i % 9, route, status, 400 + (i * 23) % 9000, trace)
end

local function build_lines(n)
    local lines = {}
    for i = 1, n do
        lines[i] = make_line(i)
    end
    return lines
end

local function parse_lines(lines, n, passes)
    local checksum = 0
    for pass = 1, passes do
        for i = 1, n do
            local parts = split_plain(lines[i], "|")
            local svc = string.sub(parts[2], 5)
            local route = string.sub(parts[3], 7)
            local status = tonumber(string.sub(parts[4], 8))
            local bytes = tonumber(string.sub(parts[5], 7))
            local trace = string.sub(parts[6], 7)

            local class = math.floor(status / 100)
            local shaped = string.format("%s:%d:%s:%d", svc, class, string.sub(route, 1, 9), bytes % 97)
            checksum = (checksum + #shaped + #trace + status + bytes % 4096 + pass) % 1000000007
        end
    end
    return checksum
end

local N = 18000
local PASSES = 8

local t0 = os.clock()
local lines = build_lines(N)
local checksum = parse_lines(lines, N, PASSES)
local elapsed = os.clock() - t0

print(string.format("log_tokenize_format lines=%d passes=%d", N, PASSES))
print(string.format("checksum: %d", checksum))
print(string.format("Time: %.3fs", elapsed))
