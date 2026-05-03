package connectaip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"unsafe"

	"connectrpc.com/connect"
	connectsse "github.com/firetiger-oss/connect-sse"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// headerTransport wraps a connect.HTTPClient to inject static headers into every request.
type headerTransport struct {
	headers map[string]string
	next    connect.HTTPClient
}

func (t *headerTransport) Do(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.next.Do(req)
}

// pathVarSSETransport intercepts the outer SSE request (already built by connectsse.Client)
// and substitutes path variables extracted from the nested JSON message body.
// It must sit between connectsse.Client and the underlying transport so it sees the outer request.
type pathVarSSETransport struct {
	pathVarFn func(msgJSON []byte) iter.Seq2[string, string]
	next      connect.HTTPClient
}

func (t *pathVarSSETransport) Do(req *http.Request) (*http.Response, error) {
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read SSE request body: %w", err)
	}

	newPath := req.URL.Path
	var nested struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(bodyBytes, &nested) == nil && len(nested.Message) > 0 {
		for placeholder, val := range t.pathVarFn(nested.Message) {
			newPath = strings.Replace(newPath, placeholder, val, 1)
		}
	}

	req = req.Clone(req.Context())
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.ContentLength = int64(len(bodyBytes))
	if newPath != req.URL.Path {
		newURL := *req.URL
		newURL.Path = newPath
		newURL.RawPath = "" // recomputed from Path by net/http
		req.URL = &newURL
	}
	return t.next.Do(req)
}

// NewSSEClient creates a connect.HTTPClient suitable for server-streaming REST calls via SSE.
// rawURL is the full target URL (e.g. baseURL+"/v2/query"); url.Parse is used to decode it.
// pathVarFn, if non-nil, is called with the JSON-encoded message from each request to
// substitute path-variable placeholders (e.g. "{name}") in the URL.
func NewSSEClient(httpClient connect.HTTPClient, rawURL string, pathVarFn func([]byte) iter.Seq2[string, string], opts ...ClientOption) connect.HTTPClient {
	o := &clientOptions{}
	for _, opt := range opts {
		opt(o)
	}
	targetURL, _ := url.Parse(rawURL)
	var transport connect.HTTPClient = httpClient
	if len(o.interceptors) > 0 {
		transport = &interceptorTransport{interceptors: o.interceptors, next: transport}
	}
	if len(o.headers) > 0 {
		transport = &headerTransport{headers: o.headers, next: transport}
	}
	if pathVarFn != nil {
		transport = &pathVarSSETransport{pathVarFn: pathVarFn, next: transport}
	}
	return &connectsse.Client{
		URL:        targetURL,
		HTTPClient: transport,
	}
}

// interceptorTransport wraps a connect.HTTPClient to run interceptors before each request.
// This is used for SSE streaming clients where interceptors can't be applied at the
// Call() level (because connectsse.Client manages the HTTP request lifecycle).
type interceptorTransport struct {
	interceptors []connect.UnaryInterceptorFunc
	next         connect.HTTPClient
}

func (t *interceptorTransport) Do(req *http.Request) (*http.Response, error) {
	connectReq := connect.NewRequest[struct{}](nil)
	setRequestIsClient(connectReq)
	for k, vs := range req.Header {
		for _, v := range vs {
			connectReq.Header().Add(k, v)
		}
	}

	var httpResp *http.Response
	next := connect.UnaryFunc(func(ctx context.Context, anyReq connect.AnyRequest) (connect.AnyResponse, error) {
		req = req.WithContext(ctx)
		req.Header = anyReq.Header().Clone()
		var err error
		httpResp, err = t.next.Do(req)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse[struct{}](nil), nil
	})

	for i := len(t.interceptors) - 1; i >= 0; i-- {
		next = t.interceptors[i](next)
	}

	if _, err := next(req.Context(), connectReq); err != nil {
		return nil, err
	}
	return httpResp, nil
}

// SSEProcedureURL returns the URL to pass to connect.NewClient for server-streaming
// REST methods. It strips any path component from baseURL so that the Connect procedure
// path is not prefixed in the nested SSE request (which would break handler dispatch).
func SSEProcedureURL(baseURL, procedure string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.TrimSuffix(baseURL, "/") + procedure
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + u.Host + procedure
}

// ClientOption configures a Client.
type ClientOption func(*clientOptions)

type clientOptions struct {
	headers      map[string]string
	interceptors []connect.UnaryInterceptorFunc
}

// WithHeader adds a header to all requests made by the client.
func WithHeader(key, value string) ClientOption {
	return func(o *clientOptions) {
		if o.headers == nil {
			o.headers = make(map[string]string)
		}
		o.headers[key] = value
	}
}

