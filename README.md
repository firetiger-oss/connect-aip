# connect-aip

[![ci](https://github.com/firetiger-oss/connect-aip/actions/workflows/ci.yml/badge.svg)](https://github.com/firetiger-oss/connect-aip/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/firetiger-oss/connect-aip.svg)](https://pkg.go.dev/github.com/firetiger-oss/connect-aip)

Connect RPC → Google AIP-shaped REST codegen for Go, TypeScript, and Python.

## Motivation

[Connect RPC](https://connectrpc.com) speaks gRPC, gRPC-Web, and JSON-over-POST out of the box. None of these is the REST shape that browsers, third-party API consumers, and AIP-aligned API gateways expect: collection-rooted paths, AIP-160 filtering, AIP-158 pagination, idiomatic HTTP verbs.

`connect-aip` fills the gap with three protoc plugins (`protoc-gen-aip-go`, `protoc-gen-aip-ts`, `protoc-gen-aip-py`) that read `google.api.http` annotations and emit AIP-130-style REST handlers and clients in three languages, plus a small Go and Python runtime that does the request forwarding.

The codegen is opinionated toward [Google AIP](https://google.aip.dev) conventions. Non-AIP `google.api.http` rules may technically work but are not the supported path; the design choice is reflected in the project's name.

Server-streaming methods are routed through [`firetiger-oss/connect-sse`](https://github.com/firetiger-oss/connect-sse) so browsers can consume them over plain `fetch`.

## Install

### Go (codegen + runtime)

```sh
go install github.com/firetiger-oss/connect-aip/cmd/protoc-gen-aip-go@latest
```

Prebuilt binaries for `linux/{amd64,arm64}` and `darwin/{amd64,arm64}` are attached to each [GitHub Release](https://github.com/firetiger-oss/connect-aip/releases).

### TypeScript (codegen only — runtime is inlined)

```sh
go install github.com/firetiger-oss/connect-aip/cmd/protoc-gen-aip-ts@latest
```

### Python (codegen + runtime)

```sh
go install github.com/firetiger-oss/connect-aip/cmd/protoc-gen-aip-py@latest
pip install connectaip
```

## Usage

Annotate your Connect RPC service with `google.api.http` rules, then run any of the plugins via `buf`:

```yaml
# buf.gen.yaml
plugins:
  - local: protoc-gen-aip-go
    out: proto/go
    opt: [paths=source_relative]
    strategy: all
```

For each service with at least one usable HTTP rule, the Go plugin emits a `*_aip.connect.go` file alongside the standard `*.connect.go`, exposing `NewServiceAIPHandler` (an `iter.Seq2[string, http.Handler]` for `http.ServeMux`) and `NewServiceAIPClient` (a typed REST client wrapping `connect.HTTPClient`).

The TypeScript plugin emits `*_aip.ts` with a `*AIPClient` class per service. The runtime is inlined into each generated file (no separate npm package).

The Python plugin emits `*_aip.py` with a `*AIPClient` class per service that uses `httpx` for transport. Install the `connectaip` runtime alongside.

## License

Apache 2.0
