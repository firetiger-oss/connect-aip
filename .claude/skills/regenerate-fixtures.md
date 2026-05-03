---
description: Regenerate the test fixture stubs after editing internal/testproto/test.proto.
---

# Regenerate fixtures

Run this whenever `internal/testproto/test.proto` changes. The Go plugin's tests assert on the emitted output, so the fixture must be in sync with the codegen for `go test` to be meaningful.

1. Build + install the AIP Go plugin into `$GOBIN`:
   ```bash
   go install ./cmd/protoc-gen-aip-go
   ```
2. Run `buf generate` from inside the testproto module so it can find the plugin on `$PATH`:
   ```bash
   cd internal/testproto && PATH=$HOME/go/bin:$PATH buf generate
   ```
3. Three files should be regenerated:
   - `internal/testproto/testv1/test.pb.go` (protocolbuffers/go)
   - `internal/testproto/testv1/testv1connect/test.connect.go` (connectrpc/go)
   - `internal/testproto/testv1/testv1connect/test_aip.connect.go` (protoc-gen-aip-go)
4. Run the test suite to confirm assertions still hold:
   ```bash
   go test -race ./...
   ```
   - If `cmd/protoc-gen-aip-go/main_test.go` fails, decide whether the assertion needs to update (deliberate change) or whether the codegen regressed (revert).
5. Commit the `.proto` and all three regenerated `.go` files together. Reviewers should be able to see the proto change → emitted-code change as a single diff.
