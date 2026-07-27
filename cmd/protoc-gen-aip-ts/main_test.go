package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTSFixtureInvariants verifies the protoc-gen-aip-ts plugin emits a class
// that nominally implements the standard connect-rpc Client interface from
// @connectrpc/connect, so consumers can drop the AIP client in wherever they
// previously called createClient(Service, transport).
//
// Regenerate the fixture if test.proto changes:
//
//	go install ./cmd/protoc-gen-aip-ts
//	cd internal/testproto && PATH=$HOME/go/bin:$PATH buf generate --template buf.gen.ts.yaml
func TestTSFixtureInvariants(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(file), "../..")
	rel := "internal/testproto/testts/test_aip.ts"
	data, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read fixture %q: %v (regenerate via `cd internal/testproto && buf generate --template buf.gen.ts.yaml`)", rel, err)
	}
	content := string(data)

	for _, want := range []string{
		`import { type Client, type CallOptions } from "@connectrpc/connect";`,
		`export class TestServiceAIPClient implements Client<typeof pb.TestService> {`,
		`options: CallOptions = {},`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q — TS AIP client must be a drop-in for createClient(Service, transport)", want)
		}
	}

	for _, banned := range []string{
		`{ headers?: Record<string, string>; signal?: AbortSignal }`,
	} {
		if strings.Contains(content, banned) {
			t.Errorf("fixture contains %q — narrow options type was replaced with CallOptions to satisfy Client<T>", banned)
		}
	}

	// Partial-coverage service: no `implements Client<...>` clause, since the
	// AIP class is missing methods the standard interface would require.
	if !strings.Contains(content, `export class MixedCoverageServiceAIPClient {`) {
		t.Error("fixture missing plain `export class MixedCoverageServiceAIPClient {` — partial-coverage services must omit the implements clause")
	}
	if strings.Contains(content, `MixedCoverageServiceAIPClient implements Client<`) {
		t.Error("fixture wrongly declares MixedCoverageServiceAIPClient implements Client<...> — that fails tsc because UnannotatedMethod is missing")
	}
}

// TestTSPathVarEncoding pins that emitted clients percent-encode path variables.
// Interpolated raw, a resource ID containing "/" (e.g. a deployment environment
// named "staging - apps/docs") splits into an extra path segment and the request
// stops matching its route, surfacing as a content-free 404.
func TestTSPathVarEncoding(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(file), "../..")
	rel := "internal/testproto/testts/test_aip.ts"
	data, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read fixture %q: %v (regenerate via `cd internal/testproto && buf generate --template buf.gen.ts.yaml`)", rel, err)
	}
	content := string(data)

	if !strings.Contains(content, "export function encodePathVar(value: string, multiSegment: boolean): string {") {
		t.Error("fixture missing the inlined encodePathVar runtime helper")
	}

	for _, want := range []string{
		// Single-segment placeholder: the whole value is one segment, so "/" must
		// be escaped along with everything else.
		`url = url.replace("{name}", encodePathVar((request.name ?? "").replace("resources/", ""), false));`,
		// Rest-of-path placeholder ({name=resources/*/versions/*}): the value spans
		// several segments, so its separators stay literal.
		`url = url.replace("{name...}", encodePathVar((request.name ?? "").replace("resources/", ""), true));`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("fixture missing %q", want)
		}
	}

	// No path variable may reach the URL without going through encodePathVar.
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "url = url.replace(") && !strings.Contains(trimmed, "encodePathVar(") {
			t.Errorf("unencoded path var substitution: %s", trimmed)
		}
	}
}
