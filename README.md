# GScript

GScript 是一门**Go 语法 + Lua 语义**的脚本语言，用 Go 实现，解释执行。

- 语法接近 Go（`:=` 声明、`func`、`for`、`if`）
- 语义完整覆盖 Lua（table、metatable、closure、coroutine、多返回值）
- 内置 HTTP 服务器和 OpenGL 2D 绘图支持

```
            ┌──────────────┐
 .gs 源码 → │ Lexer/Parser │ → AST → Interpreter → 结果
            └──────────────┘
```

---

## 安装

```bash
git clone https://github.com/jxwr/gscript
cd gscript
go build -o gscript ./cmd/gscript/
```

---

## 快速开始

```bash
# 执行文件
./gscript examples/fib.gs

# 执行字符串
./gscript -e 'print("Hello, GScript!")'

# 交互式 REPL
./gscript
```

---

## 语言特性

### 1. 基本类型

| 类型 | 示例 |
|------|------|
| nil | `nil` |
| bool | `true`, `false` |
| int | `42`, `-100` |
| float | `3.14`, `1e10` |
| string | `"hello"` |
| table | `{1, 2, 3}` |
| function | `func(x) { return x }` |
| coroutine | `coroutine.create(f)` |

```go
a := 42
b := 3.14
s := "hello"
ok := true
nothing := nil
```

---

### 2. 运算符

```go
// 算术
1 + 2       // 3
10 - 3      // 7
4 * 5       // 20
10 / 4      // 2.5
10 % 3      // 1
2 ** 8      // 256（幂运算）

// 字符串拼接
"hello" .. " world"   // "hello world"
#"hello"              // 5（长度）

// 比较
1 == 1    // true
1 != 2    // true
1 < 2     // true
1 <= 1    // true

// 逻辑（短路求值）
true && false   // false
true || false   // true
!true           // false

// 复合赋值
x += 1
x -= 1
x *= 2
x /= 2
x++
x--
```

---

### 3. 变量

```go
// 声明（:=）
x := 10
a, b := 1, 2

// 赋值（=）
x = 20
a, b = b, a   // 交换
```

---

### 4. 控制流

#### if / elseif / else

```go
score := 85

if score >= 90 {
    print("A")
} elseif score >= 80 {
    print("B")
} elseif score >= 70 {
    print("C")
} else {
    print("F")
}
```

#### for（while 风格）

```go
n := 1
for n < 100 {
    n = n * 2
}
print(n)  // 128
```

#### for（C 风格）

```go
sum := 0
for i := 1; i <= 100; i++ {
    sum = sum + i
}
print(sum)  // 5050
```

#### for range（迭代 table）

```go
fruits := {"apple", "banana", "cherry"}
for i, v := range fruits {
    print(i, v)
}
// 1  apple
// 2  banana
// 3  cherry
```

#### break / continue

```go
for i := 1; i <= 10; i++ {
    if i == 5 { break }
    if i % 2 == 0 { continue }
    print(i)   // 1 3
}
```

---

### 5. 函数

```go
// 基本函数
func add(a, b) {
    return a + b
}
print(add(3, 4))  // 7

// 多返回值
func divmod(a, b) {
    return math.floor(a / b), a % b
}
q, r := divmod(17, 5)
print(q, r)  // 3  2

// 递归
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
print(fib(10))  // 55

// 函数是一等公民
apply := func(f, x) { return f(x) }
double := func(x) { return x * 2 }
print(apply(double, 21))  // 42
```

#### 可变参数

```go
func sum(...) {
    total := 0
    for _, v := range ... {
        total = total + v
    }
    return total
}
print(sum(1, 2, 3, 4, 5))  // 15
```

---

### 6. 闭包 / Upvalue

GScript 完整支持词法闭包，多个闭包共享同一 upvalue。

```go
func makeCounter(start) {
    n := start
    return {
        inc: func() { n = n + 1; return n },
        dec: func() { n = n - 1; return n },
        get: func() { return n },
    }
}

c := makeCounter(0)
print(c.inc())  // 1
print(c.inc())  // 2
print(c.dec())  // 1
print(c.get())  // 1
```

