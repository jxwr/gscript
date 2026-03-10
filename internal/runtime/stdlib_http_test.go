package runtime

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPBuildRequestTable(t *testing.T) {
	// Create a fake HTTP request
	req := httptest.NewRequest("GET", "/hello?name=world&foo=bar", nil)
	req.Header.Set("Content-Type", "text/plain")

	val := buildRequestTable(req)
	if !val.IsTable() {
		t.Fatalf("expected table, got %s", val.TypeName())
	}
	tbl := val.Table()

	// Check method
	method := tbl.RawGet(StringValue("method"))
	if method.Str() != "GET" {
		t.Errorf("expected method=GET, got %s", method.Str())
	}

	// Check path
	path := tbl.RawGet(StringValue("path"))
	if path.Str() != "/hello" {
		t.Errorf("expected path=/hello, got %s", path.Str())
	}

	// Check query params table
	query := tbl.RawGet(StringValue("query"))
	if !query.IsTable() {
		t.Fatalf("expected query to be table, got %s", query.TypeName())
	}
	nameParam := query.Table().RawGet(StringValue("name"))
	if nameParam.Str() != "world" {
		t.Errorf("expected query.name=world, got %s", nameParam.Str())
	}

	// Check param function
	paramFn := tbl.RawGet(StringValue("param"))
	if !paramFn.IsFunction() {
		t.Fatalf("expected param to be function, got %s", paramFn.TypeName())
	}
	results, err := paramFn.GoFunction().Fn([]Value{StringValue("name")})
	if err != nil {
		t.Fatalf("param() error: %v", err)
	}
	if len(results) == 0 || results[0].Str() != "world" {
		t.Errorf("expected param('name')='world', got %v", results)
	}

	// Check param for missing key
	results, _ = paramFn.GoFunction().Fn([]Value{StringValue("missing")})
	if len(results) == 0 || !results[0].IsNil() {
		t.Errorf("expected param('missing')=nil, got %v", results)
	}
}

func TestHTTPBuildResponseTable(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	val := buildResponseTable(w, req)
	if !val.IsTable() {
		t.Fatalf("expected table, got %s", val.TypeName())
	}
	tbl := val.Table()

	// Test res.header
	headerFn := tbl.RawGet(StringValue("header")).GoFunction()
	headerFn.Fn([]Value{StringValue("X-Custom"), StringValue("test-value")})

	// Test res.write
	writeFn := tbl.RawGet(StringValue("write")).GoFunction()
	writeFn.Fn([]Value{StringValue("hello world")})

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", string(body))
	}
	if resp.Header.Get("X-Custom") != "test-value" {
		t.Errorf("expected X-Custom header, got %v", resp.Header)
	}
}

func TestHTTPResponseJSON(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	val := buildResponseTable(w, req)
	tbl := val.Table()

	// Build a GScript table to serialize
	data := NewTable()
	data.RawSet(StringValue("name"), StringValue("GScript"))
	data.RawSet(StringValue("version"), IntValue(1))

	jsonFn := tbl.RawGet(StringValue("json")).GoFunction()
	_, err := jsonFn.Fn([]Value{TableValue(data)})
	if err != nil {
		t.Fatalf("res.json() error: %v", err)
	}

	resp := w.Result()
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type: application/json, got %s", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if parsed["name"] != "GScript" {
		t.Errorf("expected name=GScript, got %v", parsed["name"])
	}
}

func TestHTTPResponseStatus(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	val := buildResponseTable(w, req)
	tbl := val.Table()

	statusFn := tbl.RawGet(StringValue("status")).GoFunction()
	results, _ := statusFn.Fn([]Value{IntValue(404)})

	// Should return the res table for chaining
	if len(results) == 0 || !results[0].IsTable() {
		t.Errorf("expected status() to return table for chaining")
	}

	writeFn := tbl.RawGet(StringValue("write")).GoFunction()
	writeFn.Fn([]Value{StringValue("not found")})

	resp := w.Result()
	if resp.StatusCode != 404 {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHTTPEndToEnd(t *testing.T) {
	// Create an interpreter and use http library to handle a request
	interp := New()

	// Get the buildResponseTable and buildRequestTable via a real handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqVal := buildRequestTable(r)
		resVal := buildResponseTable(w, r)

		// Simulate calling: res.write("Hello " .. req.method)
		reqTbl := reqVal.Table()
		resTbl := resVal.Table()

		method := reqTbl.RawGet(StringValue("method"))
		writeFn := resTbl.RawGet(StringValue("write")).GoFunction()
		writeFn.Fn([]Value{StringValue("Hello " + method.Str())})
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/test")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Hello GET" {
		t.Errorf("expected 'Hello GET', got '%s'", string(body))
	}

	_ = interp // used to ensure the test is in the right package
}

func TestHTTPEndToEndWithInterpreter(t *testing.T) {
	// Test that the interpreter can call a GScript handler function
	interp := New()

	// Create a GScript handler function as a GoFunction for simplicity
	handlerFn := FunctionValue(&GoFunction{
		Name: "testHandler",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, nil
			}
			reqTbl := args[0].Table()
			resTbl := args[1].Table()

			path := reqTbl.RawGet(StringValue("path"))
			writeFn := resTbl.RawGet(StringValue("write")).GoFunction()
			writeFn.Fn([]Value{StringValue("Path: " + path.Str())})
			return nil, nil
		},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := buildRequestTable(r)
		res := buildResponseTable(w, r)
		interp.callFunction(handlerFn, []Value{req, res})
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/mypath")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Path: /mypath" {
		t.Errorf("expected 'Path: /mypath', got '%s'", string(body))
	}
}
