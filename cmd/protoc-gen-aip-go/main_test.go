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

// TestUnaryHandlersUseConnectaipForward verifies that unary handlers use the
// connectaip.Forward path (not connectsse) and emit the AIP-named symbols.
func TestUnaryHandlersUseConnectaipForward(t *testing.T) {
	content := readFixture(t)

	for _, want := range []string{
		`connectaip.Forward[*testv1.CreateResourceRequest, *testv1.CreateResourceResponse]`,
		`connectaip.ForwardWithBody[*testv1.GetResourceRequest, *testv1.GetResourceResponse]`,
		`NewTestServiceAIPHandler`,
		`TestServiceAIPClient`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q", want)
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
