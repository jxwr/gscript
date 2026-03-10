package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func httpLib(interp *Interpreter) *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "http." + name,
			Fn:   fn,
		}))
	}

	// http.listen(addr, handler)
	// handler is called with (req, res) for each request
	// This BLOCKS until the server stops
	set("listen", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("http.listen requires address and handler")
		}
		addr := args[0].Str()
		handler := args[1]

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Build req table
			req := buildRequestTable(r)
			// Build res table
			res := buildResponseTable(w, r)

			// Call handler
			_, err := interp.callFunction(handler, []Value{req, res})
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		})

		fmt.Printf("GScript HTTP server listening on %s\n", addr)
		return nil, http.ListenAndServe(addr, mux)
	})

	// http.get(url) - simple HTTP GET client
	set("get", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("http.get requires a URL")
		}
		url := args[0].Str()
		resp, err := http.Get(url)
		if err != nil {
			return []Value{NilValue(), StringValue(err.Error())}, nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		result := NewTable()
		result.RawSet(StringValue("status"), IntValue(int64(resp.StatusCode)))
		result.RawSet(StringValue("body"), StringValue(string(body)))
		// Headers
		headers := NewTable()
		for k, v := range resp.Header {
			headers.RawSet(StringValue(k), StringValue(strings.Join(v, ", ")))
		}
		result.RawSet(StringValue("headers"), TableValue(headers))
		return []Value{TableValue(result)}, nil
	})

	// http.newRouter() - creates a router with route registration
	set("newRouter", func(args []Value) ([]Value, error) {
		return []Value{TableValue(buildRouterTable(interp))}, nil
	})

	return t
}

// buildRequestTable creates a GScript table representing an HTTP request.
func buildRequestTable(r *http.Request) Value {
	t := NewTable()

	t.RawSet(StringValue("method"), StringValue(r.Method))
	t.RawSet(StringValue("path"), StringValue(r.URL.Path))
	t.RawSet(StringValue("url"), StringValue(r.URL.String()))

	// Query params as table
	query := NewTable()
	for k, v := range r.URL.Query() {
		query.RawSet(StringValue(k), StringValue(strings.Join(v, ", ")))
	}
	t.RawSet(StringValue("query"), TableValue(query))

	// Headers as table
	headers := NewTable()
	for k, v := range r.Header {
		headers.RawSet(StringValue(k), StringValue(strings.Join(v, ", ")))
	}
	t.RawSet(StringValue("headers"), TableValue(headers))

	// Body
	body, _ := io.ReadAll(r.Body)
	t.RawSet(StringValue("body"), StringValue(string(body)))

	// req.param(name) - get query param
	t.RawSet(StringValue("param"), FunctionValue(&GoFunction{
		Name: "req.param",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return []Value{NilValue()}, nil
			}
			val := r.URL.Query().Get(args[0].Str())
			if val == "" {
				return []Value{NilValue()}, nil
			}
			return []Value{StringValue(val)}, nil
		},
	}))

	// req.json() - parse body as JSON into a table
	t.RawSet(StringValue("json"), FunctionValue(&GoFunction{
		Name: "req.json",
		Fn: func(args []Value) ([]Value, error) {
			var data interface{}
			if err := json.Unmarshal(body, &data); err != nil {
				return []Value{NilValue(), StringValue(err.Error())}, nil
			}
			return []Value{goToGScript(data)}, nil
		},
	}))

	return TableValue(t)
}

// buildResponseTable creates a GScript table representing an HTTP response writer.
func buildResponseTable(w http.ResponseWriter, r *http.Request) Value {
	t := NewTable()
	written := false
	statusSet := false

	// res.write(body) - write response body
	t.RawSet(StringValue("write"), FunctionValue(&GoFunction{
		Name: "res.write",
		Fn: func(args []Value) ([]Value, error) {
			if !statusSet {
				w.WriteHeader(200)
				statusSet = true
			}
			if len(args) > 0 {
				written = true
				fmt.Fprint(w, args[0].String())
			}
			return nil, nil
		},
	}))

	// res.writeln(body) - write with newline
	t.RawSet(StringValue("writeln"), FunctionValue(&GoFunction{
		Name: "res.writeln",
		Fn: func(args []Value) ([]Value, error) {
			if !statusSet {
				w.WriteHeader(200)
				statusSet = true
			}
			if len(args) > 0 {
				written = true
				fmt.Fprintln(w, args[0].String())
			}
			return nil, nil
		},
	}))

	// res.json(value) - write JSON response
	t.RawSet(StringValue("json"), FunctionValue(&GoFunction{
		Name: "res.json",
		Fn: func(args []Value) ([]Value, error) {
			w.Header().Set("Content-Type", "application/json")
			if !statusSet {
				w.WriteHeader(200)
				statusSet = true
			}
			if len(args) > 0 {
				data := gscriptToGo(args[0])
				jsonBytes, err := json.Marshal(data)
				if err != nil {
					return nil, err
				}
				written = true
				w.Write(jsonBytes)
			}
			return nil, nil
		},
	}))

	// res.status(code) - set status code (must call before write)
	t.RawSet(StringValue("status"), FunctionValue(&GoFunction{
		Name: "res.status",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) > 0 && !written {
				code := int(args[0].Int())
				w.WriteHeader(code)
				statusSet = true
			}
			return []Value{TableValue(t)}, nil // return res for chaining
		},
	}))

	// res.header(key, value) - set response header
	t.RawSet(StringValue("header"), FunctionValue(&GoFunction{
		Name: "res.header",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) >= 2 {
				w.Header().Set(args[0].Str(), args[1].Str())
			}
			return []Value{TableValue(t)}, nil // return res for chaining
		},
	}))

	// res.redirect(url [, code]) - redirect
	t.RawSet(StringValue("redirect"), FunctionValue(&GoFunction{
		Name: "res.redirect",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, nil
			}
			code := 302
			if len(args) >= 2 {
				code = int(args[1].Int())
			}
			http.Redirect(w, r, args[0].Str(), code)
			return nil, nil
		},
	}))

	_ = written
	return TableValue(t)
}

