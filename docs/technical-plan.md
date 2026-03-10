# GScript 技术方案

## 1. 语言定义

GScript = Go 语法 + Lua 语义，解释执行。

### 语法示例

```go
// 函数定义
func fib(n) {
    if n < 2 {
        return n
    }
    return fib(n-1) + fib(n-2)
}

// 多返回值
func divmod(a, b) {
    return a / b, a % b
}
q, r := divmod(10, 3)

// table
t := {name: "alice", age: 20}
print(t.name)
t["key"] = "value"

// 闭包
func counter() {
    i := 0
    return func() {
        i = i + 1
        return i
    }
}

// 变参
func sum(...args) {
    total := 0
    for _, v := range args {
        total = total + v
    }
    return total
}

// for 循环
for i := 0; i < 10; i++ {
    print(i)
}

// for-range (ipairs 风格)
for i, v := range t {
    print(i, v)
}

// while 风格 (用 for 替代)
for condition {
    // ...
}

// metatable
setmetatable(t, {
    __index: func(key) { return "missing" },
    __add:   func(a, b) { return merge(a, b) },
})

// coroutine
co := coroutine.create(func() {
    coroutine.yield(1)
    coroutine.yield(2)
})
ok, v := coroutine.resume(co)

// 错误处理
ok, err := pcall(func() {
    error("something went wrong")
})
```

---

## 2. 架构设计

```
source code
    │
    ▼
  Lexer         → Token stream
    │
    ▼
  Parser        → AST
    │
    ▼
  Interpreter   → tree-walking 执行
    │
    ▼
  Runtime       → Value model + Environment + GC
```

---

## 3. 核心数据结构

### 3.1 Value（tagged union）

```go
type ValueType uint8

const (
    TypeNil ValueType = iota
    TypeBool
    TypeInt
    TypeFloat
    TypeString
    TypeTable
    TypeFunction
    TypeCoroutine
    TypeUserdata
)

type Value struct {
    typ  ValueType
    ival int64       // Bool(0/1) | Int
    fval float64     // Float
    sval string      // String
    ptr  any         // *Table | *Closure | *GoFunction | *Coroutine
}
```

### 3.2 Table

```go
type Table struct {
    hash      map[Value]Value
    array     []Value          // 整数下标 1-based 优化
    metatable *Table
    nextKey    map[Value]Value  // for next() iteration
}
```

### 3.3 Closure / Function

```go
type Closure struct {
    proto    *FuncProto    // 函数原型（参数、body AST）
    upvalues []*Upvalue    // 捕获的上值
    env      *Environment  // 定义时的环境
}

type Upvalue struct {
    value *Value    // 指向栈上或堆上的 Value
    closed bool
}

type GoFunction struct {
    name string
    fn   func(args []Value) ([]Value, error)
}
```

### 3.4 Environment（作用域链）

```go
type Environment struct {
    vars   map[string]*Value   // 当前作用域变量（指针，支持闭包共享）
    parent *Environment        // 外层作用域
}
```

### 3.5 Coroutine

```go
type CoroutineStatus int

const (
    CoroutineSuspended CoroutineStatus = iota
    CoroutineRunning
    CoroutineDead
    CoroutineNormal
)

type Coroutine struct {
    status  CoroutineStatus
    fn      Value
    args    []Value
    results []Value
    resume  chan []Value   // main → coroutine
    yield   chan []Value   // coroutine → main
}
```

---

## 4. Lexer 设计

### Token 类型

```
// 字面量
TOKEN_NUMBER, TOKEN_STRING, TOKEN_TRUE, TOKEN_FALSE, TOKEN_NIL

// 标识符
TOKEN_IDENT

// 关键字
TOKEN_FUNC, TOKEN_RETURN, TOKEN_IF, TOKEN_ELSE, TOKEN_ELSEIF
TOKEN_FOR, TOKEN_RANGE, TOKEN_BREAK, TOKEN_CONTINUE
TOKEN_VAR (用 := 替代), TOKEN_IN

// 运算符
TOKEN_ASSIGN (:=), TOKEN_EQ (==), TOKEN_NEQ (!=)
TOKEN_LT, TOKEN_LE, TOKEN_GT, TOKEN_GE
TOKEN_ADD, TOKEN_SUB, TOKEN_MUL, TOKEN_DIV, TOKEN_MOD, TOKEN_POW
TOKEN_AND, TOKEN_OR, TOKEN_NOT
TOKEN_CONCAT (..), TOKEN_LEN (#)
TOKEN_PLUSASSIGN (+=), TOKEN_SUBASSIGN (-=) 等

// 分隔符
TOKEN_LPAREN, TOKEN_RPAREN, TOKEN_LBRACE, TOKEN_RBRACE
TOKEN_LBRACKET, TOKEN_RBRACKET, TOKEN_COMMA, TOKEN_SEMICOLON
TOKEN_DOT, TOKEN_DOTDOT, TOKEN_DOTDOTDOT, TOKEN_COLON

// 特殊
TOKEN_EOF, TOKEN_NEWLINE (用于语句分隔)
```

