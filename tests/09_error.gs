// pcall catches errors
ok, err := pcall(func() {
    error("something went wrong")
})
print(ok)
print(err)

// pcall success
ok2, val := pcall(func() {
    return 42
})
print(ok2)
print(val)

// Error objects
ok3, e := pcall(func() {
    error({code: 404, msg: "not found"})
})
print(ok3)
print(e.code)
print(e.msg)

// assert
ok4, e2 := pcall(func() {
    assert(1 == 2, "math is broken")
})
print(ok4)
print(e2)
