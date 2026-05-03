package connectaip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/firetiger-oss/connect-aip/internal/testproto/testv1"
	connectsse "github.com/firetiger-oss/connect-sse"
	"google.golang.org/protobuf/proto"
)

// TestSSEClientServerRoundTrip is the end-to-end integration test that exercises the full
// connectsse.Client ↔ connectsse.Server ↔ Connect handler streaming path.
//
// This test would have caught both bugs that were found only in code review:
//   - Double-wrapping bug: if ForwardStream were in the path, the request proto fields
//     would be silently discarded (DiscardUnknown), so gotResourceId would be empty.
//   - Method mismatch bug: if the route were registered as GET, connectsse.Client's POST
//     would receive 405 and the test would fail to get any responses.
func TestSSEClientServerRoundTrip(t *testing.T) {
	const procedure = "/connectaip.test.v1.TestService/StreamResources"
	const restPath = "/v1/resources:query"

	var gotResourceId string

	mux := http.NewServeMux()
	// connect.WithCompression("gzip", nil, nil) mirrors what the generated NewServiceRESTHandler
	// constructor injects: it removes gzip support from the Connect handler so that
	// connectsse.Server never produces compressed envelope payloads that connectsse.Client
	// cannot decompress.
	mux.Handle(procedure, connect.NewServerStreamHandler(
		procedure,
		func(ctx context.Context, req *connect.Request[testv1.CreateResourceRequest], stream *connect.ServerStream[testv1.CreateResourceResponse]) error {
			gotResourceId = req.Msg.GetResourceId()
			for i := range 3 {
				name := "resources/" + req.Msg.GetResourceId()
				if i > 0 {
					name += "-" + string(rune('0'+i))
				}
				if err := stream.Send(&testv1.CreateResourceResponse{
					Resource: &testv1.Resource{Name: name},
				}); err != nil {
					return err
				}
			}
			return nil
		},
		connect.WithCompression("gzip", nil, nil),
	))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		sseHandler := &connectsse.Server{Handler: mux}
		sseHandler.ServeHTTP(w, r)
	}))
	defer server.Close()

	sseHTTPClient := NewSSEClient(server.Client(), server.URL+restPath, nil)
	connectClient := connect.NewClient[testv1.CreateResourceRequest, testv1.CreateResourceResponse](
		sseHTTPClient,
		SSEProcedureURL(server.URL, procedure),
		connect.WithProtoJSON(),
	)

	stream, err := connectClient.CallServerStream(t.Context(),
		connect.NewRequest(&testv1.CreateResourceRequest{ResourceId: "round-trip-test"}))
	if err != nil {
		t.Fatalf("CallServerStream: %v", err)
	}
	defer stream.Close()

	var responses []*testv1.CreateResourceResponse
	for stream.Receive() {
		msg := proto.Clone(stream.Msg()).(*testv1.CreateResourceResponse)
		responses = append(responses, msg)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	// Verify request was received with correct fields (would be empty if double-wrapping bug present).
	if gotResourceId != "round-trip-test" {
		t.Errorf("handler got resourceId=%q, want %q — this indicates a double-wrapping bug", gotResourceId, "round-trip-test")
	}

	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}
	if responses[0].GetResource().GetName() != "resources/round-trip-test" {
		t.Errorf("response[0] name = %q, want resources/round-trip-test", responses[0].GetResource().GetName())
	}
}

// TestSSEClientRequiresPostRoute verifies that connectsse.Client always POSTs, so streaming
// routes must be registered as POST in http.ServeMux. A GET registration causes 405.
//
// This documents the method-mismatch constraint: the generator must always emit
// "POST /path" for streaming routes, never the annotation's original HTTP method.
func TestSSEClientRequiresPostRoute(t *testing.T) {
	const procedure = "/connectaip.test.v1.TestService/StreamResources"

	mux := http.NewServeMux()
	mux.Handle(procedure, connect.NewServerStreamHandler(
		procedure,
		func(ctx context.Context, req *connect.Request[testv1.CreateResourceRequest], stream *connect.ServerStream[testv1.CreateResourceResponse]) error {
			return nil
		},
	))

	// Register as GET — connectsse.Client will POST → expect 405.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpMux := http.NewServeMux()
		httpMux.Handle("GET /stream", &connectsse.Server{Handler: mux})
		httpMux.ServeHTTP(w, r)
	}))
	defer server.Close()

	sseHTTPClient := NewSSEClient(server.Client(), server.URL+"/stream", nil)
	connectClient := connect.NewClient[testv1.CreateResourceRequest, testv1.CreateResourceResponse](
		sseHTTPClient,
		SSEProcedureURL(server.URL, procedure),
		connect.WithProtoJSON(),
	)

	stream, err := connectClient.CallServerStream(t.Context(),
		connect.NewRequest(&testv1.CreateResourceRequest{ResourceId: "test"}))
	// Connect's streaming client may not surface the HTTP error until Receive() is called.
	var streamErr error
	if err != nil {
		streamErr = err
	} else {
		defer stream.Close()
		for stream.Receive() {
		}
		streamErr = stream.Err()
	}
	if streamErr == nil {
		t.Fatal("expected error due to GET/POST method mismatch, got nil")
	}
	t.Logf("method mismatch error (as expected): %v", streamErr)
}