```go
// 两个闭包共享同一变量
x := 0
inc := func() { x = x + 1 }
get := func() { return x }

inc()
inc()
print(get())  // 2
```

---

### 7. Table

Table 是 GScript 的核心数据结构，同时充当数组和哈希表。

```go
// 数组风格（1-based）
arr := {10, 20, 30, 40, 50}
print(arr[1])   // 10
print(#arr)     // 5

// 哈希风格
person := {name: "alice", age: 30}
print(person.name)       // alice
print(person["age"])     // 30

// 混合
data := {
    title: "GScript",
    tags: {"fast", "dynamic", "fun"},
    version: 1,
}

// 动态修改
arr[6] = 60
person.email = "alice@example.com"

// 嵌套
matrix := {{1,2,3},{4,5,6},{7,8,9}}
print(matrix[2][2])  // 5
```

---

### 8. Metatable（元表）

支持全部 14 种 Lua 元方法，可实现运算符重载、OOP 继承等。

```go
// __index 实现 OOP 继承
Animal := {}
Animal.new = func(name) {
    self := {name: name}
    setmetatable(self, {__index: Animal})
    return self
}
Animal.speak = func(self) {
    return self.name .. " makes a sound"
}

Dog := {}
setmetatable(Dog, {__index: Animal})
Dog.speak = func(self) {
    return self.name .. " says woof!"
}
Dog.new = func(name) {
    self := Animal.new(name)
    setmetatable(self, {__index: Dog})
    return self
}

rex := Dog.new("Rex")
print(rex.speak(rex))  // Rex says woof!
```

```go
// 运算符重载
Vec2 := {}
Vec2.__index = Vec2
Vec2.new = func(x, y) {
    v := {x: x, y: y}
    setmetatable(v, Vec2)
    return v
}
Vec2.__add = func(a, b) { return Vec2.new(a.x+b.x, a.y+b.y) }
Vec2.__eq  = func(a, b) { return a.x==b.x && a.y==b.y }
Vec2.__tostring = func(v) { return "("..v.x..", "..v.y..")" }

v1 := Vec2.new(1, 2)
v2 := Vec2.new(3, 4)
v3 := v1 + v2
print(v3.x, v3.y)  // 4  6
```

**支持的元方法：**

| 元方法 | 触发时机 |
|--------|---------|
| `__index` | 读取不存在的字段 |
| `__newindex` | 写入不存在的字段 |
| `__add` | `a + b` |
| `__sub` | `a - b` |
| `__mul` | `a * b` |
| `__div` | `a / b` |
| `__mod` | `a % b` |
| `__pow` | `a ** b` |
| `__unm` | `-a` |
| `__concat` | `a .. b` |
| `__len` | `#a` |
| `__eq` | `a == b` |
| `__lt` | `a < b` |
| `__le` | `a <= b` |
| `__call` | `a(args)` |

---

### 9. Coroutine（协程）

基于 goroutine + channel 实现，API 与 Lua 完全兼容。

```go
// 基础 yield/resume
co := coroutine.create(func() {
    coroutine.yield(1)
    coroutine.yield(2)
    coroutine.yield(3)
})

ok, v := coroutine.resume(co)  // true  1
ok, v  = coroutine.resume(co)  // true  2
ok, v  = coroutine.resume(co)  // true  3
```

```go
// 生产者模式
gen := coroutine.wrap(func() {
    for i := 1; i <= 5; i++ {
        coroutine.yield(i * i)
    }
})

for {
    v := gen()
    if v == nil { break }
    print(v)   // 1  4  9  16  25
}
```

```go
// 双向传值
co := coroutine.create(func(init) {
    x := init
    for {
        x = coroutine.yield(x * 2)
    }
})

_, v := coroutine.resume(co, 1)   // 启动，v=2
_, v  = coroutine.resume(co, 3)   // 传入3，v=6
_, v  = coroutine.resume(co, 5)   // 传入5，v=10
```

**API：**