---

## 5. Parser / AST 设计

### AST 节点

```go
// 语句
type AssignStmt      // a, b = expr, expr
type DeclareStmt     // a, b := expr, expr
type CallStmt        // f(args)
type IfStmt          // if cond { } else if { } else { }
type ForNumStmt      // for i := 0; i < n; i++ { }
type ForRangeStmt    // for k, v := range t { }
type ForStmt         // for cond { }  (while)
type ReturnStmt      // return expr, expr
type BreakStmt
type ContinueStmt
type FuncDecl        // func name(params) { body }

// 表达式
type NumberLit
type StringLit
type BoolLit
type NilLit
type VarArgExpr      // ...
type IdentExpr
type BinaryExpr      // a + b
type UnaryExpr       // -a, !a, #a
type IndexExpr       // t[k]
type FieldExpr       // t.k
type CallExpr        // f(args)
type FuncLit         // func(params) { body }
type TableLit        // {k: v, ...}
```

---

## 6. Runtime 执行模型

### 6.1 Interpreter

```go
type Interpreter struct {
    globals *Environment
    current *Coroutine   // 当前运行的协程（nil 表示主线程）
}
```

### 6.2 多返回值处理

函数调用返回 `[]Value`。
在赋值语句中，自动展开或截断。
在表达式末尾位置（如函数调用的最后一个参数），自动展开。

### 6.3 错误处理

使用 Go panic/recover 实现 pcall。
定义：
```go
type LuaError struct {
    value Value
}
```

---

## 7. 开发里程碑（TDD）

### Phase 1: Lexer
- 实现所有 Token
- 测试：完整 token 流

### Phase 2: Parser + AST
- 递归下降解析器
- 测试：parse 每种语法结构

### Phase 3: 基本解释器
- 算术、逻辑、变量、if、for
- 测试：基本运算

### Phase 4: 函数 + 多返回值
- 函数定义/调用
- 多返回值展开

### Phase 5: Table
- Table 字面量、读写、嵌套

### Phase 6: Closure + Upvalue
- 闭包捕获、upvalue 共享、close

### Phase 7: Metatable
- 所有元方法

### Phase 8: Coroutine
- create/resume/yield
- Go goroutine + channel 实现

### Phase 9: 标准库
- print, type, tostring, tonumber
- string.*, table.*, math.*, io.*

### Phase 10: 错误处理
- pcall, xpcall, error

### Phase 11: 模块系统
- require, dofile

### Phase 12: Web Demo

---

## 8. 文件结构

```
gscript/
  cmd/gscript/
    main.go             入口
  internal/
    lexer/
      lexer.go          词法分析器
      token.go          Token 定义
      lexer_test.go     词法测试
    parser/
      parser.go         递归下降解析
      parser_test.go    解析测试
    ast/
      ast.go            AST 节点定义
    runtime/
      value.go          Value 类型
      table.go          Table 实现
      closure.go        Closure/Upvalue
      coroutine.go      Coroutine
      environment.go    Environment
      interpreter.go    树遍历解释器
      interpreter_test.go
    stdlib/
      base.go           print, type, tostring 等
      string.go         string.*
      table.go          table.*
      math.go           math.*
      io.go             io.*
      http.go           http.* (Web demo)
  tests/
    basic_test.gs
    closure_test.gs
    metatable_test.gs
    coroutine_test.gs
    ...
  examples/
    fib.gs
    webserver.gs
```

---

## 9. 关键算法

### 9.1 Upvalue 实现

变量声明时分配 `*Value` 指针。
闭包捕获时共享同一指针。
函数返回时，将栈上 Value 拷贝到堆（close upvalue）。

### 9.2 Coroutine 实现

每个 coroutine 运行在独立 goroutine，用两个 channel 通信：
- `resume chan []Value`：主线程传参数给协程
- `yield chan []Value`：协程传值给主线程

resume/yield 对应 channel send/receive，天然同步。

### 9.3 Metatable dispatch

访问 `t[k]`：
1. 先查 raw table
2. 若不存在，查 metatable.__index
3. 若 __index 是 table，递归查找
4. 若 __index 是 function，调用它

---

## 10. 验收标准

- [ ] `gscript main.gs` 正常执行
- [ ] Lua 测试改写版通过率 ≥ 95%
- [ ] Web server demo 正常运行
