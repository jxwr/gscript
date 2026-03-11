// error_handling.gs - Error handling patterns in GScript
// Demonstrates: result type, error chaining, retry, custom error types, cleanup

print("=== Error Handling Patterns ===")
print()

// -------------------------------------------------------
// 1. Result type pattern (ok, err)
// -------------------------------------------------------
print("--- Result Type Pattern ---")

// Functions return (value, nil) on success, (nil, error) on failure
func divide(a, b) {
    if b == 0 {
        return nil, "division by zero"
    }
    return a / b, nil
}

func safeSqrt(x) {
    if x < 0 {
        return nil, "cannot take square root of negative number"
    }
    return math.sqrt(x), nil
}

func parseInt(s) {
    n := tonumber(s)
    if n == nil {
        return nil, "invalid number: " .. tostring(s)
    }
    return n, nil
}

// Using result types
result, err := divide(10, 3)
print("  divide(10, 3):", result, "err:", err)

result, err = divide(10, 0)
print("  divide(10, 0):", result, "err:", err)

result, err = safeSqrt(16)
print("  safeSqrt(16):", result, "err:", err)

result, err = safeSqrt(-4)
print("  safeSqrt(-4):", result, "err:", err)

result, err = parseInt("42")
print("  parseInt('42'):", result, "err:", err)

result, err = parseInt("abc")
print("  parseInt('abc'):", result, "err:", err)
print()

// -------------------------------------------------------
// 2. Error chaining
// -------------------------------------------------------
print("--- Error Chaining ---")

// Chain operations that can fail, stopping at first error
func chainOps(value, ...) {
    ops := {...}
    current := value
    for i := 1; i <= #ops; i++ {
        result, err := ops[i](current)
        if err != nil {
            return nil, "step " .. tostring(i) .. ": " .. err
        }
        current = result
    }
    return current, nil
}

// Define a pipeline of operations
result, err = chainOps("16",
    func(s) { return parseInt(s) },
    func(n) { return safeSqrt(n) },
    func(n) { return divide(100, n) }
)
print("  Chain('16' -> parseInt -> sqrt -> 100/x):", result, "err:", err)

result, err = chainOps("abc",
    func(s) { return parseInt(s) },
    func(n) { return safeSqrt(n) },
    func(n) { return divide(100, n) }
)
print("  Chain('abc' -> parseInt -> sqrt -> 100/x):", result, "err:", err)

result, err = chainOps("-9",
    func(s) { return parseInt(s) },
    func(n) { return safeSqrt(n) },
    func(n) { return divide(100, n) }
)
print("  Chain('-9' -> parseInt -> sqrt -> 100/x):", result, "err:", err)
print()

// -------------------------------------------------------
// 3. Retry with exponential backoff simulation
// -------------------------------------------------------
print("--- Retry Pattern ---")

// Simulate a flaky operation that fails some number of times before succeeding
func flakyOperation(maxFailures) {
    attempts := 0
    return func() {
        attempts = attempts + 1
        if attempts <= maxFailures {
            return nil, "temporary failure (attempt " .. tostring(attempts) .. ")"
        }
        return "success after " .. tostring(attempts) .. " attempts", nil
    }
}

// Retry with simulated exponential backoff
func retry(operation, maxRetries) {
    delay := 1
    for attempt := 1; attempt <= maxRetries; attempt++ {
        result, err := operation()
        if err == nil {
            return result, nil
        }
        print(string.format("    Attempt %d failed: %s (would wait %d units)", attempt, err, delay))
        delay = delay * 2
    }
    return nil, "max retries exceeded"
}

print("  Retrying a flaky operation (fails 3 times, max 5 retries):")
op := flakyOperation(3)
result, err = retry(op, 5)
print("  Result:", result, "err:", err)
print()

print("  Retrying a very flaky operation (fails 10 times, max 3 retries):")
op2 := flakyOperation(10)
result, err = retry(op2, 3)
print("  Result:", result, "err:", err)
print()

// -------------------------------------------------------
// 4. Custom error types with metatables
// -------------------------------------------------------
print("--- Custom Error Types ---")

// Error base type
func newError(kind, message, details) {
    err := {
        kind: kind,
        message: message,
        details: details
    }

    err.toString = func() {
        s := "[" .. err.kind .. "] " .. err.message
        if err.details != nil {
            s = s .. " (" .. tostring(err.details) .. ")"
        }
        return s
    }

    err.isKind = func(k) {
        return err.kind == k
    }

    return err
}

// Specific error constructors
func validationError(field, message) {
    return newError("ValidationError", message, "field: " .. field)
}

func notFoundError(resource, id) {
    return newError("NotFoundError", resource .. " not found", "id: " .. tostring(id))
}

func permissionError(action) {
    return newError("PermissionError", "permission denied", "action: " .. action)
}

