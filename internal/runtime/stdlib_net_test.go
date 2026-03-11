package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// netInterp creates an interpreter with the net library manually registered.
func netInterp(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithLib(t, src, "net", buildNetLib())
}

// ==================================================================
// Net library tests
// ==================================================================

func TestNetRegistered(t *testing.T) {
	interp := netInterp(t, `
		result := type(net)
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "table" {
		t.Errorf("expected net to be 'table', got %s", v.Str())
	}
}

func TestNetFunctions(t *testing.T) {
	interp := netInterp(t, `
		a := type(net.get)
		b := type(net.post)
		c := type(net.put)
		d := type(net.delete)
		e := type(net.patch)
		f := type(net.request)
	`)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		v := interp.GetGlobal(name)
		if v.Str() != "function" {
			t.Errorf("expected net function '%s' to be 'function', got '%s'", name, v.Str())
		}
	}
}

func TestNetGet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("X-Custom", "test-value")
		w.WriteHeader(200)
		w.Write([]byte("hello world"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.get("%s")
		status := resp.status
		body := resp.body
		statusText := resp.statusText
		ok := resp.ok
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}

	status := interp.GetGlobal("status")
	if !status.IsInt() || status.Int() != 200 {
		t.Errorf("expected status=200, got %v", status)
	}

	body := interp.GetGlobal("body")
	if body.Str() != "hello world" {
		t.Errorf("expected body='hello world', got '%s'", body.Str())
	}

	statusText := interp.GetGlobal("statusText")
	if !strings.Contains(statusText.Str(), "200") {
		t.Errorf("expected statusText to contain '200', got '%s'", statusText.Str())
	}

	ok := interp.GetGlobal("ok")
	if !ok.Bool() {
		t.Errorf("expected ok=true for status 200")
	}
}

func TestNetGetHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom request header was sent
		if r.Header.Get("X-Custom-Request") != "hello" {
			t.Errorf("expected custom request header, got '%s'", r.Header.Get("X-Custom-Request"))
		}
		w.Header().Set("X-Custom-Response", "world")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		hdrs := {}
		hdrs["X-Custom-Request"] = "hello"
		opts := {headers: hdrs}
		resp, err := net.get("%s", opts)
		status := resp.status
		headers := resp.headers
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}

	status := interp.GetGlobal("status")
	if status.Int() != 200 {
		t.Errorf("expected status=200, got %v", status)
	}

	headers := interp.GetGlobal("headers")
	if !headers.IsTable() {
		t.Errorf("expected headers to be table, got %s", headers.TypeName())
	}
}

func TestNetPost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		bodyStr := string(bodyBytes)
		w.WriteHeader(201)
		w.Write([]byte("received: " + bodyStr))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.post("%s", "test-body")
		status := resp.status
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}

	status := interp.GetGlobal("status")
	if status.Int() != 201 {
		t.Errorf("expected status=201, got %v", status)
	}

	body := interp.GetGlobal("body")
	if body.Str() != "received: test-body" {
		t.Errorf("expected body='received: test-body', got '%s'", body.Str())
	}
}

func TestNetPut(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(200)
		w.Write([]byte("updated"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.put("%s", "update-body")
		status := resp.status
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("status").Int() != 200 {
		t.Errorf("expected status=200, got %v", interp.GetGlobal("status"))
	}
	if interp.GetGlobal("body").Str() != "updated" {
		t.Errorf("expected body='updated', got '%s'", interp.GetGlobal("body").Str())
	}
}

func TestNetDelete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(204)
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.delete("%s")
		status := resp.status
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("status").Int() != 204 {
		t.Errorf("expected status=204, got %v", interp.GetGlobal("status"))
	}
}

func TestNetPatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		w.WriteHeader(200)
		w.Write([]byte("patched"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.patch("%s", "patch-body")
		status := resp.status
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("status").Int() != 200 {
		t.Errorf("expected status=200, got %v", interp.GetGlobal("status"))
	}
	if interp.GetGlobal("body").Str() != "patched" {
		t.Errorf("expected body='patched', got '%s'", interp.GetGlobal("body").Str())
	}
}

func TestNetRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		hdrs := {}
		hdrs["Content-Type"] = "application/json"
		resp, err := net.request({
			method: "POST",
			url: "%s",
			headers: hdrs,
			body: "{\"key\": \"value\"}"
		})
		status := resp.status
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("status").Int() != 200 {
		t.Errorf("expected status=200, got %v", interp.GetGlobal("status"))
	}
}

func TestNetResponseJson(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		data := map[string]interface{}{"name": "gscript", "version": 1.0}
		json.NewEncoder(w).Encode(data)
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.get("%s")
		data := resp.json()
		name := data.name
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	name := interp.GetGlobal("name")
	if name.Str() != "gscript" {
		t.Errorf("expected name='gscript', got '%s'", name.Str())
	}
}

func TestNetGetErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.get("%s")
		status := resp.status
		ok := resp.ok
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error (HTTP errors are not Go errors), got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("status").Int() != 404 {
		t.Errorf("expected status=404, got %v", interp.GetGlobal("status"))
	}
	if interp.GetGlobal("ok").Truthy() {
		t.Errorf("expected ok=false for status 404")
	}
	if interp.GetGlobal("body").Str() != "not found" {
		t.Errorf("expected body='not found', got '%s'", interp.GetGlobal("body").Str())
	}
}

func TestNetGetConnectionError(t *testing.T) {
	// Use an invalid URL to trigger a connection error
	src := `
		resp, err := net.get("http://127.0.0.1:1")
	`
	interp := netInterp(t, src)

	resp := interp.GetGlobal("resp")
	if !resp.IsNil() {
		t.Errorf("expected nil response on connection error, got %v", resp)
	}
	errVal := interp.GetGlobal("err")
	if errVal.IsNil() || !errVal.IsString() {
		t.Errorf("expected error string on connection error, got %v", errVal)
	}
}

func TestNetPostWithHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token123" {
			t.Errorf("expected Authorization header, got '%s'", r.Header.Get("Authorization"))
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		w.WriteHeader(200)
		w.Write(bodyBytes)
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		hdrs := {}
		hdrs["Authorization"] = "Bearer token123"
		opts := {headers: hdrs}
		resp, err := net.post("%s", "data", opts)
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("body").Str() != "data" {
		t.Errorf("expected body='data', got '%s'", interp.GetGlobal("body").Str())
	}
}

func TestNetRequestDefaultGet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET as default method, got %s", r.Method)
		}
		w.WriteHeader(200)
		w.Write([]byte("default-get"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.request({url: "%s"})
		body := resp.body
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}
	if interp.GetGlobal("body").Str() != "default-get" {
		t.Errorf("expected body='default-get', got '%s'", interp.GetGlobal("body").Str())
	}
}

func TestNetResponseHeaders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Header", "test-value")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	src := fmt.Sprintf(`
		resp, err := net.get("%s")
		headers := resp.headers
	`, ts.URL)

	interp := netInterp(t, src)

	if !interp.GetGlobal("err").IsNil() {
		t.Errorf("expected nil error, got %v", interp.GetGlobal("err"))
	}

	headers := interp.GetGlobal("headers")
	if !headers.IsTable() {
		t.Errorf("expected headers to be table, got %s", headers.TypeName())
	}
	// Go http normalizes headers to X-Test-Header
	hdrVal := headers.Table().RawGet(StringValue("X-Test-Header"))
	if hdrVal.IsNil() {
		// Try lowercase
		hdrVal = headers.Table().RawGet(StringValue("x-test-header"))
	}
	if hdrVal.Str() != "test-value" {
		t.Errorf("expected X-Test-Header='test-value', got '%s'", hdrVal.Str())
	}
}
