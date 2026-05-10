package testv1connect

import (
	"net/http"
	"testing"
)

// The AIP client is meant to be a drop-in replacement for the standard
// connect-go client. Assigning the AIP constructor's return value to the
// standard interface fails the build if signatures drift.
var _ TestServiceClient = NewTestServiceAIPClient(http.DefaultClient, "http://example.test")

func TestAIPClientImplementsStandardInterface(t *testing.T) {
	var c TestServiceClient = NewTestServiceAIPClient(http.DefaultClient, "http://example.test")
	if c == nil {
		t.Fatal("NewTestServiceAIPClient returned nil")
	}
}
