"""Tests for path-variable percent-encoding.

Resource IDs are arbitrary strings. Interpolated raw, one containing "/" — e.g. a
deployment environment auto-discovered from a Vercel monorepo build and named
"staging - apps/docs" — splits into an extra path segment, so the request stops
matching its route and the caller gets a content-free 404 instead of ever reaching
the service. Unlike the rest of the Python runtime, the escaping rule is
language-specific (urllib's quote vs Go's url.PathEscape vs encodeURIComponent),
so it gets its own behavioral tests here rather than leaning on the Go suite.
"""

import httpx
import pytest
from connectaip import Client, MethodSpec, PathVar, _escape_path_var
from google.protobuf import empty_pb2, wrappers_pb2


@pytest.mark.parametrize(
    ("val", "placeholder", "want"),
    [
        ("production", "{name}", "production"),
        ("staging - apps/docs", "{name}", "staging%20-%20apps%2Fdocs"),
        ("a?b#c", "{name}", "a%3Fb%23c"),
        ("100%", "{name}", "100%25"),
        # A rest-of-path placeholder deliberately spans several segments, so its
        # own separators stay literal while each segment is still escaped.
        ("abc/versions/v1", "{name...}", "abc/versions/v1"),
        ("a b/c d", "{name...}", "a%20b/c%20d"),
    ],
)
def test_escape_path_var(val: str, placeholder: str, want: str) -> None:
    assert _escape_path_var(val, placeholder) == want


def _request_path_for(url_pattern: str, placeholder: str, name: str) -> bytes:
    """Run one GET through a mock transport and return the path it requested."""
    seen: list[bytes] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request.url.raw_path)
        return httpx.Response(200, json={})

    client: Client = Client(
        session=httpx.Client(transport=httpx.MockTransport(handler)),
        base_url="http://example.test",
        spec=MethodSpec(
            http_method="GET",
            url_pattern=url_pattern,
            path_vars=[PathVar(placeholder=placeholder, prefix="resources/")],
        ),
        response_type=empty_pb2.Empty,
        path_var_fn=lambda req: {placeholder: req.value},
    )

    client.call(wrappers_pb2.StringValue(value=name))
    return seen[0]


def test_client_escapes_slash_in_single_segment_path_var() -> None:
    got = _request_path_for(
        "/v1/resources/{name}", "{name}", "resources/staging - apps/docs"
    )
    assert got == b"/v1/resources/staging%20-%20apps%2Fdocs"


def test_client_keeps_separators_in_rest_of_path_var() -> None:
    got = _request_path_for(
        "/v1/resources/{name...}", "{name...}", "resources/a b/versions/c d"
    )
    assert got == b"/v1/resources/a%20b/versions/c%20d"