// WithInterceptors adds interceptors that run before each request.
// Interceptors see a connect.AnyRequest with the proto message and HTTP headers,
// and can modify headers (e.g., inject Authorization) before the request is sent.
// This matches the ConnectRPC connect.WithInterceptors() pattern.
func WithInterceptors(interceptors ...connect.UnaryInterceptorFunc) ClientOption {
	return func(o *clientOptions) {
		o.interceptors = append(o.interceptors, interceptors...)
	}
}

// MethodSpec describes a REST method's HTTP configuration.
type MethodSpec struct {
	HTTPMethod string    // GET, POST, PATCH, DELETE, PUT
	URLPattern string    // e.g., "/v1/credentials/{name}"
	PathVars   []PathVar // path variable extraction info
}

// PathVar describes how to extract a path variable from a request.
type PathVar struct {
	Placeholder string // e.g., "{name}"
	Prefix      string // e.g., "credentials/" to strip from value
}

// Client is a generic REST client for a single method.
// It uses connect.HTTPClient for compatibility with ConnectRPC clients.
type Client[Req, Resp any] struct {
	httpClient connect.HTTPClient
	baseURL    string
	spec       MethodSpec
	opts       clientOptions
	pathVarFn  func(req *Req) map[string]string
	queryFn    func(req *Req) map[string]string
}

// NewClient creates a new REST method client.
func NewClient[Req, Resp any](
	httpClient connect.HTTPClient,
	baseURL string,
	spec MethodSpec,
	pathVarFn func(*Req) map[string]string,
	queryFn func(*Req) map[string]string,
	opts ...ClientOption,
) *Client[Req, Resp] {
	c := &Client[Req, Resp]{
		httpClient: httpClient,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		spec:       spec,
		pathVarFn:  pathVarFn,
		queryFn:    queryFn,
	}
	for _, opt := range opts {
		opt(&c.opts)
	}
	return c
}

// Call executes the REST request.
func (c *Client[Req, Resp]) Call(ctx context.Context, req *Req) (*Resp, error) {
	urlPath := c.spec.URLPattern

	if c.pathVarFn != nil {
		pathVars := c.pathVarFn(req)
		for _, pv := range c.spec.PathVars {
			if val, ok := pathVars[pv.Placeholder]; ok {
				if pv.Prefix != "" {
					val = strings.TrimPrefix(val, pv.Prefix)
				}
				urlPath = strings.Replace(urlPath, pv.Placeholder, val, 1)
			}
		}
	}

	fullURL := c.baseURL + urlPath

	if c.queryFn != nil {
		queryParams := c.queryFn(req)
		if len(queryParams) > 0 {
			query := url.Values{}
			for k, v := range queryParams {
				if v != "" {
					query.Set(k, v)
				}
			}
			if encoded := query.Encode(); encoded != "" {
				fullURL += "?" + encoded
			}
		}
	}

	var body io.Reader
	// Only send body for POST/PATCH/PUT when queryFn is nil (body-based route).
	// When queryFn is provided, fields are sent as query params instead.
	if c.queryFn == nil && (c.spec.HTTPMethod == "POST" || c.spec.HTTPMethod == "PATCH" || c.spec.HTTPMethod == "PUT") {
		var data []byte
		var err error
		if pm, ok := any(req).(proto.Message); ok {
			data, err = protojson.Marshal(pm)
		} else {
			data, err = json.Marshal(req)
		}
		if err != nil {
			return nil, fmt.Errorf("marshaling request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	httpReq, err := http.NewRequestWithContext(ctx, c.spec.HTTPMethod, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	for k, v := range c.opts.headers {
		httpReq.Header.Set(k, v)
	}

	if len(c.opts.interceptors) > 0 {
		return callWithInterceptors[Req, Resp](ctx, c.httpClient, httpReq, req, c.opts.interceptors)
	}
	return doHTTPCall[Resp](c.httpClient, httpReq)
}

// callWithInterceptors creates a connect.Request, runs interceptors, copies
// modified headers back to the HTTP request, then executes.
func callWithInterceptors[Req, Resp any](ctx context.Context, httpClient connect.HTTPClient, httpReq *http.Request, req *Req, interceptors []connect.UnaryInterceptorFunc) (*Resp, error) {
	connectReq := connect.NewRequest(req)
	setRequestIsClient(connectReq)
	for k, vs := range httpReq.Header {
		for _, v := range vs {
			connectReq.Header().Add(k, v)
		}
	}

	next := connect.UnaryFunc(func(ctx context.Context, anyReq connect.AnyRequest) (connect.AnyResponse, error) {
		httpReq = httpReq.WithContext(ctx)
		httpReq.Header = anyReq.Header().Clone()
		result, err := doHTTPCall[Resp](httpClient, httpReq)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(result), nil
	})

	for i := len(interceptors) - 1; i >= 0; i-- {
		next = interceptors[i](next)
	}

	anyResp, err := next(ctx, connectReq)
	if err != nil {
		return nil, err
	}
	resp, ok := anyResp.(*connect.Response[Resp])
	if !ok {
		return nil, fmt.Errorf("unexpected response type %T", anyResp)
	}
	return resp.Msg, nil
}

// setRequestIsClient sets Spec().IsClient = true on a connect.Request via unsafe
// access to the unexported spec field, so interceptors that check IsClient
// (like NewM2MAuthInterceptor) correctly identify REST client requests.
func setRequestIsClient[T any](req *connect.Request[T]) {
	rv := reflect.ValueOf(req).Elem()
	specField := rv.FieldByName("spec")
	if !specField.IsValid() {
		return
	}
	spec := (*connect.Spec)(unsafe.Pointer(specField.UnsafeAddr()))
	spec.IsClient = true
}

func doHTTPCall[Resp any](httpClient connect.HTTPClient, httpReq *http.Request) (*Resp, error) {
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, parseErrorResponse(resp.StatusCode, respBody)
	}

	var result Resp
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if pm, ok := any(&result).(proto.Message); ok {
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, pm); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}
	} else {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}
	}

	return &result, nil
}