| 函数 | 说明 |
|------|------|
| `coroutine.create(f)` | 创建协程 |
| `coroutine.resume(co, ...)` | 恢复执行，返回 `ok, values...` |
| `coroutine.yield(...)` | 暂停，返回 resume 传入的值 |
| `coroutine.wrap(f)` | 返回一个函数，每次调用相当于 resume |
| `coroutine.status(co)` | `"suspended"` / `"running"` / `"dead"` |
| `coroutine.isyieldable()` | 是否在协程中 |

---

### 10. 错误处理

```go
// pcall 捕获错误
ok, err := pcall(func() {
    error("something went wrong")
})
print(ok)   // false
print(err)  // something went wrong

// 错误可以是任意值
ok, e := pcall(func() {
    error({code: 404, msg: "not found"})
})
print(e.code)  // 404
print(e.msg)   // not found

// xpcall 带错误处理函数
ok, msg := xpcall(
    func() { error("oops") },
    func(err) { return "caught: " .. err }
)
print(msg)  // caught: oops

// assert
assert(1 + 1 == 2, "math is broken")

ok2, e2 := pcall(func() {
    assert(false, "failed!")
})
print(e2)  // failed!
```

---

## 标准库

### string 库

```go
s := "Hello, World!"

string.len(s)               // 13
string.upper(s)             // "HELLO, WORLD!"
string.lower(s)             // "hello, world!"
string.sub(s, 1, 5)         // "Hello"
string.sub(s, -6)           // "orld!"
string.rep("ab", 3)         // "ababab"
string.rep("ab", 3, "-")    // "ab-ab-ab"
string.reverse("hello")     // "olleh"
string.byte("A")            // 65
string.char(65, 66, 67)     // "ABC"

// 查找（支持 Lua 模式）
string.find("hello world", "world")     // 7  11
string.find("hello", "l+")             // 3  4

// 模式匹配
string.match("2024-03-11", "(%d+)-(%d+)-(%d+)")  // "2024" "03" "11"

// 全局替换
string.gsub("hello world", "o", "0")   // "hell0 w0rld"  2
string.gsub("aaa", "a", "b", 2)        // "bba"  2

// 迭代匹配
for word := range string.gmatch("one two three", "%a+") {
    print(word)
}

// 格式化
string.format("%d + %d = %d", 1, 2, 3)     // "1 + 2 = 3"
string.format("%.2f", 3.14159)              // "3.14"
string.format("%s is %d years old", "Alice", 30)

// 字符串方法（通过元表）
("hello"):upper()     // "HELLO"
("  hi  "):sub(3, 4)  // "hi"
```

**Lua 模式类：**

| 模式 | 匹配 |
|------|------|
| `%d` | 数字 |
| `%a` | 字母 |
| `%l` | 小写字母 |
| `%u` | 大写字母 |
| `%s` | 空白符 |
| `%w` | 字母数字 |
| `%p` | 标点 |
| `.` | 任意字符 |

---

### table 库

```go
t := {10, 20, 30}

table.insert(t, 40)        // 末尾插入：{10,20,30,40}
table.insert(t, 2, 15)     // 指定位置：{10,15,20,30,40}
table.remove(t)            // 移除末尾：{10,15,20,30}
table.remove(t, 1)         // 移除首位：{15,20,30}

table.concat(t, ", ")      // "15, 20, 30"
table.concat(t, "-", 2, 3) // "20-30"

table.sort(t)              // 升序排序
table.sort(t, func(a, b) { return a > b })  // 降序

-- unpack 展开为多值
a, b, c := table.unpack({10, 20, 30})
print(a, b, c)  // 10  20  30
```

---

### math 库

```go
math.pi           // 3.141592653589793
math.huge         // +∞
math.maxinteger   // 9223372036854775807

math.abs(-5)      // 5
math.floor(3.7)   // 3
math.ceil(3.2)    // 4
math.sqrt(16)     // 4
math.pow(2, 10)   // 1024

math.sin(math.pi / 2)  // 1
math.cos(0)            // 1
math.atan(1, 1)        // π/4

math.max(1, 5, 3, 2)   // 5
math.min(1, 5, 3, 2)   // 1

math.log(math.exp(1))  // 1
math.log(100, 10)      // 2

math.random()          // [0, 1) 的随机浮点数
math.random(6)         // 1~6 的随机整数
math.random(1, 100)    // 1~100 的随机整数
math.randomseed(42)

math.type(1)     // "integer"
math.type(1.0)   // "float"
math.type("x")   // false
```

