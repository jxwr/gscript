# net

The `net` library provides HTTP client capabilities for making HTTP requests.

## Response Table

All request functions return a response table with these fields:

| Field        | Type     | Description                                |
|--------------|----------|--------------------------------------------|
| `status`     | int      | HTTP status code (e.g. 200, 404)           |
| `statusText` | string   | Full status string (e.g. "200 OK")         |
| `body`       | string   | Response body as a string                  |
| `headers`    | table    | Response headers (name -> value)           |
| `ok`         | bool     | `true` if status < 400                     |
| `json()`     | function | Parses body as JSON, returns GScript value |

## Options Table

The optional `opts` parameter for `get`, `post`, `put`, `delete`, and `patch` supports:

| Field             | Type   | Default | Description                      |
|-------------------|--------|---------|----------------------------------|
| `headers`         | table  | none    | Request headers (name -> value)  |
| `timeout`         | float  | 30      | Request timeout in seconds       |
| `followRedirects` | bool   | true    | Whether to follow HTTP redirects |

## Functions

### net.get(url [, opts])

Performs an HTTP GET request. Returns `response, nil` on success or `nil, errorMessage` on connection failure.

```
resp, err := net.get("https://api.example.com/data")
if err != nil {
    print("Error: " .. err)
} else {
    print(resp.status)  // 200
    print(resp.body)    // response body
}
```

With options:

```
hdrs := {}
hdrs["Authorization"] = "Bearer mytoken"
resp, err := net.get("https://api.example.com/data", {
    headers: hdrs,
    timeout: 10
})
```

### net.post(url, body [, opts])

Performs an HTTP POST request with the given body string.

```
resp, err := net.post("https://api.example.com/data", "{\"name\": \"test\"}")
print(resp.status)  // e.g. 201
```

### net.put(url, body [, opts])

Performs an HTTP PUT request with the given body string.

```
resp, err := net.put("https://api.example.com/data/1", "{\"name\": \"updated\"}")
```

### net.delete(url [, opts])

Performs an HTTP DELETE request.

```
resp, err := net.delete("https://api.example.com/data/1")
print(resp.status)  // e.g. 204
```

### net.patch(url, body [, opts])

Performs an HTTP PATCH request with the given body string.

```
resp, err := net.patch("https://api.example.com/data/1", "{\"name\": \"patched\"}")
```

### net.request(opts_table)

Performs a fully configurable HTTP request. The opts_table supports all standard options plus:

| Field    | Type   | Default | Description              |
|----------|--------|---------|--------------------------|
| `method` | string | "GET"   | HTTP method              |
| `url`    | string | (required) | Request URL           |
| `body`   | string | ""      | Request body             |

```
hdrs := {}
hdrs["Content-Type"] = "application/json"
resp, err := net.request({
    method: "POST",
    url: "https://api.example.com/data",
    headers: hdrs,
    body: "{\"key\": \"value\"}",
    timeout: 15
})
```

## Parsing JSON Responses

Use the `json()` method on the response to parse the body as JSON:

```
resp, err := net.get("https://api.example.com/users/1")
if resp.ok {
    data := resp.json()
    print(data.name)    // access parsed JSON fields
    print(data.email)
}
```

## Error Handling

HTTP status errors (4xx, 5xx) are not Go-level errors. The response is returned normally with `ok` set to `false`:

```
resp, err := net.get("https://api.example.com/missing")
if err != nil {
    // Connection-level error (DNS failure, timeout, etc.)
    print("Connection error: " .. err)
} elseif !resp.ok {
    // HTTP error status
    print("HTTP error: " .. resp.status)
    print(resp.body)
}
```
