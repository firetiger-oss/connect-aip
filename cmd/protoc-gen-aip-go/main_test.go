package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the connect-aip module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is .../cmd/protoc-gen-aip-go/main_test.go — go up two directories
	return filepath.Join(filepath.Dir(file), "../..")
}

// readFixture reads the AIP-generated file produced by running this plugin
// against internal/testproto/test.proto. The fixture is checked in; if it
// drifts from the plugin's output, the regenerate-fixtures skill explains
// the recovery.
func readFixture(t *testing.T) string {
	t.Helper()
	rel := "internal/testproto/testv1/testv1connect/test_aip.connect.go"
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read fixture %q: %v", rel, err)
	}
	return string(data)
}

// TestStreamingHandlerInvariants verifies the three patterns that prevent the
// SSE protocol bugs found during the original code review:
//
//  1. connect.WithCompression("gzip", nil, nil) — server-streaming responses
//     must not be gzip-compressed by Connect or the SSE client cannot decode.
//
//  2. connectsse.Server is the streaming handler — a unary forwarder would
//     silently discard the nested JSON request body.
//
//  3. The streaming route is registered as POST — connectsse.Client always
//     POSTs; an annotation-derived GET route would 405.
func TestStreamingHandlerInvariants(t *testing.T) {
	content := readFixture(t)

	if !strings.Contains(content, `connect.WithCompression("gzip", nil, nil)`) {
		t.Error("fixture missing connect.WithCompression(\"gzip\", nil, nil) — streaming handlers must disable gzip to prevent SSE client decompression failure")
	}
	if !strings.Contains(content, `connectsse.Server{Handler: connectHandler}`) {
		t.Error("fixture missing &connectsse.Server{Handler: connectHandler} — streaming handler must use SSE server, not a unary forwarder")
	}
	if !strings.Contains(content, `"POST /v1/resources:stream"`) {
		t.Error("fixture missing POST registration for /v1/resources:stream — streaming routes must be POST regardless of the annotation method")
	}
}

// TestStreamingClientWiresInterceptors verifies that the server-streaming
// client constructor routes connectaip ClientOptions into connect.NewClient via
// connectaip.ConnectClientOptions. This is what makes full connect.Interceptors
// (e.g. connectrpc.com/otelconnect) run as streaming interceptors on the SSE
// client path — without it, otelconnect would never see streaming RPCs.
func TestStreamingClientWiresInterceptors(t *testing.T) {
	content := readFixture(t)

	if !strings.Contains(content, "connectaip.ConnectClientOptions(opts...)") {
		t.Error("fixture missing connectaip.ConnectClientOptions(opts...) — streaming client must forward interceptors into connect.NewClient")
	}
}

// TestUnaryHandlersUseConnectaipForward verifies that unary handlers use the
// connectaip.Forward path (not connectsse) and emit the AIP-named symbols.
func TestUnaryHandlersUseConnectaipForward(t *testing.T) {
	content := readFixture(t)

	for _, want := range []string{
		`connectaip.Forward[*testv1.CreateResourceRequest, *testv1.CreateResourceResponse]`,
		`connectaip.ForwardWithBody[*testv1.GetResourceRequest, *testv1.GetResourceResponse]`,
		`NewTestServiceAIPHandler`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q", want)
		}
	}
}

// TestClientImplementsStandardInterface verifies the AIP client constructor
// returns the standard {Service}Client interface (not a separate AIP-specific
// interface), and that unary methods take *connect.Request[T] and return
// *connect.Response[T] so the impl satisfies the standard interface. It also
// verifies that the AIP type name is still exported (as an alias) so existing
// downstream code that types variables as {Service}AIPClient keeps compiling.
func TestClientImplementsStandardInterface(t *testing.T) {
	content := readFixture(t)

	for _, want := range []string{
		`func NewTestServiceAIPClient(httpClient connect.HTTPClient, baseURL string, opts ...connectaip.ClientOption) TestServiceClient`,
		`func (c *testServiceAIPClient) CreateResource(ctx context.Context, req *connect.Request[testv1.CreateResourceRequest]) (*connect.Response[testv1.CreateResourceResponse], error)`,
		`return c.createResource.CallRequest(ctx, req)`,
		// The AIP type name is preserved as an alias so existing code keeps
		// compiling after regeneration.
		`type TestServiceAIPClient = TestServiceClient`,
		// Procedure must propagate so unary interceptors can identify the RPC.
		`Procedure:  TestServiceCreateResourceProcedure,`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q — AIP client must satisfy the standard {Service}Client interface", want)
		}
	}

	for _, banned := range []string{
		`type TestServiceAIPClient interface`,
		`TestServiceAIPClient is a AIP client`,
	} {
		if strings.Contains(content, banned) {
			t.Errorf("fixture contains banned token %q — the separate AIP client interface was removed", banned)
		}
	}
}

