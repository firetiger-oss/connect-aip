---
description: Add a new HTTP-rule capability across all three codegens (Go/TS/Py) consistently.
---

# Add codegen feature

The three codegens are public API surface in lockstep. A feature that lands in 1/3 languages is a regression vector — downstream consumers will silently get inconsistent client/handler behavior across services.

When adding a new HTTP-rule capability (e.g. bytes-typed path params, a new query encoding, a new AIP convention):

1. **Decide the wire shape.** Where does the new value travel — URL, query string, body? What does the JSON look like in TS/Py? What's the Go struct shape?
2. **Land it in the Go plugin first.**
   - Update `cmd/protoc-gen-aip-go/main.go`.
   - Add a fixture method exercising it in `internal/testproto/test.proto`.
   - Add a regression assertion in `cmd/protoc-gen-aip-go/main_test.go`.
   - Run `regenerate-fixtures.md`.
   - Confirm `go test -race ./...` passes.
3. **Mirror in the TS plugin.**
   - Update `cmd/protoc-gen-aip-ts/main.go`.
   - The TS runtime is inlined — feature support that needs new client-side code goes into the emitted `*_aip.ts` content, not a separate package.
   - Add a TS smoke test that runs the plugin against `internal/testproto/test.proto` and asserts on the emitted output.
4. **Mirror in the Py plugin.**
   - Update `cmd/protoc-gen-aip-py/main.go`.
   - If the feature needs new runtime support, edit `python/src/connectaip/__init__.py`.
   - Add a Python smoke test in `python/tests/`.
5. **Update the Go runtime** (`client.go` / `forward.go`) if the feature needs request/response handling beyond the existing `Forward` / `ForwardWithBody` / `ForwardWithPathVars` codepaths.
6. **Bump the README usage section** if the feature is user-visible.
7. **Don't open the PR until all three plugins agree.** A half-done feature in 1/3 languages is the bug pattern this rule exists to prevent. If the change is large, split into "preparatory refactor" PRs that don't change emitted output, then one PR that adds the feature in lockstep across all three.
