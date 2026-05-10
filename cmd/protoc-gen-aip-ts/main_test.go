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

	r := newTSTypeResolverForPath(localFile)

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

	// Pins codex review [P1]: external proto must import via a real relative
	// path from the current file's directory to the dep's directory, NOT the
	// dep's basename. With localFile=connectaip/test/v1/test.proto and
	// other=connectaip/other/v1/other.proto, the dep lives two levels up
	// then back down: ../../other/v1/other_pb.
	gotImports := r.importLines()
	wantImports := []string{
		`import { Empty, EmptySchema } from "@bufbuild/protobuf/wkt";`,
		`import * as other_pb from "../../other/v1/other_pb";`,
	}
	if !slices.Equal(gotImports, wantImports) {
		t.Errorf("importLines() = %q; want %q", gotImports, wantImports)
	}
}

// TestTSTypeResolverRelativeImport pins the relative-path import behavior in
// isolation across several layouts: same dir as current, sibling dir, deeper
// dir, and a proto with no directory at all (top-level).
func TestTSTypeResolverRelativeImport(t *testing.T) {
	cases := []struct {
		name        string
		currentFile string
		otherSource string
		want        string
	}{
		{"sibling-file", "a/b/c/test.proto", "a/b/c/sibling.proto", `import * as sibling_pb from "./sibling_pb";`},
		{"sibling-dir", "a/b/c/test.proto", "a/b/d/cousin.proto", `import * as cousin_pb from "../d/cousin_pb";`},
		{"distant-dir", "connectaip/test/v1/test.proto", "connectaip/other/v1/other.proto", `import * as other_pb from "../../other/v1/other_pb";`},
		{"deeper-dir", "a/b/test.proto", "a/b/sub/deep/x.proto", `import * as x_pb from "./sub/deep/x_pb";`},
		{"top-level-current", "test.proto", "a/b/dep.proto", `import * as dep_pb from "./a/b/dep_pb";`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newTSTypeResolverForPath(c.currentFile)
			r.registerSource(c.otherSource, "X")
			got := r.importLines()
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("importLines() = %q; want [%q]", got, c.want)
			}
		})
	}
}

// TestTSTypeResolverAliasUniqueness pins codex review [P2]: when two distinct
// non-local proto files share a basename (foo/v1/common.proto and
// bar/v1/common.proto), each must get a distinct alias and resolveSource must
// return the right one for each source. Without the fix both collapsed to
// `common_pb` and one of the resolved references would point at the wrong
// module.
func TestTSTypeResolverAliasUniqueness(t *testing.T) {
	const localFile = "myapp/v1/svc.proto"

	r := newTSTypeResolverForPath(localFile)
	r.registerSource("foo/v1/common.proto", "FooThing")
	r.registerSource("bar/v1/common.proto", "BarThing")

	fooType, fooSc := r.resolveSource("foo/v1/common.proto", "FooThing")
	barType, barSc := r.resolveSource("bar/v1/common.proto", "BarThing")

	if fooType == barType {
		t.Errorf("foo and bar resolved to the same type expression %q — basename collision regressed", fooType)
	}
	if fooSc == barSc {
		t.Errorf("foo and bar resolved to the same schema expression %q — basename collision regressed", fooSc)
	}

	wantImports := []string{
		`import * as common_pb_1 from "../../bar/v1/common_pb";`,
		`import * as common_pb from "../../foo/v1/common_pb";`,
	}
	if got := r.importLines(); !slices.Equal(got, wantImports) {
		t.Errorf("importLines() = %q; want %q", got, wantImports)
	}

	// Aliases are pinned at registration time (foo registered first, claimed
	// `common_pb`; bar registered second, was renamed to `common_pb_1`).
	// Source order in importLines() is alphabetical, so bar appears first.
	if fooType != "common_pb.FooThing" {
		t.Errorf("foo type = %q; want common_pb.FooThing", fooType)
	}
	if barType != "common_pb_1.BarThing" {
		t.Errorf("bar type = %q; want common_pb_1.BarThing", barType)
	}
}

// TestTSTypeResolverNestedGoogleProtobuf pins codex review round 3 [P2]:
// `google/protobuf/compiler/plugin.proto` is NOT a top-level WKT — types like
// `CodeGeneratorRequest` are not exported by `@bufbuild/protobuf/wkt`. A naive
// prefix check would emit `import { CodeGeneratorRequest, CodeGeneratorRequestSchema }
// from "@bufbuild/protobuf/wkt"` and TypeScript would fail to resolve. Instead,
// the file should be treated as a regular cross-file proto with a relative
// import path.
func TestTSTypeResolverNestedGoogleProtobuf(t *testing.T) {
	const localFile = "myapp/v1/svc.proto"

	r := newTSTypeResolverForPath(localFile)
	r.registerSource("google/protobuf/compiler/plugin.proto", "CodeGeneratorRequest")

	gotType, gotSc := r.resolveSource("google/protobuf/compiler/plugin.proto", "CodeGeneratorRequest")
	if gotType != "plugin_pb.CodeGeneratorRequest" || gotSc != "plugin_pb.CodeGeneratorRequestSchema" {
		t.Errorf("nested google.protobuf type resolved to (%q, %q); want (plugin_pb.CodeGeneratorRequest, plugin_pb.CodeGeneratorRequestSchema)", gotType, gotSc)
	}

	wantImports := []string{
		`import * as plugin_pb from "../../google/protobuf/compiler/plugin_pb";`,
	}
	if got := r.importLines(); !slices.Equal(got, wantImports) {
		t.Errorf("importLines() = %q; want %q (nested google/protobuf/** must NOT be imported from @bufbuild/protobuf/wkt)", got, wantImports)
	}
}
