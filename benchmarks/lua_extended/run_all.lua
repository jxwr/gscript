-- Run all extended LuaJIT references in sequence.

local benchmarks = {
    "actors_dispatch_mutation",
    "groupby_nested_agg",
    "json_table_walk",
    "log_tokenize_format",
    "mixed_inventory_sim",
    "producer_consumer_pipeline",
}

local script_dir = ""
if arg and arg[0] then
    script_dir = arg[0]:match("(.*/)") or ""
end

print("============================================")
print("  LuaJIT Extended Benchmark Suite")
print("  " .. (_VERSION or "Lua"))
print("============================================")
print("")

local total_t0 = os.clock()
for _, name in ipairs(benchmarks) do
    print("--- " .. name .. " ---")
    local ok, err = pcall(dofile, script_dir .. name .. ".lua")
    if not ok then
        print("ERROR: " .. tostring(err))
        os.exit(1)
    end
    print("")
end

local total_elapsed = os.clock() - total_t0
print("============================================")
print(string.format("  Extended suite complete (total %.2fs)", total_elapsed))
print("============================================")

