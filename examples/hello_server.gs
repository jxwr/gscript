// Minimal web server
// Run: ./gscript examples/hello_server.gs

http.listen(":8080", func(req, res) {
    res.write("Hello from GScript!\n")
    res.write("Method: " .. req.method .. "\n")
    res.write("Path: " .. req.path .. "\n")
})
