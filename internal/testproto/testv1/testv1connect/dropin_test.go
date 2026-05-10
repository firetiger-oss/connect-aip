package testv1connect

import (
	"net/http"
	"testing"
)

// The AIP client is meant to be a drop-in replacement for the standard
// connect-go client. Assigning the AIP constructor's return value to the
// standard interface fails the build if signatures drift.
var _ TestServiceClient = NewTestServiceAIPClient(http.DefaultClient, "http://example.test")

// MixedCoverageService has an RPC without an HTTP rule, so the AIP client
// only covers a subset of the standard interface. The constructor must
// instead return the service-scoped MixedCoverageServiceAIPClient interface,
// and assigning it to the standard MixedCoverageServiceClient must NOT
// compile. We can't assert non-compilation in a normal test, but the var
// below ensures the legacy AIP-specific interface is emitted and assignable.
var _ MixedCoverageServiceAIPClient = NewMixedCoverageServiceAIPClient(http.DefaultClient, "http://example.test")

func TestAIPClientImplementsStandardInterface(t *testing.T) {
	var c TestServiceClient = NewTestServiceAIPClient(http.DefaultClient, "http://example.test")
	if c == nil {
		t.Fatal("NewTestServiceAIPClient returned nil")
	}
}

func TestPartialCoverageReturnsLegacyAIPInterface(t *testing.T) {
	var c MixedCoverageServiceAIPClient = NewMixedCoverageServiceAIPClient(http.DefaultClient, "http://example.test")
	if c == nil {
		t.Fatal("NewMixedCoverageServiceAIPClient returned nil")
	}
}
