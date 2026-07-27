package testv1connect

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/firetiger-oss/connect-aip/internal/testproto/testv1"
)

// TestPathVarRoundTripThroughGeneratedRoutes drives the generated AIP client
// against the generated AIP handler so that the encoding done on the way out and
// the reconstruction done on the way in are checked together. A unit test on the
// escaper cannot catch a client and server that each look self-consistent but
// disagree on the path shape — which is exactly how a resource ID containing "/"
// used to produce a bare 404.
//
// The handler stub echoes the name it received, so a mismatch anywhere in
// escape → route match → PathValue → reconstruct shows up as a changed name.
func TestPathVarRoundTripThroughGeneratedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	for pattern, handler := range NewTestServiceAIPHandler(otelTestService{}) {
		mux.Handle(pattern, handler)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewTestServiceAIPClient(server.Client(), server.URL)

	t.Run("single wildcard", func(t *testing.T) {
		// The reported production case: an environment auto-discovered from a
		// Vercel monorepo build, whose ID carries both a space and a slash.
		for _, name := range []string{
			"resources/plain",
			"resources/staging - apps/docs",
			"resources/100%",
			"resources/a?b#c",
		} {
			resp, err := client.GetResource(t.Context(),
				connect.NewRequest(&testv1.GetResourceRequest{Name: name}))
			if err != nil {
				t.Errorf("GetResource(%q): %v", name, err)
				continue
			}
			if got := resp.Msg.GetResource().GetName(); got != name {
				t.Errorf("GetResource(%q) round-tripped as %q", name, got)
			}
		}
	})

	t.Run("multi wildcard", func(t *testing.T) {
		// {name=resources/*/versions/*} is registered as two single-segment route
		// wildcards, so each ID component has to be escaped independently. The
		// last case is the one the TS/Py clients used to miss entirely.
		for _, name := range []string{
			"resources/r1/versions/v1",
			"resources/r1/versions/a b",
			"resources/r1/versions/a/b",
			"resources/r 1/versions/v 1",
		} {
			resp, err := client.GetVersion(t.Context(),
				connect.NewRequest(&testv1.GetVersionRequest{Name: name}))
			if err != nil {
				t.Errorf("GetVersion(%q): %v", name, err)
				continue
			}
			if got := resp.Msg.GetName(); got != name {
				t.Errorf("GetVersion(%q) round-tripped as %q", name, got)
			}
		}
	})
}
