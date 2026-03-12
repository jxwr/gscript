-- Comprehensive benchmark suite for native Lua / LuaJIT
-- Measures pure computation time using os.clock()

local function bench(name, iterations, fn)
    -- warmup
    fn()
    -- timed run
    local start = os.clock()
    for i = 1, iterations do
        fn()
    end
    local elapsed = os.clock() - start
    local per_op = elapsed / iterations * 1e6 -- microseconds
    io.write(string.format("%-30s %8d iterations  %10.0f us/op  (total %.3fs)\n",
        name, iterations, per_op, elapsed))
end

-- 1. Fibonacci recursive n=20
local function fib(n)
    if n < 2 then return n end
    return fib(n-1) + fib(n-2)
end
bench("FibRecursive_N20", 1000, function() fib(20) end)

-- 2. Fibonacci recursive n=25
bench("FibRecursive_N25", 100, function() fib(25) end)

-- 3. Fibonacci iterative n=30
local function fib_iter(n)
    local a, b = 0, 1
    for i = 1, n do
        a, b = b, a + b
    end
    return a
end
bench("FibIterative_N30", 100000, function() fib_iter(30) end)

-- 4. Table ops (1000 keys)
bench("TableOps_1000", 1000, function()
    local t = {}
    for i = 0, 999 do
        t[tostring(i)] = i
    end
    local sum = 0
    for i = 0, 999 do
        sum = sum + t[tostring(i)]
    end
end)

-- 5. String concat (100x)
bench("StringConcat_100", 10000, function()
    local s = ""
    for i = 1, 100 do
        s = s .. "x"
    end
end)

-- 6. Closure creation (1000)
local function make_closure(x)
    return function() return x end
end
bench("ClosureCreation_1000", 1000, function()
    local closures = {}
    for i = 1, 1000 do
        closures[i] = make_closure(i)
    end
end)

-- 7. Function calls (10000)
local function add(a, b)
    return a + b
end
bench("FunctionCalls_10000", 100, function()
    local x = 0
    for i = 1, 10000 do
        x = add(x, 1)
    end
end)