// parseErrorResponse parses an HTTP error response body into a *connect.Error.
// The server returns JSON like {"code":"not_found","message":"resource not found"}.
// If the body can't be parsed, it falls back to mapping the HTTP status code.
func parseErrorResponse(statusCode int, body []byte) *connect.Error {
	var errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &errBody) == nil && errBody.Code != "" {
		if code, ok := connectCodeFromString(errBody.Code); ok {
			return connect.NewError(code, fmt.Errorf("%s", errBody.Message))
		}
	}
	return connect.NewError(httpStatusToConnectCode(statusCode), fmt.Errorf("%s", string(body)))
}

func httpStatusToConnectCode(status int) connect.Code {
	switch status {
	case http.StatusBadRequest:
		return connect.CodeInvalidArgument
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound:
		return connect.CodeNotFound
	case http.StatusConflict:
		return connect.CodeAlreadyExists
	case http.StatusPreconditionFailed:
		return connect.CodeFailedPrecondition
	case http.StatusTooManyRequests:
		return connect.CodeResourceExhausted
	case http.StatusNotImplemented:
		return connect.CodeUnimplemented
	case http.StatusServiceUnavailable:
		return connect.CodeUnavailable
	case http.StatusGatewayTimeout:
		return connect.CodeDeadlineExceeded
	default:
		return connect.CodeInternal
	}
}

var connectCodeStrings = map[string]connect.Code{
	"canceled":            connect.CodeCanceled,
	"unknown":             connect.CodeUnknown,
	"invalid_argument":    connect.CodeInvalidArgument,
	"deadline_exceeded":   connect.CodeDeadlineExceeded,
	"not_found":           connect.CodeNotFound,
	"already_exists":      connect.CodeAlreadyExists,
	"permission_denied":   connect.CodePermissionDenied,
	"resource_exhausted":  connect.CodeResourceExhausted,
	"failed_precondition": connect.CodeFailedPrecondition,
	"aborted":             connect.CodeAborted,
	"out_of_range":        connect.CodeOutOfRange,
	"unimplemented":       connect.CodeUnimplemented,
	"internal":            connect.CodeInternal,
	"unavailable":         connect.CodeUnavailable,
	"data_loss":           connect.CodeDataLoss,
	"unauthenticated":     connect.CodeUnauthenticated,
}

func connectCodeFromString(s string) (connect.Code, bool) {
	code, ok := connectCodeStrings[s]
	return code, ok
}

// SplitMultiWildcard extracts one segment from a multi-wildcard resource name.
// It trims prefix from val, splits on sep, and returns the element at idx.
// If the split produces fewer parts than idx+1 (malformed name), it returns "".
//
// This is used in generated PathVars helpers for patterns like {name=agents/*/slos/*}.
func SplitMultiWildcard(val, prefix, sep string, idx int) string {
	i := 0
	for part := range strings.SplitSeq(strings.TrimPrefix(val, prefix), sep) {
		if i == idx {
			return part
		}
		i++
	}
	return ""
}