---

### io 库

```go
io.write("hello ")    // 不换行输出
io.write("world\n")

line := io.read()     // 读一行
num  := io.read("*n") // 读一个数字
all  := io.read("*a") // 读全部

-- 文件操作
f := io.open("data.txt", "r")
content := f.read("*a")
f.close()

f2 := io.open("out.txt", "w")
f2.write("hello\n")
f2.close()
```

---

### os 库

```go
os.time()           // Unix 时间戳（秒）
os.clock()          // CPU 时间（秒）
os.date("%Y-%m-%d") // 格式化日期，如 "2024-03-11"
os.getenv("HOME")   // 环境变量
os.exit(0)          // 退出进程
```

---

### 内置函数

```go
print(...)                 // 打印，tab 分隔，自动换行
type(v)                    // 返回类型名字符串
tostring(v)                // 转为字符串
tonumber(s [, base])       // 转为数字
#v                         // 字符串/table 长度

pairs(t)                   // 迭代 table 所有键值对
ipairs(t)                  // 迭代 table 数组部分（1, 2, 3...）
next(t [, key])            // 迭代器底层函数
select(n, ...)             // 返回第 n 个起的参数
unpack(t [, i [, j]])      // 展开 table 为多值

setmetatable(t, mt)        // 设置元表
getmetatable(t)            // 获取元表
rawget(t, k)               // 跳过元表直接读
rawset(t, k, v)            // 跳过元表直接写
rawequal(a, b)             // 跳过 __eq 比较

pcall(f, ...)              // 保护调用
xpcall(f, handler, ...)    // 带处理器的保护调用
error(msg)                 // 抛出错误
assert(v [, msg])          // 断言

require(name)              // 加载模块
dofile(path)               // 执行文件
loadstring(src)            // 编译字符串为函数
```

---

## HTTP 服务器

内置 `http` 库，基于 Go 的 `net/http`。

```go
// 最简单的服务器
http.listen(":8080", func(req, res) {
    res.write("Hello from GScript!\n")
    res.write("Path: " .. req.path .. "\n")
})
```

```go
// 使用路由器
router := http.newRouter()

// 计数器
counter := 0

router.get("/", func(req, res) {
    res.header("Content-Type", "text/html")
    res.write("<h1>GScript Web Server</h1>")
})

router.get("/count", func(req, res) {
    counter = counter + 1
    res.write("Count: " .. counter)
})

router.get("/greet", func(req, res) {
    name := req.param("name")
    if name == nil { name = "stranger" }
    res.write("Hello, " .. name .. "!")
})

router.post("/echo", func(req, res) {
    res.json({body: req.body, method: req.method})
})

router.listen(":9988")
```

**Request 对象：**

| 字段/方法 | 说明 |
|-----------|------|
| `req.method` | HTTP 方法（GET/POST 等）|
| `req.path` | 请求路径 |
| `req.url` | 完整 URL |
| `req.body` | 请求体字符串 |
| `req.headers` | 请求头 table |
| `req.query` | 查询参数 table |
| `req.param("name")` | 获取查询参数 |
| `req.json()` | 解析 body 为 table |

**Response 对象：**

| 方法 | 说明 |
|------|------|
| `res.write(s)` | 写入响应体 |
| `res.writeln(s)` | 写入响应体并换行 |
| `res.json(table)` | 写入 JSON 响应 |
| `res.status(code)` | 设置状态码 |
| `res.header(k, v)` | 设置响应头 |
| `res.redirect(url)` | 重定向 |

---

## OpenGL 绘图

内置 `gl` 库，基于 OpenGL 4.1 + GLFW，可用于游戏和可视化。

