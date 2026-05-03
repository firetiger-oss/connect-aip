"""Smoke tests for the connectaip runtime — confirms the public API surface
is importable and the dataclass shapes are stable. Functional tests live in
the Go test suite (the Go runtime mirrors the Python runtime closely enough
that one set of behavioral tests covers both); these tests catch import-time
and signature regressions only.
"""

from connectaip import Client, MethodSpec, PathVar, SSEClient


def test_methodspec_construction() -> None:
    spec = MethodSpec(http_method="POST", url_pattern="/v1/resources")
    assert spec.http_method == "POST"
    assert spec.url_pattern == "/v1/resources"
    assert spec.path_vars == []


def test_pathvar_construction() -> None:
    pv = PathVar(placeholder="{name}", prefix="resources/")
    assert pv.placeholder == "{name}"
    assert pv.prefix == "resources/"


def test_client_and_sseclient_importable() -> None:
    # Just confirm the symbols are exported. Functional behavior is covered by
    # downstream integration tests against a real server.
    assert Client is not None
    assert SSEClient is not None
