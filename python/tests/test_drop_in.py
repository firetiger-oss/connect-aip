"""Regression test for protoc-gen-aip-py emitted shape.

The Python AIP client is meant to be drop-in compatible with downstream
connect-python clients: methods take raw protobuf messages, return raw
protobuf messages, and accept a per-call ``headers`` kwarg. This test
asserts the generated fixture preserves that shape so future generator
edits don't silently regress the API.

Regenerate the fixture if test.proto changes::

    go install ./cmd/protoc-gen-aip-py
    cd internal/testproto && PATH=$HOME/go/bin:$PATH buf generate --template buf.gen.py.yaml
"""

import re
from pathlib import Path


_FIXTURE = (
    Path(__file__).resolve().parent.parent.parent
    / "internal"
    / "testproto"
    / "testpy"
    / "test_aip.py"
)


def _content() -> str:
    if not _FIXTURE.exists():
        raise AssertionError(
            f"missing fixture {_FIXTURE} - run "
            "`cd internal/testproto && buf generate --template buf.gen.py.yaml` to regenerate"
        )
    return _FIXTURE.read_text()


def test_unary_method_takes_raw_message_and_per_call_headers() -> None:
    content = _content()
    assert "def create_resource(" in content
    assert "request: pb2.CreateResourceRequest," in content
    assert "headers: Mapping[str, str] | None = None," in content
    assert ") -> pb2.CreateResourceResponse:" in content


def test_streaming_method_returns_iterator_of_raw_messages() -> None:
    content = _content()
    assert "def stream_resources(" in content
    assert ") -> Iterator[pb2.CreateResourceResponse]:" in content


def test_external_return_type_uses_well_known_module() -> None:
    content = _content()
    assert "from google.protobuf import empty_pb2" in content
    assert ") -> empty_pb2.Empty:" in content
    # No bare `pb2.Empty` — only `empty_pb2.Empty` is permitted, since Empty
    # lives in google.protobuf, not the local proto file.
    assert not re.search(r"(?<!empty_)pb2\.Empty\b", content)


def test_class_does_not_define_aip_specific_request_wrapper() -> None:
    content = _content()
    # No connect.Request[T] / connect.Response[T] wrappers - the AIP plugin
    # already uses raw protobuf messages, matching the connect-python client
    # shape so callers can drop it in.
    assert "connect.Request" not in content
    assert "connect.Response" not in content
