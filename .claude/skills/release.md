---
description: Cut a new release of connect-aip — tag, push, verify GoReleaser binaries + PyPI publish.
---

# Release

## One-time setup (do once per repo, before the first release)

### PyPI Trusted Publishers

PyPI publishing uses [Trusted Publishers (OIDC)](https://docs.pypi.org/trusted-publishers/) — no token to manage. Configure once:

1. Make sure a PyPI account exists for the org publishing this project (e.g. `firetiger-oss`). If `connectaip` is a brand-new project on PyPI, you'll register a *pending* publisher (no project exists yet) so the first release can claim the name:
   - Go to https://pypi.org/manage/account/publishing/
   - Under "Add a new pending publisher", fill in:
     - **PyPI Project Name**: `connectaip`
     - **Owner**: `firetiger-oss`
     - **Repository name**: `connect-aip`
     - **Workflow name**: `release.yml`
     - **Environment name**: `pypi`
2. In the GitHub repo, create the `pypi` environment (Settings → Environments → New environment → name: `pypi`). The release workflow uses this environment to scope OIDC.
   - Optional: add a required reviewer to the environment if you want a manual gate before each publish.

Once the project exists on PyPI (after the first release succeeds), the "pending" publisher converts into a regular Trusted Publisher and subsequent releases require no further action.

### `FIRETIGER_OSS_PAT` (only while connect-sse is private)

The Go test + release jobs need to fetch `github.com/firetiger-oss/connect-sse` via go modules. While that repo is private, Settings → Secrets and variables → Actions → New repository secret:

- Name: `FIRETIGER_OSS_PAT`
- Value: a fine-grained PAT with `Contents: Read-only` on `firetiger-oss/connect-sse` (and `firetiger-oss/connect-aip` if needed for self-fetch).

Once both repos go public, the secret + the `Configure git for private firetiger-oss modules` step in the workflows can be deleted.

## Per-release flow

1. Confirm CI is green on `main`:
   ```bash
   gh run list --workflow=ci.yml --branch=main --limit=1
   ```
2. Decide the version bump using semver:
   - **Major**: emitted Go/TS/Py code changes shape (consumers must regenerate AND adjust their code), runtime breaks API, wire format changes.
   - **Minor**: new features, additional emitted symbols (regenerating works without manual changes).
   - **Patch**: bug fixes that don't change emitted output structure.
3. **Bump `python/pyproject.toml` version to match the tag** (the workflow asserts `version = "X.Y.Z"` matches the `vX.Y.Z` tag and refuses to publish on mismatch). Commit the bump.
4. Tag and push:
   ```bash
   git checkout main && git pull
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```
5. Watch the release workflow. Two jobs run in parallel:
   ```bash
   gh run watch $(gh run list --workflow=release.yml --limit=1 --json databaseId --jq '.[0].databaseId')
   ```
   - `goreleaser`: builds and attaches 12 binary archives + checksums to the GitHub Release.
   - `pypi`: builds sdist + wheel and publishes to PyPI via OIDC.
6. Verify:
   - GitHub Releases page should have 12 archives + `checksums.txt`.
   - https://pypi.org/project/connectaip/X.Y.Z/ should be live within ~1 minute.
   - Smoke test:
     ```bash
     go install github.com/firetiger-oss/connect-aip/cmd/protoc-gen-aip-go@vX.Y.Z
     pip install connectaip==X.Y.Z
     python -c "from connectaip import Client, MethodSpec, SSEClient, PathVar; print('ok')"
     ```
