package main

import (
	"fmt"
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

	for _, helper := range []string{
		"export function encodePathVar(value: string, multiSegment: boolean): string {",
		"export function splitMultiWildcard(",
	} {
		if !strings.Contains(content, helper) {
			t.Errorf("fixture missing inlined runtime helper %q", helper)
		}
	}

	for _, want := range []string{
		// Single-segment placeholder: the whole value is one segment, so "/" must
		// be escaped along with everything else.
		`url = url.replace("{name}", encodePathVar((request.name ?? "").replace("resources/", ""), false));`,
		// Multi-wildcard ({name=resources/*/versions/*}) expands to one segment per
		// wildcard, matching the route the server registers, and each wildcard's
		// value is split back out of the single proto field and escaped whole.
		`let url = ` + "`" + `${this.baseUrl}/v1/resources/{name_0}/versions/{name_1}` + "`" + `;`,
		`url = url.replace("{name_0}", encodePathVar(splitMultiWildcard(request.name ?? "", "resources/", ["/versions/"], 0), false));`,
		`url = url.replace("{name_1}", encodePathVar(splitMultiWildcard(request.name ?? "", "resources/", ["/versions/"], 1), false));`,
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

// TestTSRouteMatchesGoRoute is the cross-plugin drift guard. The Go plugin's
// ServeMux pattern is what the server actually registers, so a TS client that
// builds a different path shape cannot reach it. This compares the two checked-in
// fixtures directly for the multi-wildcard route, the one shape where the plugins
// previously disagreed: TS emitted a single rest-of-path placeholder whose literal
// "/" separators made any slash-bearing ID miss the route entirely.
func TestTSRouteMatchesGoRoute(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(file), "../..")

	goFixture, err := os.ReadFile(filepath.Join(repoRoot, "internal/testproto/testv1/testv1connect/test_aip.connect.go"))
	if err != nil {
		t.Fatalf("read Go fixture: %v", err)
	}
	tsFixture, err := os.ReadFile(filepath.Join(repoRoot, "internal/testproto/testts/test_aip.ts"))
	if err != nil {
		t.Fatalf("read TS fixture: %v", err)
	}

	const route = "/v1/resources/{name_0}/versions/{name_1}"
	if !strings.Contains(string(goFixture), `yield("GET `+route+`"`) {
		t.Fatalf("Go fixture no longer registers %q — update this test and the TS expectation together", route)
	}
	if !strings.Contains(string(tsFixture), route) {
		t.Errorf("TS fixture does not build %q, so its requests cannot match the route the server registers", route)
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