// TestPartialCoverageEmitsLegacyInterface verifies that when a service has any
// RPC without an HTTP rule (or otherwise filtered out by the plugin), the
// generated constructor falls back to a service-scoped {Service}AIPClient
// interface rather than claiming to satisfy {Service}Client. Without this
// fallback the generated file would not compile because the impl struct is
// missing methods the standard interface requires.
func TestPartialCoverageEmitsLegacyInterface(t *testing.T) {
	content := readFixture(t)

	for _, want := range []string{
		`type MixedCoverageServiceAIPClient interface {`,
		`AnnotatedMethod(ctx context.Context, req *connect.Request[testv1.GetResourceRequest]) (*connect.Response[testv1.GetResourceResponse], error)`,
		`func NewMixedCoverageServiceAIPClient(httpClient connect.HTTPClient, baseURL string, opts ...connectaip.ClientOption) MixedCoverageServiceAIPClient {`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q — partial-coverage services must emit a service-scoped AIP interface", want)
		}
	}

	for _, banned := range []string{
		`opts ...connectaip.ClientOption) MixedCoverageServiceClient`,
		`MixedCoverageServiceAIPClient is a AIP client`,
	} {
		if strings.Contains(content, banned) {
			t.Errorf("fixture contains banned token %q — partial-coverage services must NOT return the standard {Service}Client", banned)
		}
	}
}

// TestNoLegacyRESTSymbols guards against partial REST→AIP renames sneaking
// back into the codegen. Emitted symbols and filenames must use AIP, never
// REST. The string "REST" itself is allowed in prose comments since the
// codegen produces REST/HTTP endpoints — but type/function names must not.
func TestNoLegacyRESTSymbols(t *testing.T) {
	content := readFixture(t)

	for _, banned := range []string{
		"RESTHandler",
		"RESTClient",
		"RESTError",
		"_rest.connect.go",
		"firetiger-inc/core",
	} {
		if strings.Contains(content, banned) {
			t.Errorf("fixture contains forbidden token %q — partial rename or stale generator output", banned)
		}
	}
}

// TestUnaryHandlerWithExternalReturnType pins the fix for a regression where
// any RPC returning a message from a different proto package (e.g.
// google.protobuf.Empty) emitted the type via the local proto package alias
// (e.g. testv1.Empty), producing an "undefined" reference at compile time.
// Cross-package types must resolve through g.QualifiedGoIdent, e.g.
// emptypb.Empty.
func TestUnaryHandlerWithExternalReturnType(t *testing.T) {
	content := readFixture(t)

	for _, want := range []string{
		`emptypb "google.golang.org/protobuf/types/known/emptypb"`,
		`*testv1.DeleteResourceRequest, *emptypb.Empty`,
		`DeleteResource(ctx context.Context, req *connect.Request[testv1.DeleteResourceRequest]) (*connect.Response[emptypb.Empty], error)`,
		`*connectaip.Client[testv1.DeleteResourceRequest, emptypb.Empty]`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q — cross-package return type (google.protobuf.Empty) is not resolving via the emptypb import", want)
		}
	}

	for _, banned := range []string{
		`testv1.Empty`,
	} {
		if strings.Contains(content, banned) {
			t.Errorf("fixture contains %q — cross-package return type (google.protobuf.Empty) is being emitted via the local proto package alias", banned)
		}
	}
}
