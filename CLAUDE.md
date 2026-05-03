# connect-aip

Three protoc plugins (Go, TypeScript, Python) that consume `google.api.http` annotations on Connect RPC services and emit AIP-130-shaped REST handlers + REST clients. The TypeScript runtime is inlined into emitted code by design (no separate npm package). Server-streaming methods produce SSE handlers via `firetiger-oss/connect-sse`.

## Why "AIP" not "REST"

The codegen is opinionated toward Google AIP conventions (resource-collection paths, AIP-160 filtering, AIP-158 pagination, idiomatic HTTP verbs). Consumers writing weird non-AIP `google.api.http` rules may technically work but aren't a supported path. Naming makes the intent clear and prevents scope creep.

## Repo layout

- Go runtime at module root (`client.go`, `forward.go`, `loopback.go`).
- 3 protoc plugins under `cmd/protoc-gen-aip-{go,ts,py}/`.
- Python runtime under `python/src/connectaip/`.
- TS runtime: none — the codegen inlines it into every emitted `*_aip.ts` file.
- Test fixture proto: `internal/testproto/test.proto`. The Go plugin runs against it during `go test`; the generated AIP file (`internal/testproto/testv1/testv1connect/test_aip.connect.go`) is checked in.

## Build & test

```sh
go test -race ./...
go install ./cmd/...
cd python && uv run pytest
```

Each language stack tests independently.

## The "all three codegens stay aligned" rule

Any new HTTP-rule feature (e.g. supporting bytes-typed path params, a new query encoding, a new AIP convention) must land in **all three plugins** in one PR — Go, TS, Py — plus runtime support in the Go and Python packages.

The TS plugin inlines its own runtime, so TS feature work means editing `cmd/protoc-gen-aip-ts/main.go` runtime emission, not a separate package.

Each plugin needs a corresponding regression test against the fixture proto.

**Why this matters**: the three languages are public API surface in lockstep; drifting one is a silent regression nobody catches until a downstream consumer files a bug.

## Test fixture regeneration

When `internal/testproto/test.proto` changes, regenerate via:

```sh
go install ./cmd/protoc-gen-aip-go
cd internal/testproto && PATH=$HOME/go/bin:$PATH buf generate
```

Check the regenerated `.pb.go`, `*_connect.go`, and `*_aip.connect.go` into git. See `.claude/skills/regenerate-fixtures.md` for the canonical flow.

## Codegen testing pattern

`cmd/protoc-gen-aip-go/main_test.go` reads the AIP-generated fixture and asserts on emitted Go source structure (compression, SSE handler, POST registration, no legacy REST symbols). Use the same pattern when adding TS/Py invariants — pre-generate, check in, assert.

## Release cut

Tag `vX.Y.Z`. GoReleaser builds `protoc-gen-aip-{go,ts,py}` × `{linux,darwin}/{amd64,arm64}` archives with SHA256 sums. See `.claude/skills/release.md`.

Manual: bump `python/pyproject.toml` version, `cd python && uv build && uv publish` (only after PyPI account exists; until then leave a TODO).

## Backward-compat rule

Emitted Go/TS/Py code is the public API. A change that requires consumers to regenerate their code is a **breaking change**. A change that requires consumers to update only their `go install`'d plugin binary is a minor change.

## Hands-off areas

- Don't reach into `connectrpc.com/connect` internals — keep the runtime surface to documented public APIs.
- SSE handling is delegated to `firetiger-oss/connect-sse`; don't reimplement framing here.
- When a wire change in `connect-sse` lands, this repo needs a matching `go.mod` bump + new tag.
