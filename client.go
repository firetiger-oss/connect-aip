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
//
// Interceptors registered via WithInterceptors are NOT applied here; for
// server-streaming clients they must be passed to connect.NewClient via
// ConnectClientOptions so connect-go runs them as streaming interceptors.
func NewSSEClient(httpClient connect.HTTPClient, rawURL string, pathVarFn func([]byte) iter.Seq2[string, string], opts ...ClientOption) connect.HTTPClient {
	o := &clientOptions{}
	for _, opt := range opts {
		opt(o)
	}
	targetURL, _ := url.Parse(rawURL)
	var transport connect.HTTPClient = httpClient
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
	interceptors []connect.Interceptor
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

// WithInterceptors adds connect.Interceptors that run before each request.
// Interceptors see a connect.AnyRequest with the proto message, HTTP headers,
// and populated Spec/Peer, and can modify headers (e.g., inject Authorization)
// before the request is sent. This matches the ConnectRPC
// connect.WithInterceptors() pattern and accepts full connect.Interceptors such
// as those from connectrpc.com/otelconnect. For unary methods the WrapUnary
// stage runs; for server-streaming methods the interceptors are applied by
// connect.NewClient (see ConnectClientOptions).
func WithInterceptors(interceptors ...connect.Interceptor) ClientOption {
	return func(o *clientOptions) {
		o.interceptors = append(o.interceptors, interceptors...)
	}
}

// ConnectClientOptions translates connectaip ClientOptions into connect
// ClientOptions for the server-streaming SSE client built with connect.NewClient.
// Interceptors registered via WithInterceptors are surfaced as
// connect.WithInterceptors so connect-go runs them as streaming interceptors.
func ConnectClientOptions(opts ...ClientOption) []connect.ClientOption {
	o := &clientOptions{}
	for _, opt := range opts {
		opt(o)
	}
	var out []connect.ClientOption
	if len(o.interceptors) > 0 {
		out = append(out, connect.WithInterceptors(o.interceptors...))
	}
	return out
}

// MethodSpec describes a REST method's HTTP configuration.
type MethodSpec struct {
	HTTPMethod string    // GET, POST, PATCH, DELETE, PUT
	URLPattern string    // e.g., "/v1/credentials/{name}"
	PathVars   []PathVar // path variable extraction info
	// Procedure is the Connect RPC procedure URL for this method
	// (e.g. "/example.v1.Service/Method"). When set, it populates
	// req.Spec().Procedure so unary interceptors can identify the RPC.
	Procedure string
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

// Call executes the REST request and returns the response message. Convenience
// wrapper around CallRequest for callers that don't need per-call headers or
// connect.Response wrapping.
func (c *Client[Req, Resp]) Call(ctx context.Context, req *Req) (*Resp, error) {
	resp, err := c.CallRequest(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// CallRequest executes the REST request, propagating headers from connectReq onto
// the outgoing HTTP request and populating response headers on the returned
// connect.Response. Static headers from WithHeader are applied first, then
// per-call headers from connectReq override them. Interceptors registered via
// WithInterceptors run with full visibility into the connect.Request.
func (c *Client[Req, Resp]) CallRequest(ctx context.Context, connectReq *connect.Request[Req]) (*connect.Response[Resp], error) {
	setRequestSpec(connectReq, c.spec.Procedure)
	if u, err := url.Parse(c.baseURL); err == nil {
		setRequestPeer(connectReq, u.Host)
	}
	req := connectReq.Msg

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
	hasBody := false
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
		hasBody = true
	}

	httpReq, err := http.NewRequestWithContext(ctx, c.spec.HTTPMethod, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Compose headers on the connect.Request so interceptors see the full set.
	// Static WithHeader values fill in defaults; existing connectReq headers win.
	if connectReq.Header().Get("Accept") == "" {
		connectReq.Header().Set("Accept", "application/json")
	}
	if hasBody && connectReq.Header().Get("Content-Type") == "" {
		connectReq.Header().Set("Content-Type", "application/json")
	}
	for k, v := range c.opts.headers {
		if connectReq.Header().Get(k) == "" {
			connectReq.Header().Set(k, v)
		}
	}

	httpClient := c.httpClient
	next := connect.UnaryFunc(func(ctx context.Context, anyReq connect.AnyRequest) (connect.AnyResponse, error) {
		httpReq = httpReq.WithContext(ctx)
		httpReq.Header = anyReq.Header().Clone()
		msg, respHeader, respTrailer, callErr := doHTTPCall[Resp](httpClient, httpReq)
		if callErr != nil {
			return nil, callErr
		}
		out := connect.NewResponse(msg)
		copyHeader(out.Header(), respHeader)
		copyHeader(out.Trailer(), respTrailer)
		return out, nil
	})

	for i := len(c.opts.interceptors) - 1; i >= 0; i-- {
		next = c.opts.interceptors[i].WrapUnary(next)
	}

	anyResp, err := next(ctx, connectReq)
	if err != nil {
		return nil, err
	}
	resp, ok := anyResp.(*connect.Response[Resp])
	if !ok {
		return nil, fmt.Errorf("unexpected response type %T", anyResp)
	}
	return resp, nil
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// setRequestSpec populates Spec().IsClient = true and Spec().Procedure on a
// connect.Request via unsafe access to the unexported spec field. IsClient
// lets interceptors that branch on it (like NewM2MAuthInterceptor) correctly
// identify REST client requests; Procedure lets them identify the RPC method,
// matching the behaviour of a connect-go-generated client.
func setRequestSpec[T any](req *connect.Request[T], procedure string) {
	rv := reflect.ValueOf(req).Elem()
	specField := rv.FieldByName("spec")
	if !specField.IsValid() {
		return
	}
	spec := (*connect.Spec)(unsafe.Pointer(specField.UnsafeAddr()))
	spec.IsClient = true
	if procedure != "" {
		spec.Procedure = procedure
	}
}

// setRequestPeer populates Spec().Peer on a connect.Request via unsafe access
// to the unexported peer field, so interceptors (like connectrpc.com/otelconnect)
// that read req.Peer().Addr / req.Peer().Protocol see the target host and a
// protocol, matching the behaviour of a connect-go-generated client. The AIP
// client speaks REST/JSON, but reports connect.ProtocolConnect as the closest
// well-known protocol so telemetry attributes are populated rather than empty.
func setRequestPeer[T any](req *connect.Request[T], addr string) {
	if addr == "" {
		return
	}
	rv := reflect.ValueOf(req).Elem()
	peerField := rv.FieldByName("peer")
	if !peerField.IsValid() {
		return
	}
	peer := (*connect.Peer)(unsafe.Pointer(peerField.UnsafeAddr()))
	peer.Addr = addr
	peer.Protocol = connect.ProtocolConnect
}

func doHTTPCall[Resp any](httpClient connect.HTTPClient, httpReq *http.Request) (*Resp, http.Header, http.Header, error) {
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, resp.Header, resp.Trailer, parseErrorResponse(resp.StatusCode, respBody, resp.Header, resp.Trailer)
	}

	var result Resp
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.Header, resp.Trailer, fmt.Errorf("reading response: %w", err)
	}
	if pm, ok := any(&result).(proto.Message); ok {
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(respBody, pm); err != nil {
			return nil, resp.Header, resp.Trailer, fmt.Errorf("decoding response: %w", err)
		}
	} else {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, resp.Header, resp.Trailer, fmt.Errorf("decoding response: %w", err)
		}
	}

	return &result, resp.Header, resp.Trailer, nil
}

// parseErrorResponse parses an HTTP error response body into a *connect.Error,
// copying the response headers and trailers onto the error's metadata so
// callers checking err.Meta() (e.g. for Retry-After or request IDs) see the
// same values they would when using a connect-go-generated client.
// The server returns JSON like {"code":"not_found","message":"resource not found"}.
// If the body can't be parsed, it falls back to mapping the HTTP status code.
func parseErrorResponse(statusCode int, body []byte, header, trailer http.Header) *connect.Error {
	var errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	var connectErr *connect.Error
	if json.Unmarshal(body, &errBody) == nil && errBody.Code != "" {
		if code, ok := connectCodeFromString(errBody.Code); ok {
			connectErr = connect.NewError(code, fmt.Errorf("%s", errBody.Message))
		}
	}
	if connectErr == nil {
		connectErr = connect.NewError(httpStatusToConnectCode(statusCode), fmt.Errorf("%s", string(body)))
	}
	copyHeader(connectErr.Meta(), header)
	copyHeader(connectErr.Meta(), trailer)
	return connectErr
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
