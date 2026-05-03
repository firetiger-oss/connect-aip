package connectaip

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	testv1 "github.com/firetiger-oss/connect-aip/internal/testproto/testv1"
	"google.golang.org/protobuf/proto"
)

func TestForward(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/connectaip.test.v1.TestService/CreateResource" {
			t.Errorf("expected procedure path, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/proto" {
			t.Errorf("expected Content-Type: application/proto, got %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var req testv1.CreateResourceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetResourceId() != "test-id" {
			t.Errorf("expected resource_id test-id, got %s", req.GetResourceId())
		}

		resp := &testv1.CreateResourceResponse{
			Resource: &testv1.Resource{Name: "resources/test-id"},
		}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("POST", "/v1/resources", strings.NewReader(`{"resourceId":"test-id"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	Forward[*testv1.CreateResourceRequest, *testv1.CreateResourceResponse](
		w, req, "/connectaip.test.v1.TestService/CreateResource", handler,
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "resources/test-id") {
		t.Errorf("expected response to contain resource name, got %s", w.Body.String())
	}
}

func TestForwardWithPathVars(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req testv1.UpdateResourceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetResource().GetName() != "resources/my-cred" {
			t.Errorf("expected resource.name to be set, got %v", req.GetResource().GetName())
		}
		if req.GetResource().GetDescription() != "updated description" {
			t.Errorf("expected description to be preserved, got %v", req.GetResource().GetDescription())
		}

		resp := &testv1.UpdateResourceResponse{
			Resource: req.GetResource(),
		}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("PATCH", "/v1/resources/my-cred", strings.NewReader(`{"resource":{"description":"updated description"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ForwardWithPathVars[*testv1.UpdateResourceRequest, *testv1.UpdateResourceResponse](
		w, req, "/connectaip.test.v1.TestService/UpdateResource", handler,
		&testv1.UpdateResourceRequest{
			Resource: &testv1.Resource{Name: "resources/my-cred"},
		},
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardWithPathVarsEmptyBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req testv1.GetResourceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetName() != "resources/test" {
			t.Errorf("expected name to be set, got %v", req.GetName())
		}

		resp := &testv1.GetResourceResponse{
			Resource: &testv1.Resource{Name: req.GetName()},
		}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("GET", "/v1/resources/test", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	ForwardWithPathVars[*testv1.GetResourceRequest, *testv1.GetResourceResponse](
		w, req, "/connectaip.test.v1.TestService/GetResource", handler,
		&testv1.GetResourceRequest{Name: "resources/test"},
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardWithBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req testv1.GetResourceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetName() != "resources/test-id" {
			t.Errorf("expected name resources/test-id, got %v", req.GetName())
		}

		resp := &testv1.GetResourceResponse{
			Resource: &testv1.Resource{Name: req.GetName()},
		}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("GET", "/v1/resources/test-id", nil)
	w := httptest.NewRecorder()

	ForwardWithBody[*testv1.GetResourceRequest, *testv1.GetResourceResponse](
		w, req, "/connectaip.test.v1.TestService/GetResource", handler,
		&testv1.GetResourceRequest{Name: "resources/test-id"},
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardWithBodyQueryParams(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req testv1.ListResourcesRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetPageSize() != 10 {
			t.Errorf("expected pageSize 10, got %v", req.GetPageSize())
		}
		if req.GetFilter() != "active" {
			t.Errorf("expected filter active, got %v", req.GetFilter())
		}

		resp := &testv1.ListResourcesResponse{}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("GET", "/v1/resources?pageSize=10&filter=active", nil)
	w := httptest.NewRecorder()

	ForwardWithBody[*testv1.ListResourcesRequest, *testv1.ListResourcesResponse](
		w, req, "/connectaip.test.v1.TestService/ListResources", handler,
		&testv1.ListResourcesRequest{},
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardWithBodyMultipleQueryValuesIgnored(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req testv1.ListResourcesRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetFilter() != "" {
			t.Errorf("expected filter to be empty (multiple values ignored), got %v", req.GetFilter())
		}

		resp := &testv1.ListResourcesResponse{}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("GET", "/v1/resources?filter=a&filter=b", nil)
	w := httptest.NewRecorder()

	ForwardWithBody[*testv1.ListResourcesRequest, *testv1.ListResourcesResponse](
		w, req, "/connectaip.test.v1.TestService/ListResources", handler,
		&testv1.ListResourcesRequest{},
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestApplyQueryParams(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected *testv1.ListResourcesRequest
	}{
		{
			name:  "string field",
			query: "filter=active",
			expected: &testv1.ListResourcesRequest{
				Filter: "active",
			},
		},
		{
			name:  "int32 field",
			query: "pageSize=25",
			expected: &testv1.ListResourcesRequest{
				PageSize: 25,
			},
		},
		{
			name:  "multiple fields",
			query: "pageSize=10&filter=test&pageToken=abc",
			expected: &testv1.ListResourcesRequest{
				PageSize:  10,
				Filter:    "test",
				PageToken: "abc",
			},
		},
		{
			name:  "json name mapping",
			query: "pageSize=5&orderBy=name",
			expected: &testv1.ListResourcesRequest{
				PageSize: 5,
				OrderBy:  "name",
			},
		},
		{
			name:     "unknown field ignored",
			query:    "unknownField=value",
			expected: &testv1.ListResourcesRequest{},
		},
		{
			name:     "multiple values ignored",
			query:    "filter=a&filter=b",
			expected: &testv1.ListResourcesRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test?"+tt.query, nil)
			msg := &testv1.ListResourcesRequest{}
			applyQueryParams(msg, req.URL.Query())

			if msg.GetPageSize() != tt.expected.GetPageSize() {
				t.Errorf("PageSize: got %d, want %d", msg.GetPageSize(), tt.expected.GetPageSize())
			}
			if msg.GetPageToken() != tt.expected.GetPageToken() {
				t.Errorf("PageToken: got %s, want %s", msg.GetPageToken(), tt.expected.GetPageToken())
			}
			if msg.GetFilter() != tt.expected.GetFilter() {
				t.Errorf("Filter: got %s, want %s", msg.GetFilter(), tt.expected.GetFilter())
			}
			if msg.GetOrderBy() != tt.expected.GetOrderBy() {
				t.Errorf("OrderBy: got %s, want %s", msg.GetOrderBy(), tt.expected.GetOrderBy())
			}
		})
	}
}

func TestForwardWithBodyCombined(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req testv1.ListVersionsRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.GetName() != "resources/test" {
			t.Errorf("expected name from path vars, got %v", req.GetName())
		}
		if req.GetPageToken() != "token123" {
			t.Errorf("expected pageToken from query, got %v", req.GetPageToken())
		}

		resp := &testv1.ListVersionsResponse{}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("GET", "/v1/resources/test/versions?pageToken=token123", nil)
	w := httptest.NewRecorder()

	ForwardWithBody[*testv1.ListVersionsRequest, *testv1.ListVersionsResponse](
		w, req, "/connectaip.test.v1.TestService/ListVersions", handler,
		&testv1.ListVersionsRequest{Name: "resources/test"},
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardWithPathVarsReadError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("PATCH", "/v1/resources/test", &errorReader{})
	w := httptest.NewRecorder()

	ForwardWithPathVars[*testv1.UpdateResourceRequest, *testv1.UpdateResourceResponse](
		w, req, "/connectaip.test.v1.TestService/UpdateResource", handler,
		&testv1.UpdateResourceRequest{
			Resource: &testv1.Resource{Name: "resources/test"},
		},
	)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for read error, got %d", w.Code)
	}
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func (e *errorReader) Close() error {
	return nil
}

func TestForwardClearsQueryString(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected empty query string, got %s", r.URL.RawQuery)
		}

		resp := &testv1.CreateResourceResponse{}
		respBytes, _ := proto.Marshal(resp)
		w.Write(respBytes)
	})

	req := httptest.NewRequest("POST", "/v1/resources?unexpected=param", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	Forward[*testv1.CreateResourceRequest, *testv1.CreateResourceResponse](
		w, req, "/connectaip.test.v1.TestService/CreateResource", handler,
	)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardErrorResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"not_found","message":"resource not found"}`))
	})

	req := httptest.NewRequest("GET", "/v1/resources/missing", nil)
	w := httptest.NewRecorder()

	ForwardWithBody[*testv1.GetResourceRequest, *testv1.GetResourceResponse](
		w, req, "/connectaip.test.v1.TestService/GetResource", handler,
		&testv1.GetResourceRequest{Name: "resources/missing"},
	)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not_found") {
		t.Errorf("expected error body to be passed through, got %s", w.Body.String())
	}
}

func TestHandleNoTrailingSlashRedirectWithRestOfPathWildcard(t *testing.T) {
	mux := http.NewServeMux()

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handlers := func(yield func(string, http.Handler) bool) {
		if !yield("GET /v2/agents", okHandler) {
			return
		}
		if !yield("GET /v2/agents/{name...}", okHandler) {
			return
		}
	}

	Handle(mux, handlers)

	req := httptest.NewRequest("GET", "/v2/agents", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /v2/agents, got %d", w.Code)
	}
}
