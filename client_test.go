package connectaip

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

type testRequest struct {
	Name     string `json:"name"`
	PageSize int    `json:"pageSize,omitempty"`
	Filter   string `json:"filter,omitempty"`
}

type testResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

func TestClientGET(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/v1/items/test-id" {
			t.Errorf("expected /v1/items/test-id, got %s", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept: application/json, got %s", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "test-id", Message: "success"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items/{name}",
			PathVars:   []PathVar{{Placeholder: "{name}", Prefix: "items/"}},
		},
		func(req *testRequest) map[string]string {
			return map[string]string{"{name}": req.Name}
		},
		nil,
	)

	resp, err := client.Call(t.Context(), &testRequest{Name: "items/test-id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "test-id" {
		t.Errorf("expected ID test-id, got %s", resp.ID)
	}
}

func TestClientPOST(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/items" {
			t.Errorf("expected /v1/items, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var req testRequest
		json.Unmarshal(body, &req)
		if req.Name != "new-item" {
			t.Errorf("expected name new-item, got %s", req.Name)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "created-id", Message: "created"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "POST",
			URLPattern: "/v1/items",
		},
		nil,
		nil,
	)

	resp, err := client.Call(t.Context(), &testRequest{Name: "new-item"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "created-id" {
		t.Errorf("expected ID created-id, got %s", resp.ID)
	}
}

func TestClientWithQueryParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageSize") != "10" {
			t.Errorf("expected pageSize=10, got %s", r.URL.Query().Get("pageSize"))
		}
		if r.URL.Query().Get("filter") != "active" {
			t.Errorf("expected filter=active, got %s", r.URL.Query().Get("filter"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "list", Message: "filtered"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items",
		},
		nil,
		func(req *testRequest) map[string]string {
			result := make(map[string]string)
			if req.PageSize > 0 {
				result["pageSize"] = "10"
			}
			if req.Filter != "" {
				result["filter"] = req.Filter
			}
			return result
		},
	)

	resp, err := client.Call(t.Context(), &testRequest{PageSize: 10, Filter: "active"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "list" {
		t.Errorf("expected ID list, got %s", resp.ID)
	}
}

func TestClientWithHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Authorization header, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Custom-Header") != "custom-value" {
			t.Errorf("expected X-Custom-Header, got %s", r.Header.Get("X-Custom-Header"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "auth", Message: "authorized"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items",
		},
		nil,
		nil,
		WithHeader("Authorization", "Bearer test-token"),
		WithHeader("X-Custom-Header", "custom-value"),
	)

	resp, err := client.Call(t.Context(), &testRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "auth" {
		t.Errorf("expected ID auth, got %s", resp.ID)
	}
}

func TestClientErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found"}`))
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items/{name}",
			PathVars:   []PathVar{{Placeholder: "{name}"}},
		},
		func(req *testRequest) map[string]string {
			return map[string]string{"{name}": req.Name}
		},
		nil,
	)

	_, err := client.Call(t.Context(), &testRequest{Name: "missing"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %v", connect.CodeOf(err))
	}
}

func TestClientErrorResponseWithConnectCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"invalid_argument","message":"name is required"}`))
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{HTTPMethod: "POST", URLPattern: "/v1/items"},
		nil,
		nil,
	)

	_, err := client.Call(t.Context(), &testRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %v", connect.CodeOf(err))
	}
	if !contains(err.Error(), "name is required") {
		t.Errorf("expected error to contain message, got %s", err.Error())
	}
}

func TestClientWithInterceptor(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "intercepted"})
	}))
	defer server.Close()

	authInterceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer test-jwt")
			return next(ctx, req)
		}
	})

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{HTTPMethod: "POST", URLPattern: "/v1/items"},
		nil,
		nil,
		WithInterceptors(authInterceptor),
	)

	resp, err := client.Call(t.Context(), &testRequest{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "intercepted" {
		t.Errorf("expected ID intercepted, got %s", resp.ID)
	}
	if gotAuth != "Bearer test-jwt" {
		t.Errorf("expected Authorization header 'Bearer test-jwt', got %q", gotAuth)
	}
}

func TestClientInterceptorSeesIsClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "ok"})
	}))
	defer server.Close()

	var sawIsClient bool
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			sawIsClient = req.Spec().IsClient
			return next(ctx, req)
		}
	})

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{HTTPMethod: "GET", URLPattern: "/v1/items"},
		nil,
		nil,
		WithInterceptors(interceptor),
	)

	_, err := client.Call(t.Context(), &testRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sawIsClient {
		t.Error("interceptor did not see Spec().IsClient == true")
	}
}

func TestClientWithMultipleInterceptors(t *testing.T) {
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "ok"})
	}))
	defer server.Close()

	first := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("X-First", "1")
			return next(ctx, req)
		}
	})
	second := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("X-Second", "2")
			return next(ctx, req)
		}
	})

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{HTTPMethod: "GET", URLPattern: "/v1/items"},
		nil,
		nil,
		WithInterceptors(first, second),
	)

	_, err := client.Call(t.Context(), &testRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeaders.Get("X-First") != "1" {
		t.Errorf("expected X-First=1, got %q", gotHeaders.Get("X-First"))
	}
	if gotHeaders.Get("X-Second") != "2" {
		t.Errorf("expected X-Second=2, got %q", gotHeaders.Get("X-Second"))
	}
}

func TestClientBaseURLTrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/items" {
			t.Errorf("expected /v1/items, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "ok"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL+"/",
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items",
		},
		nil,
		nil,
	)

	_, err := client.Call(t.Context(), &testRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientPATCH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "updated"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "PATCH",
			URLPattern: "/v1/items/{name}",
			PathVars:   []PathVar{{Placeholder: "{name}"}},
		},
		func(req *testRequest) map[string]string {
			return map[string]string{"{name}": req.Name}
		},
		nil,
	)

	resp, err := client.Call(t.Context(), &testRequest{Name: "item-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "updated" {
		t.Errorf("expected ID updated, got %s", resp.ID)
	}
}

func TestClientContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testResponse{ID: "ok"})
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items",
		},
		nil,
		nil,
	)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := client.Call(ctx, &testRequest{})
	if err == nil {
		t.Fatal("expected error due to cancelled context")
	}
}

type unmarshalableRequest struct {
	Channel chan int `json:"channel"`
}

type unmarshalableResponse struct{}

func TestClientDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := NewClient[testRequest, testResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "GET",
			URLPattern: "/v1/items",
		},
		nil,
		nil,
	)

	_, err := client.Call(t.Context(), &testRequest{})
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decoding response") {
		t.Errorf("expected decoding error, got %s", err.Error())
	}
}

func TestClientMarshalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))
	defer server.Close()

	client := NewClient[unmarshalableRequest, unmarshalableResponse](
		server.Client(),
		server.URL,
		MethodSpec{
			HTTPMethod: "POST",
			URLPattern: "/v1/items",
		},
		nil,
		nil,
	)

	_, err := client.Call(t.Context(), &unmarshalableRequest{Channel: make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error, got nil")
	}
	if !strings.Contains(err.Error(), "marshaling request") {
		t.Errorf("expected marshaling error, got %s", err.Error())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
