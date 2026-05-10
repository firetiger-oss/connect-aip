package main

import (
	"slices"
	"strings"
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
	// Aliases are assigned in source-path-sorted order to be order-independent
	// (bar/v1/common.proto sorts before foo/v1/common.proto), so bar claims
	// the canonical `common_pb2` and foo gets renamed.
	if barType != "common_pb2.BarThing" {
		t.Errorf("bar type = %q; want common_pb2.BarThing", barType)
	}
	if fooType != "common_pb2_1.FooThing" {
		t.Errorf("foo type = %q; want common_pb2_1.FooThing", fooType)
	}

	wantImports := []string{
		`import bar.v1.common_pb2 as common_pb2`,
		`import foo.v1.common_pb2 as common_pb2_1`,
	}
	if got := r.importLines(); !slices.Equal(got, wantImports) {
		t.Errorf("importLines() = %q; want %q", got, wantImports)
	}
}

// TestPyTypeResolverNestedGoogleProtobuf pins codex review round 3 [P2]:
// `google/protobuf/compiler/plugin.proto` is NOT a top-level WKT — its Python
// module is `google.protobuf.compiler.plugin_pb2`, not `google.protobuf.plugin_pb2`.
// A naive prefix check that treats any `google/protobuf/**` as a WKT would emit
// `from google.protobuf import plugin_pb2` and the import would fail at runtime.
func TestPyTypeResolverNestedGoogleProtobuf(t *testing.T) {
	const localFile = "myapp/v1/svc.proto"

	r := newPyTypeResolverForPath(localFile)
	r.registerSource("google/protobuf/compiler/plugin.proto", "CodeGeneratorRequest")

	got := r.resolveSource("google/protobuf/compiler/plugin.proto", "CodeGeneratorRequest")
	if got != "plugin_pb2.CodeGeneratorRequest" {
		t.Errorf("nested google.protobuf type resolved to %q; want plugin_pb2.CodeGeneratorRequest", got)
	}

	wantImports := []string{
		`import google.protobuf.compiler.plugin_pb2 as plugin_pb2`,
	}
	if got := r.importLines(); !slices.Equal(got, wantImports) {
		t.Errorf("importLines() = %q; want %q (nested google/protobuf/** must NOT be treated as a top-level WKT)", got, wantImports)
	}
}

// TestPyTypeResolverWKTCollidesWithCustomProto pins codex review [P2]: a
// non-WKT proto whose basename matches a WKT (e.g. a custom `empty.proto`
// alongside `google.protobuf.Empty`) must NOT collapse onto the WKT's
// `empty_pb2` identifier. The WKT keeps its canonical name (Python convention)
// and the custom proto is renamed.
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

// TestPyTypeResolverWKTCollisionOrderIndependent pins codex review round 2 [P2]:
// the WKT-vs-custom collision fix must work regardless of registration order.
// If the non-WKT custom/v1/empty.proto is registered FIRST, the prior fix would
// claim `empty_pb2` for the custom proto, then the WKT registration would also
// take the canonical `empty_pb2` and the generated file would have two
// `empty_pb2` identifiers. The custom proto must be renamed even when it
// registers first.
func TestPyTypeResolverWKTCollisionOrderIndependent(t *testing.T) {
	const localFile = "myapp/v1/svc.proto"

	r := newPyTypeResolverForPath(localFile)
	// Register non-WKT first — the bug-prone order.
	r.registerSource("custom/v1/empty.proto", "CustomEmpty")
	r.registerSource("google/protobuf/empty.proto", "Empty")

	wktType := r.resolveSource("google/protobuf/empty.proto", "Empty")
	customType := r.resolveSource("custom/v1/empty.proto", "CustomEmpty")

	if wktType != "empty_pb2.Empty" {
		t.Errorf("WKT Empty resolved to %q; want empty_pb2.Empty", wktType)
	}
	if customType == wktType {
		t.Errorf("custom Empty collapsed onto the WKT identifier %q — order-dependent collision regressed", customType)
	}

	got := r.importLines()
	// Build a set of identifiers actually consumed in the import lines and
	// assert no duplicates. (Don't pin the exact alias name for the custom
	// proto — only that it differs from the WKT.)
	seen := make(map[string]int)
	for _, line := range got {
		// `from google.protobuf import empty_pb2` → identifier `empty_pb2`
		// `import custom.v1.empty_pb2 as empty_pb2_X` → identifier `empty_pb2_X`
		if strings.HasPrefix(line, "from google.protobuf import ") {
			seen[strings.TrimPrefix(line, "from google.protobuf import ")]++
			continue
		}
		if i := strings.LastIndex(line, " as "); i >= 0 {
			seen[strings.TrimSuffix(line[i+4:], "")]++
		}
	}
	for ident, count := range seen {
		if count > 1 {
			t.Errorf("identifier %q imported %d times in: %q", ident, count, got)
		}
	}
	if len(seen) != 2 {
		t.Errorf("expected 2 distinct imported identifiers, got %d in: %q", len(seen), got)
	}
}
