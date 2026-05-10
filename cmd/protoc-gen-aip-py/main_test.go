package main

import (
	"slices"
	"testing"
)

// TestPyTypeResolver pins the cross-package type resolution that protoc-gen-aip-py
// needs to emit working clients. Local types resolve through the always-imported
// `pb2` alias, well-known types (google/protobuf/*) come in via
// `from google.protobuf import <basename>_pb2` and are referenced as
// `<basename>_pb2.X`, and any other cross-file type gets a module alias
// derived from the source proto file basename.
func TestPyTypeResolver(t *testing.T) {
	const localFile = "connectaip/test/v1/test.proto"

	r := &pyTypeResolver{
		currentFile:  localFile,
		wktBaseNames: map[string]struct{}{},
		otherFiles:   map[string]string{},
	}

	r.registerSource(localFile, "Resource")
	r.registerSource("google/protobuf/empty.proto", "Empty")
	r.registerSource("connectaip/other/v1/other.proto", "OtherMessage")
	// Re-register Empty to verify the WKT set is keyed by basename, not duplicated.
	r.registerSource("google/protobuf/empty.proto", "Empty")

	cases := []struct {
		name     string
		source   string
		typeName string
		want     string
	}{
		{"local", localFile, "Resource", "pb2.Resource"},
		{"wkt", "google/protobuf/empty.proto", "Empty", "empty_pb2.Empty"},
		{"other", "connectaip/other/v1/other.proto", "OtherMessage", "other_pb2.OtherMessage"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.resolveSource(c.source, c.typeName)
			if got != c.want {
				t.Errorf("resolveSource(%q, %q) = %q; want %q", c.source, c.typeName, got, c.want)
			}
		})
	}

	gotImports := r.importLines()
	wantImports := []string{
		`from google.protobuf import empty_pb2`,
		`import connectaip.other.v1.other_pb2 as other_pb2`,
	}
	if !slices.Equal(gotImports, wantImports) {
		t.Errorf("importLines() = %q; want %q", gotImports, wantImports)
	}
}
