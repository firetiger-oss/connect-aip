package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// TestParsePathPatternWildcardShapes pins how each pattern shape maps onto a
// ServeMux path, because that decision drives path-var encoding: a single-segment
// placeholder has its whole value escaped ("/" included), while a rest-of-path one
// keeps its separators. Getting it wrong sends requests to a path the server never
// registered, which surfaces as a bare 404.
//
// These expectations must stay identical across protoc-gen-aip-{go,ts,py}: the Go
// plugin's ServeMux path is what the server registers, so the other two have to
// build the same shape.
func TestParsePathPatternWildcardShapes(t *testing.T) {
	cases := map[string]struct {
		pattern  string
		wantPath string
		wantVars []string // "name[:prefix]" per var, or "name!multi(prefix|sep|idx)"
	}{
		"single trailing wildcard": {
			pattern:  "/v1/{name=resources/*}",
			wantPath: "/v1/resources/{name}",
			wantVars: []string{"name:resources/"},
		},
		"wildcard with literal suffix outside braces": {
			pattern:  "/v1/{name=resources/*}/versions",
			wantPath: "/v1/resources/{name}/versions",
			wantVars: []string{"name:resources/"},
		},
		"multi wildcard expands to one segment each": {
			pattern:  "/v1/{name=resources/*/versions/*}",
			wantPath: "/v1/resources/{name_0}/versions/{name_1}",
			wantVars: []string{"name_0!multi(resources/|/versions/|0)", "name_1!multi(resources/|/versions/|1)"},
		},
		"rest of path when a literal trails inside braces": {
			pattern:  "/v1/{name=resources/*/versions}",
			wantPath: "/v1/resources/{name...}",
			wantVars: []string{"name:resources/"},
		},
		"nested field path uses the last segment": {
			pattern:  "/v1/{resource.name=resources/*}",
			wantPath: "/v1/resources/{name}",
			wantVars: []string{"name:resources/"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			vars, path, ok := parsePathPattern(c.pattern)
			if !ok {
				t.Fatalf("parsePathPattern(%q) not ok", c.pattern)
			}
			if path != c.wantPath {
				t.Errorf("path = %q; want %q", path, c.wantPath)
			}
			var got []string
			for _, pv := range vars {
				if pv.multi != nil {
					got = append(got, fmt.Sprintf("%s!multi(%s|%s|%d)",
						pv.name, pv.multi.prefix, strings.Join(pv.multi.seps, ","), pv.multi.idx))
					continue
				}
				got = append(got, pv.name+":"+pv.prefix)
			}
			if strings.Join(got, " ") != strings.Join(c.wantVars, " ") {
				t.Errorf("vars = %v; want %v", got, c.wantVars)
			}
		})
	}
}

// TestPyPathVarEncoding is the Python counterpart to protoc-gen-aip-ts's
// TestTSPathVarEncoding: the emitted client must hand the runtime the same
// placeholder shapes the Go plugin registers as routes, so the runtime's escaping
// (whole value for a single segment, per-segment for rest-of-path) lines up.
//
// Regenerate the fixture if test.proto changes:
//
//	go install ./cmd/protoc-gen-aip-py
//	cd internal/testproto && PATH=$HOME/go/bin:$PATH buf generate --template buf.gen.py.yaml
func TestPyPathVarEncoding(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	rel := "internal/testproto/testpy/test_aip.py"
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../..", rel))
	if err != nil {
		t.Fatalf("read fixture %q: %v (regenerate via `cd internal/testproto && buf generate --template buf.gen.py.yaml`)", rel, err)
	}
	content := string(data)

	for _, want := range []string{
		// Multi-wildcard: one route segment per wildcard, matching the ServeMux
		// path protoc-gen-aip-go registers, with each value split back out of the
		// single proto field the pattern matched.
		`"GET", "/v1/resources/{name_0}/versions/{name_1}"`,
		`"{name_0}": split_multi_wildcard(req.name, "resources/", ["/versions/"], 0),`,
		`"{name_1}": split_multi_wildcard(req.name, "resources/", ["/versions/"], 1),`,
		// The helper has to be imported for the emitted module to run at all.
		`from connectaip import Client, MethodSpec, PathVar, SSEClient, split_multi_wildcard`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q", want)
		}
	}

	// A rest-of-path placeholder keeps its separators, so it must never be emitted
	// for a pattern the server registers as separate segments.
	if strings.Contains(content, `"/v1/resources/{name...}/versions/`) {
		t.Error("fixture builds a rest-of-path placeholder for a multi-segment route")
	}
}
