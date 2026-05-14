package testv1connect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	connectaip "github.com/firetiger-oss/connect-aip"
	testv1 "github.com/firetiger-oss/connect-aip/internal/testproto/testv1"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// otelTestService is a minimal TestServiceHandler used to exercise the AIP
// handler + client paths under otelconnect instrumentation.
type otelTestService struct{}

func (otelTestService) CreateResource(context.Context, *connect.Request[testv1.CreateResourceRequest]) (*connect.Response[testv1.CreateResourceResponse], error) {
	return connect.NewResponse(&testv1.CreateResourceResponse{}), nil
}

func (otelTestService) GetResource(_ context.Context, req *connect.Request[testv1.GetResourceRequest]) (*connect.Response[testv1.GetResourceResponse], error) {
	return connect.NewResponse(&testv1.GetResourceResponse{
		Resource: &testv1.Resource{Name: req.Msg.GetName()},
	}), nil
}

func (otelTestService) UpdateResource(context.Context, *connect.Request[testv1.UpdateResourceRequest]) (*connect.Response[testv1.UpdateResourceResponse], error) {
	return connect.NewResponse(&testv1.UpdateResourceResponse{}), nil
}

func (otelTestService) ListResources(context.Context, *connect.Request[testv1.ListResourcesRequest]) (*connect.Response[testv1.ListResourcesResponse], error) {
	return connect.NewResponse(&testv1.ListResourcesResponse{}), nil
}

func (otelTestService) ListVersions(context.Context, *connect.Request[testv1.ListVersionsRequest]) (*connect.Response[testv1.ListVersionsResponse], error) {
	return connect.NewResponse(&testv1.ListVersionsResponse{}), nil
}

func (otelTestService) StreamResources(_ context.Context, req *connect.Request[testv1.CreateResourceRequest], stream *connect.ServerStream[testv1.CreateResourceResponse]) error {
	return stream.Send(&testv1.CreateResourceResponse{
		Resource: &testv1.Resource{Name: "resources/" + req.Msg.GetResourceId()},
	})
}

func (otelTestService) DeleteResource(context.Context, *connect.Request[testv1.DeleteResourceRequest]) (*connect.Response[emptypb.Empty], error) {
	return connect.NewResponse(&emptypb.Empty{}), nil
}

// TestOtelConnectInterceptorCompatibility verifies that an otelconnect
// interceptor — a full connect.Interceptor — can be wired into both the AIP
// handler (via connect.HandlerOption) and the AIP client (via
// connectaip.WithInterceptors), and that it records spans with the correct
// procedure names for both unary and server-streaming REST methods.
func TestOtelConnectInterceptorCompatibility(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	interceptor, err := otelconnect.NewInterceptor(
		otelconnect.WithTracerProvider(tracerProvider),
		otelconnect.WithoutServerPeerAttributes(),
	)
	if err != nil {
		t.Fatalf("otelconnect.NewInterceptor: %v", err)
	}

	mux := http.NewServeMux()
	for pattern, handler := range NewTestServiceAIPHandler(otelTestService{}, connect.WithInterceptors(interceptor)) {
		mux.Handle(pattern, handler)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewTestServiceAIPClient(server.Client(), server.URL, connectaip.WithInterceptors(interceptor))

	// Unary REST method.
	if _, err := client.GetResource(t.Context(), connect.NewRequest(&testv1.GetResourceRequest{
		Name: "resources/abc",
	})); err != nil {
		t.Fatalf("GetResource: %v", err)
	}

	// Server-streaming REST method (SSE path).
	stream, err := client.StreamResources(t.Context(), connect.NewRequest(&testv1.CreateResourceRequest{
		ResourceId: "xyz",
	}))
	if err != nil {
		t.Fatalf("StreamResources: %v", err)
	}
	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream close: %v", err)
	}

	tracerProvider.ForceFlush(t.Context())
	spans := recorder.Ended()

	// Each instrumented RPC produces a client span and a server span.
	want := map[string]int{
		strings.TrimPrefix(TestServiceGetResourceProcedure, "/"):     0,
		strings.TrimPrefix(TestServiceStreamResourcesProcedure, "/"): 0,
	}
	for _, span := range spans {
		if _, ok := want[span.Name()]; ok {
			want[span.Name()]++
		}
	}
	for name, count := range want {
		if count < 2 {
			t.Errorf("expected >=2 spans (client + server) for %q, got %d (all spans: %v)", name, count, spanNames(spans))
		}
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name()
	}
	return names
}
