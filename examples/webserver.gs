// GScript Web Server Demo
// Run: ./gscript examples/webserver.gs

router := http.newRouter()

// Counter state
counter := 0

// GET / - welcome page
router.get("/", func(req, res) {
    res.header("Content-Type", "text/html")
    res.write("<html><body>")
    res.write("<h1>Welcome to GScript Web Server!</h1>")
    res.write("<ul>")
    res.write("<li><a href='/hello'>Say Hello</a></li>")
    res.write("<li><a href='/counter'>Counter</a></li>")
    res.write("<li><a href='/json'>JSON API</a></li>")
    res.write("<li><a href='/echo?msg=hello'>Echo</a></li>")
    res.write("</ul>")
    res.write("</body></html>")
})

// GET /hello - hello world
router.get("/hello", func(req, res) {
    name := req.param("name")
    if name == nil {
        name = "World"
    }
    res.write("Hello, " .. name .. "!")
})

// GET /counter - increment and show counter
router.get("/counter", func(req, res) {
    counter = counter + 1
    res.write("Counter: " .. counter)
})

// GET /json - return JSON
router.get("/json", func(req, res) {
    data := {
        language: "GScript",
        version: "0.1.0",
        features: {"closures", "metatables", "coroutines"},
        counter: counter
    }
    res.json(data)
})

// GET /echo - echo query param
router.get("/echo", func(req, res) {
    msg := req.param("msg")
    if msg == nil {
        msg = "(no message)"
    }
    res.write("Echo: " .. msg)
})

// GET /fib - compute fibonacci
router.get("/fib", func(req, res) {
    n_str := req.param("n")
    n := 10
    if n_str != nil {
        n = tonumber(n_str)
    }

    func fib(x) {
        if x < 2 { return x }
        return fib(x-1) + fib(x-2)
    }

    result := fib(n)
    res.write("fib(" .. n .. ") = " .. result)
})

print("Starting GScript web server...")
print("Visit http://localhost:8080")
router.listen(":8080")