// buildRouterTable creates a router with route registration.
func buildRouterTable(interp *Interpreter) *Table {
	t := NewTable()
	mux := http.NewServeMux()

	registerRoute := func(method, pattern string, handler Value) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if method != "" && r.Method != method {
				http.Error(w, "Method Not Allowed", 405)
				return
			}
			req := buildRequestTable(r)
			res := buildResponseTable(w, r)
			interp.callFunction(handler, []Value{req, res})
		})
	}

	// router.get(pattern, handler)
	t.RawSet(StringValue("get"), FunctionValue(&GoFunction{
		Name: "router.get",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) >= 2 {
				registerRoute("GET", args[0].Str(), args[1])
			}
			return []Value{TableValue(t)}, nil
		},
	}))

	// router.post(pattern, handler)
	t.RawSet(StringValue("post"), FunctionValue(&GoFunction{
		Name: "router.post",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) >= 2 {
				registerRoute("POST", args[0].Str(), args[1])
			}
			return []Value{TableValue(t)}, nil
		},
	}))

	// router.any(pattern, handler)
	t.RawSet(StringValue("any"), FunctionValue(&GoFunction{
		Name: "router.any",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) >= 2 {
				registerRoute("", args[0].Str(), args[1])
			}
			return []Value{TableValue(t)}, nil
		},
	}))

	// router.listen(addr)
	t.RawSet(StringValue("listen"), FunctionValue(&GoFunction{
		Name: "router.listen",
		Fn: func(args []Value) ([]Value, error) {
			addr := ":8080"
			if len(args) >= 1 {
				addr = args[0].Str()
			}
			fmt.Printf("GScript HTTP server listening on %s\n", addr)
			return nil, http.ListenAndServe(addr, mux)
		},
	}))

	return t
}

// goToGScript converts Go values (from JSON unmarshal) to GScript Values.
func goToGScript(v interface{}) Value {
	switch val := v.(type) {
	case nil:
		return NilValue()
	case bool:
		return BoolValue(val)
	case float64:
		return FloatValue(val)
	case string:
		return StringValue(val)
	case []interface{}:
		t := NewTable()
		for i, item := range val {
			t.RawSet(IntValue(int64(i+1)), goToGScript(item))
		}
		return TableValue(t)
	case map[string]interface{}:
		t := NewTable()
		for k, item := range val {
			t.RawSet(StringValue(k), goToGScript(item))
		}
		return TableValue(t)
	default:
		return StringValue(fmt.Sprintf("%v", val))
	}
}

// gscriptToGo converts GScript Values to Go values (for JSON marshal).
func gscriptToGo(v Value) interface{} {
	switch v.Type() {
	case TypeNil:
		return nil
	case TypeBool:
		return v.Bool()
	case TypeInt:
		return v.Int()
	case TypeFloat:
		return v.Number()
	case TypeString:
		return v.Str()
	case TypeTable:
		t := v.Table()
		// Check if it's array-like
		length := t.Length()
		if length > 0 {
			arr := make([]interface{}, length)
			for i := 1; i <= length; i++ {
				arr[i-1] = gscriptToGo(t.RawGet(IntValue(int64(i))))
			}
			return arr
		}
		// Hash map
		m := make(map[string]interface{})
		key := NilValue()
		for {
			k, val, ok := t.Next(key)
			if !ok {
				break
			}
			m[k.String()] = gscriptToGo(val)
			key = k
		}
		return m
	default:
		return v.String()
	}
}