// Use custom errors
func findUser(id) {
    users := {
        {id: 1, name: "Alice", role: "admin"},
        {id: 2, name: "Bob", role: "user"}
    }
    for i := 1; i <= #users; i++ {
        if users[i].id == id {
            return users[i], nil
        }
    }
    return nil, notFoundError("User", id)
}

func updateUser(id, newName) {
    if #newName < 2 {
        return nil, validationError("name", "name must be at least 2 characters")
    }

    user, err := findUser(id)
    if err != nil {
        return nil, err
    }

    if user.role != "admin" {
        return nil, permissionError("updateUser")
    }

    user.name = newName
    return user, nil
}

// Test various error scenarios
testCases := {
    {id: 1, name: "NewAlice"},
    {id: 99, name: "Nobody"},
    {id: 2, name: "NewBob"},
    {id: 1, name: "X"}
}

for i := 1; i <= #testCases; i++ {
    tc := testCases[i]
    result, err = updateUser(tc.id, tc.name)
    if err != nil {
        print(string.format("  updateUser(%d, '%s'): ERROR: %s", tc.id, tc.name, err.toString()))
    } else {
        print(string.format("  updateUser(%d, '%s'): OK: %s", tc.id, tc.name, result.name))
    }
}
print()

// -------------------------------------------------------
// 5. Cleanup with pcall (try/finally pattern)
// -------------------------------------------------------
print("--- Cleanup with pcall ---")

// Resource that needs cleanup
func openResource(name) {
    print("    Opening resource: " .. name)
    return {
        name: name,
        closed: false,
        read: func() {
            return "data from " .. name
        },
        close: func() {
            print("    Closing resource: " .. name)
        }
    }
}

// try-finally pattern using pcall
func withResource(name, fn) {
    resource := openResource(name)
    ok, result := pcall(func() {
        return fn(resource)
    })
    // Always close the resource
    resource.close()

    if !ok {
        return nil, "error: " .. tostring(result)
    }
    return result, nil
}

// Successful case
result, err = withResource("database", func(r) {
    return r.read()
})
print("  Success:", result, "err:", err)

// Error case - resource still gets cleaned up
result, err = withResource("network", func(r) {
    error("connection timeout")
})
print("  Error:", result, "err:", err)
print()

// -------------------------------------------------------
// 6. Error accumulator (collect multiple errors)
// -------------------------------------------------------
print("--- Error Accumulator ---")

func newErrorCollector() {
    errors := {}
    ec := {}

    ec.add = func(err) {
        table.insert(errors, err)
    }

    ec.hasErrors = func() {
        return #errors > 0
    }

    ec.count = func() {
        return #errors
    }

    ec.getErrors = func() {
        return errors
    }

    ec.toString = func() {
        if #errors == 0 { return "no errors" }
        parts := {}
        for i := 1; i <= #errors; i++ {
            table.insert(parts, tostring(i) .. ". " .. tostring(errors[i]))
        }
        return #errors .. " error(s):\n    " .. table.concat(parts, "\n    ")
    }

    return ec
}

// Validate a form
func validateForm(data) {
    ec := newErrorCollector()

    if data.name == nil || #data.name == 0 {
        ec.add("name is required")
    } elseif #data.name < 2 {
        ec.add("name must be at least 2 characters")
    }

    if data.age == nil {
        ec.add("age is required")
    } elseif data.age < 0 || data.age > 150 {
        ec.add("age must be between 0 and 150")
    }

    if data.email == nil || #data.email == 0 {
        ec.add("email is required")
    } elseif string.find(data.email, "@") == nil {
        ec.add("email must contain @")
    }

    return ec
}

// Valid form
ec := validateForm({name: "Alice", age: 30, email: "alice@example.com"})
print("  Valid form:", ec.toString())

// Invalid form
ec = validateForm({name: "", age: 200, email: "not-an-email"})
print("  Invalid form:", ec.toString())

// Partially invalid
ec = validateForm({name: "Bob", age: 25, email: ""})
print("  Partial:", ec.toString())
print()

// -------------------------------------------------------
// 7. Panic/recover pattern with pcall
// -------------------------------------------------------
print("--- Panic/Recover ---")

func mustParse(s) {
    n := tonumber(s)
    if n == nil {
        error("mustParse: cannot parse '" .. s .. "' as number")
    }
    return n
}

// Recover from panics
func safeParse(s) {
    ok, result := pcall(func() {
        return mustParse(s)
    })
    if ok {
        return result, nil
    }
    return nil, tostring(result)
}

testStrings := {"42", "3.14", "hello", "", "0", "-17"}
for i := 1; i <= #testStrings; i++ {
    s := testStrings[i]
    result, err = safeParse(s)
    if err != nil {
        print(string.format("  safeParse('%s'): ERROR: %s", s, err))
    } else {
        print(string.format("  safeParse('%s'): OK: %s", s, tostring(result)))
    }
}
print()

print("=== Done ===")
