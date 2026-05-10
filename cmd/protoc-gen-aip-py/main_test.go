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

	r := newPyTypeResolverForPath(localFile)

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

// TestPyTypeResolverAliasUniqueness pins codex review [P2]: when two distinct
// non-local proto files share a basename (foo/v1/common.proto and
// bar/v1/common.proto), each must get a distinct alias and resolveSource must
// return the right one for each source. Without the fix both collapsed to
// `common_pb2`, the import lines would have a duplicate identifier, and at
// least one resolved type would point at the wrong module.
func TestPyTypeResolverAliasUniqueness(t *testing.T) {
	const localFile = "myapp/v1/svc.proto"

	r := newPyTypeResolverForPath(localFile)
	r.registerSource("foo/v1/common.proto", "FooThing")
	r.registerSource("bar/v1/common.proto", "BarThing")

	fooType := r.resolveSource("foo/v1/common.proto", "FooThing")
	barType := r.resolveSource("bar/v1/common.proto", "BarThing")

	if fooType == barType {
		t.Errorf("foo and bar resolved to the same expression %q — basename collision regressed", fooType)
	}
	if fooType != "common_pb2.FooThing" {
		t.Errorf("foo type = %q; want common_pb2.FooThing", fooType)
	}
	if barType != "common_pb2_1.BarThing" {
		t.Errorf("bar type = %q; want common_pb2_1.BarThing", barType)
	}

	wantImports := []string{
		`import bar.v1.common_pb2 as common_pb2_1`,
		`import foo.v1.common_pb2 as common_pb2`,
	}
	if got := r.importLines(); !slices.Equal(got, wantImports) {
		t.Errorf("importLines() = %q; want %q", got, wantImports)
	}
}

// TestPyTypeResolverWKTCollidesWithCustomProto pins codex review [P2]: a
// non-WKT proto whose basename matches a WKT (e.g. a custom `empty.proto`
// alongside `google.protobuf.Empty`) must NOT collapse onto the WKT's
// `empty_pb2` identifier. The WKT keeps its canonical name (Python convention)
// and the custom proto is renamed.
func TestPyTypeResolverWKTCollidesWithCustomProto(t *testing.T) {
	const localFile = "myapp/v1/svc.proto"

	r := newPyTypeResolverForPath(localFile)
	r.registerSource("google/protobuf/empty.proto", "Empty")
	r.registerSource("custom/v1/empty.proto", "CustomEmpty")

	wktType := r.resolveSource("google/protobuf/empty.proto", "Empty")
	customType := r.resolveSource("custom/v1/empty.proto", "CustomEmpty")

	if wktType != "empty_pb2.Empty" {
		t.Errorf("WKT Empty resolved to %q; want empty_pb2.Empty (Python's canonical name must not be renamed)", wktType)
	}
	if customType == wktType {
		t.Errorf("custom Empty collapsed onto the WKT identifier %q — collision regressed", customType)
	}
	if customType != "empty_pb2_1.CustomEmpty" {
		t.Errorf("custom Empty resolved to %q; want empty_pb2_1.CustomEmpty", customType)
	}

	wantImports := []string{
		`from google.protobuf import empty_pb2`,
		`import custom.v1.empty_pb2 as empty_pb2_1`,
	}
	if got := r.importLines(); !slices.Equal(got, wantImports) {
		t.Errorf("importLines() = %q; want %q", got, wantImports)
	}
}
