package main

import (
	"slices"
	"testing"
)

// TestTSTypeResolver pins the cross-package type resolution that protoc-gen-aip-ts
// needs to emit working clients. Local types resolve through the always-imported
// `pb` namespace, well-known types (google/protobuf/*) come in by name from
// `@bufbuild/protobuf/wkt`, and any other cross-file type gets a namespace import
// alias derived from the source proto file basename.
func TestTSTypeResolver(t *testing.T) {
	const localFile = "connectaip/test/v1/test.proto"

	r := &tsTypeResolver{
		currentFile: localFile,
		wktNames:    map[string]struct{}{},
		otherFiles:  map[string]string{},
	}

	r.registerSource(localFile, "Resource")
	r.registerSource("google/protobuf/empty.proto", "Empty")
	r.registerSource("connectaip/other/v1/other.proto", "OtherMessage")
	// Re-register Empty to verify the WKT set is keyed by name, not duplicated.
	r.registerSource("google/protobuf/empty.proto", "Empty")

	cases := []struct {
		name             string
		source           string
		typeName         string
		wantType, wantSc string
	}{
		{"local", localFile, "Resource", "pb.Resource", "pb.ResourceSchema"},
		{"wkt", "google/protobuf/empty.proto", "Empty", "Empty", "EmptySchema"},
		{"other", "connectaip/other/v1/other.proto", "OtherMessage", "other_pb.OtherMessage", "other_pb.OtherMessageSchema"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotType, gotSc := r.resolveSource(c.source, c.typeName)
			if gotType != c.wantType || gotSc != c.wantSc {
				t.Errorf("resolveSource(%q, %q) = (%q, %q); want (%q, %q)", c.source, c.typeName, gotType, gotSc, c.wantType, c.wantSc)
			}
		})
	}

	gotImports := r.importLines()
	wantImports := []string{
		`import { Empty, EmptySchema } from "@bufbuild/protobuf/wkt";`,
		`import * as other_pb from "./other_pb";`,
	}
	if !slices.Equal(gotImports, wantImports) {
		t.Errorf("importLines() = %q; want %q", gotImports, wantImports)
	}
}