```go
// 创建窗口
win := gl.newWindow(800, 600, "My Game")

// 游戏循环
for !win.shouldClose() {
    win.pollEvents()

    // 清屏（深蓝色背景）
    win.clear(0.05, 0.05, 0.1)

    // 绘制填充矩形（x, y, w, h, r, g, b, a）
    gl.drawRect(100, 100, 200, 150, 1, 0, 0, 1)       // 红色矩形
    gl.drawRect(400, 200, 100, 100, 0, 1, 0, 0.5)     // 半透明绿色

    // 绘制边框矩形（x, y, w, h, r, g, b, 线宽）
    gl.drawRectOutline(50, 50, 300, 200, 1, 1, 0, 2)

    // 绘制文字（text, x, y, scale, r, g, b）
    gl.drawText("Score: 1234", 10, 10, 1.5, 1, 1, 1)

    win.swapBuffers()
}
win.close()
```

**键盘输入：**

```go
// 持续按下
if gl.isKeyDown(gl.KEY_LEFT) {
    x = x - 5
}

// 刚按下（单次触发）
if gl.isKeyJustPressed(gl.KEY_SPACE) {
    jump()
}
```

**键盘常量：** `KEY_LEFT` `KEY_RIGHT` `KEY_UP` `KEY_DOWN` `KEY_SPACE` `KEY_ESCAPE` `KEY_ENTER` `KEY_A`~`KEY_Z` `KEY_0`~`KEY_9`

---

## 示例程序

### Fibonacci

```go
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

for i := 0; i <= 20; i++ {
    print(i, fib(i))
}
```

### 闭包计数器

```go
func makeCounter(name, start) {
    n := start
    return {
        inc:   func() { n = n + 1; return n },
        dec:   func() { n = n - 1; return n },
        get:   func() { return n },
        reset: func() { n = start },
        name:  name,
    }
}

c := makeCounter("hits", 0)
print(c.inc())   // 1
print(c.inc())   // 2
print(c.get())   // 2
c.reset()
print(c.get())   // 0
```

### OOP 继承

```go
func Class(parent) {
    cls := {}
    cls.__index = cls
    if parent != nil {
        setmetatable(cls, {__index: parent})
    }
    cls.new = func(...) {
        inst := {}
        setmetatable(inst, cls)
        if cls.init != nil { cls.init(inst, ...) }
        return inst
    }
    return cls
}

Animal := Class(nil)
Animal.init  = func(self, name) { self.name = name }
Animal.speak = func(self) { return self.name .. " makes a sound" }

Dog := Class(Animal)
Dog.speak = func(self) { return self.name .. " says woof!" }

rex := Dog.new("Rex")
print(rex.speak(rex))  // Rex says woof!
```

### 协程生成器

```go
func range_gen(from, to) {
    return coroutine.wrap(func()
        for i := from; i <= to; i++ {
            coroutine.yield(i)
        end
    end)
}

sum := 0
gen := range_gen(1, 100)
for {
    v := gen()
    if v == nil { break }
    sum = sum + v
}
print(sum)  // 5050
```

### 内置 Tetris 游戏

```bash
./gscript examples/tetris.gs
```

操作：`←→` 移动，`↑/X` 顺转，`Z` 逆转，`↓` 软降，`空格` 硬降，`C` 暂存，`P` 暂停，`R` 重开

---

## 项目结构

```
gscript/
├── cmd/gscript/          # CLI 入口
├── internal/
│   ├── lexer/            # 词法分析器
│   ├── parser/           # 递归下降解析器
│   ├── ast/              # AST 节点定义
│   └── runtime/          # 树遍历解释器 + 标准库
│       ├── interpreter.go
│       ├── value.go       # 类型系统（tagged union）
│       ├── table.go       # Table 实现
│       ├── closure.go     # 闭包 / Upvalue
│       ├── coroutine.go   # 协程
│       ├── stdlib_string.go
│       ├── stdlib_table.go
│       ├── stdlib_math.go
│       ├── stdlib_io.go
│       ├── stdlib_os.go
│       ├── stdlib_http.go
│       └── stdlib_gl.go   # OpenGL 绘图
├── tests/                # 集成测试
├── examples/             # 示例程序
│   ├── fib.gs
│   ├── counter.gs
│   ├── class.gs
│   ├── webserver.gs
│   └── tetris.gs
└── docs/decisions/       # 架构决策记录
```

---

## 运行测试

```bash
go test ./...              # 全部测试
go test -race ./...        # 含竞态检测
go test ./internal/runtime/... -v -run TestClosure
```

---

## License

MIT
