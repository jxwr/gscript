package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// buildNetLib creates the "net" standard library table for HTTP client operations.
func buildNetLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "net." + name,
			Fn:   fn,
		}))
	}

	// net.get(url [, opts]) -> response table or nil, errMsg
	set("get", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'net.get' (string expected)")
		}
		url := args[0].Str()
		var opts Value
		if len(args) >= 2 {
			opts = args[1]
		}
		return netDoRequest("GET", url, "", opts)
	})

	// net.post(url, body [, opts]) -> response table or nil, errMsg
	set("post", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'net.post' (url and body expected)")
		}
		url := args[0].Str()
		body := args[1].Str()
		var opts Value
		if len(args) >= 3 {
			opts = args[2]
		}
		return netDoRequest("POST", url, body, opts)
	})

	// net.put(url, body [, opts]) -> response table or nil, errMsg
	set("put", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'net.put' (url and body expected)")
		}
		url := args[0].Str()
		body := args[1].Str()
		var opts Value
		if len(args) >= 3 {
			opts = args[2]
		}
		return netDoRequest("PUT", url, body, opts)
	})

	// net.delete(url [, opts]) -> response table or nil, errMsg
	set("delete", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'net.delete' (string expected)")
		}
		url := args[0].Str()
		var opts Value
		if len(args) >= 2 {
			opts = args[1]
		}
		return netDoRequest("DELETE", url, "", opts)
	})

	// net.patch(url, body [, opts]) -> response table or nil, errMsg
	set("patch", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'net.patch' (url and body expected)")
		}
		url := args[0].Str()
		body := args[1].Str()
		var opts Value
		if len(args) >= 3 {
			opts = args[2]
		}
		return netDoRequest("PATCH", url, body, opts)
	})

	// net.request(opts_table) -> response table or nil, errMsg
	set("request", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'net.request' (table expected)")
		}
		optsTable := args[0].Table()

		method := "GET"
		methodVal := optsTable.RawGet(StringValue("method"))
		if methodVal.IsString() && methodVal.Str() != "" {
			method = strings.ToUpper(methodVal.Str())
		}

		urlVal := optsTable.RawGet(StringValue("url"))
		if !urlVal.IsString() || urlVal.Str() == "" {
			return nil, fmt.Errorf("net.request: 'url' field is required")
		}
		url := urlVal.Str()

		body := ""
		bodyVal := optsTable.RawGet(StringValue("body"))
		if bodyVal.IsString() {
			body = bodyVal.Str()
		}

		return netDoRequest(method, url, body, args[0])
	})

	return t
}

// netDoRequest performs an HTTP request and returns a GScript response table.
func netDoRequest(method, url, body string, opts Value) ([]Value, error) {
	// Build the request
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return []Value{NilValue(), StringValue(err.Error())}, nil
	}

	// Default timeout
	timeout := 30 * time.Second
	followRedirects := true

	// Parse opts if provided
	if opts.IsTable() {
		optsTable := opts.Table()

		// Headers
		headersVal := optsTable.RawGet(StringValue("headers"))
		if headersVal.IsTable() {
			hdrTable := headersVal.Table()
			key := NilValue()
			for {
				k, v, ok := hdrTable.Next(key)
				if !ok {
					break
				}
				if k.IsString() && v.IsString() {
					req.Header.Set(k.Str(), v.Str())
				}
				key = k
			}
		}

		// Timeout
		timeoutVal := optsTable.RawGet(StringValue("timeout"))
		if timeoutVal.IsFloat() || timeoutVal.IsInt() {
			timeout = time.Duration(toFloat(timeoutVal) * float64(time.Second))
		}

		// followRedirects
		followVal := optsTable.RawGet(StringValue("followRedirects"))
		if followVal.IsBool() {
			followRedirects = followVal.Bool()
		}
	}

	// Create client
	client := &http.Client{
		Timeout: timeout,
	}
	if !followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return []Value{NilValue(), StringValue(err.Error())}, nil
	}
	defer resp.Body.Close()

	// Read body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return []Value{NilValue(), StringValue(err.Error())}, nil
	}
	bodyStr := string(bodyBytes)

	// Build response table
	result := NewTable()
	result.RawSet(StringValue("status"), IntValue(int64(resp.StatusCode)))
	result.RawSet(StringValue("statusText"), StringValue(resp.Status))
	result.RawSet(StringValue("body"), StringValue(bodyStr))
	result.RawSet(StringValue("ok"), BoolValue(resp.StatusCode < 400))

	// Response headers
	headers := NewTable()
	for k, v := range resp.Header {
		headers.RawSet(StringValue(k), StringValue(strings.Join(v, ", ")))
	}
	result.RawSet(StringValue("headers"), TableValue(headers))

	// json() method - parses body as JSON
	result.RawSet(StringValue("json"), FunctionValue(&GoFunction{
		Name: "response.json",
		Fn: func(args []Value) ([]Value, error) {
			var data interface{}
			if err := json.Unmarshal(bodyBytes, &data); err != nil {
				return []Value{NilValue(), StringValue(err.Error())}, nil
			}
			return []Value{goToGScript(data)}, nil
		},
	}))

	return []Value{TableValue(result), NilValue()}, nil
}
