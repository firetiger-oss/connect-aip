---
description: Cut a new release of connect-aip — tag, push, verify GoReleaser binaries.
---

# Release

1. Confirm CI is green on `main`:
   ```bash
   gh run list --workflow=ci.yml --branch=main --limit=1
   ```
2. Decide the version bump using semver:
   - **Major**: emitted Go/TS/Py code changes shape (consumers must regenerate AND adjust their code), runtime breaks API, wire format changes.
   - **Minor**: new features, additional emitted symbols (regenerating works without manual changes).
   - **Patch**: bug fixes that don't change emitted output structure.
3. Tag and push:
   ```bash
   git checkout main && git pull
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```
4. Watch the release workflow:
   ```bash
   gh run watch $(gh run list --workflow=release.yml --limit=1 --json databaseId --jq '.[0].databaseId')
   ```
5. Verify the binaries on the Release page — should have 12 archives + checksums.txt:
   - `protoc-gen-aip-{go,ts,py}_VERSION_{linux,darwin}_{amd64,arm64}.tar.gz`
6. Smoke test the freshly published binary:
   ```bash
   go install github.com/firetiger-oss/connect-aip/cmd/protoc-gen-aip-go@vX.Y.Z
   protoc-gen-aip-go --help 2>/dev/null || protoc-gen-aip-go </dev/null  # plugin reads stdin; just confirm it exists
   ```
7. **Python (post-PyPI)**: bump `python/pyproject.toml` version, then:
   ```bash
   cd python && uv build && uv publish
   ```
   (Skip until a PyPI account is configured.)
