import json as _json
from collections.abc import Callable, Iterator, Mapping
from dataclasses import dataclass, field
from typing import Generic, TypeVar, cast

import httpx
from connectrpc.code import Code
from connectrpc.errors import ConnectError
from google.protobuf import json_format
from google.protobuf.message import Message

Req = TypeVar("Req", bound=Message)
Resp = TypeVar("Resp", bound=Message)

_CODE_STRINGS: dict[str, Code] = {
    "canceled": Code.CANCELED,
    "unknown": Code.UNKNOWN,
    "invalid_argument": Code.INVALID_ARGUMENT,
    "deadline_exceeded": Code.DEADLINE_EXCEEDED,
    "not_found": Code.NOT_FOUND,
    "already_exists": Code.ALREADY_EXISTS,
    "permission_denied": Code.PERMISSION_DENIED,
    "resource_exhausted": Code.RESOURCE_EXHAUSTED,
    "failed_precondition": Code.FAILED_PRECONDITION,
    "aborted": Code.ABORTED,
    "out_of_range": Code.OUT_OF_RANGE,
    "unimplemented": Code.UNIMPLEMENTED,
    "internal": Code.INTERNAL,
    "unavailable": Code.UNAVAILABLE,
    "data_loss": Code.DATA_LOSS,
    "unauthenticated": Code.UNAUTHENTICATED,
}

_HTTP_STATUS_TO_CODE: dict[int, Code] = {
    400: Code.INVALID_ARGUMENT,
    401: Code.UNAUTHENTICATED,
    403: Code.PERMISSION_DENIED,
    404: Code.NOT_FOUND,
    409: Code.ALREADY_EXISTS,
    412: Code.FAILED_PRECONDITION,
    429: Code.RESOURCE_EXHAUSTED,
    501: Code.UNIMPLEMENTED,
    503: Code.UNAVAILABLE,
    504: Code.DEADLINE_EXCEEDED,
}


def _parse_error_response(response: httpx.Response) -> ConnectError:
    """Parse an HTTP error response into a ConnectError with the appropriate code."""
    try:
        body = response.json()
        code_str = body.get("code", "")
        message = body.get("message", "")
        if code_str in _CODE_STRINGS:
            return ConnectError(_CODE_STRINGS[code_str], message)
    except Exception:
        pass
    code = _HTTP_STATUS_TO_CODE.get(response.status_code, Code.INTERNAL)
    return ConnectError(code, response.text)


def _check_trailer_error(data: str) -> None:
    """Parse end-of-stream trailer and raise ConnectError if it contains an error."""
    try:
        trailer = _json.loads(data)
    except (ValueError, _json.JSONDecodeError):
        return
    err = trailer.get("error")
    if not err:
        return
    code_str = err.get("code", "")
    message = err.get("message", "")
    code = _CODE_STRINGS.get(code_str, Code.INTERNAL)
    raise ConnectError(code, message)


@dataclass
class PathVar:
    placeholder: str
    prefix: str = ""


@dataclass
class MethodSpec:
    http_method: str
    url_pattern: str
    path_vars: list[PathVar] = field(default_factory=list)


class Client(Generic[Req, Resp]):
    def __init__(
        self,
        session: httpx.Client,
        base_url: str,
        spec: MethodSpec,
        response_type: type[Resp],
        path_var_fn: Callable[[Req], dict[str, str]] | None = None,
        query_fn: Callable[[Req], dict[str, str]] | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> None:
        self._session = session
        self._base_url = base_url.rstrip("/")
        self._spec = spec
        self._response_type = response_type
        self._path_var_fn = path_var_fn
        self._query_fn = query_fn
        self._headers = dict(headers) if headers else {}

    def call(
        self,
        request: Req,
        *,
        headers: Mapping[str, str] | None = None,
        timeout: float | None = None,
    ) -> Resp:
        url = self._base_url + self._spec.url_pattern

        if self._path_var_fn:
            path_vars = self._path_var_fn(request)
            for pv in self._spec.path_vars:
                if pv.placeholder in path_vars:
                    val = path_vars[pv.placeholder]
                    if pv.prefix:
                        val = val.removeprefix(pv.prefix)
                    url = url.replace(pv.placeholder, val)

        req_headers = {**self._headers}
        if headers:
            req_headers.update(headers)
        req_headers["Accept"] = "application/json"

        if self._spec.http_method in ("GET", "DELETE"):
            params: dict[str, str] = {}
            if self._query_fn:
                params = {k: v for k, v in self._query_fn(request).items() if v}
            response = self._session.request(
                self._spec.http_method,
                url,
                params=params or None,
                headers=req_headers,
                timeout=timeout,
            )
        else:
            req_headers["Content-Type"] = "application/json"
            body = json_format.MessageToDict(
                cast(Message, request), preserving_proto_field_name=True
            )
            response = self._session.request(
                self._spec.http_method,
                url,
                json=body,
                headers=req_headers,
                timeout=timeout,
            )

        if not response.is_success:
            raise _parse_error_response(response)

        result = self._response_type()
        json_format.ParseDict(response.json(), result)
        return result


class SSEClient(Generic[Req, Resp]):
    """Client for server-streaming REST methods via SSE (Server-Sent Events).

    The server returns text/event-stream where each SSE event contains a
    proto-JSON message. The client sends a POST with a nested request envelope
    matching the connectsse protocol.
    """

    def __init__(
        self,
        session: httpx.Client,
        base_url: str,
        procedure: str,
        url_pattern: str,
        response_type: type[Resp],
        path_var_fn: Callable[[Req], dict[str, str]] | None = None,
        path_vars: list[PathVar] | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> None:
        self._session = session
        self._base_url = base_url.rstrip("/")
        self._procedure = procedure
        self._url_pattern = url_pattern
        self._response_type = response_type
        self._path_var_fn = path_var_fn
        self._path_vars = {pv.placeholder: pv for pv in (path_vars or [])}
        self._headers = dict(headers) if headers else {}

    def stream(
        self,
        request: Req,
        *,
        headers: Mapping[str, str] | None = None,
        timeout: float | None = None,
    ) -> Iterator[Resp]:
        url = self._base_url + self._url_pattern

        if self._path_var_fn:
            path_vars = self._path_var_fn(request)
            for placeholder, val in path_vars.items():
                pv = self._path_vars.get(placeholder)
                if pv and pv.prefix:
                    val = val.removeprefix(pv.prefix)
                url = url.replace(placeholder, val)

        message = json_format.MessageToDict(
            cast(Message, request), preserving_proto_field_name=True
        )

        envelope = {
            "procedure": self._procedure,
            "header": {"Content-Type": "application/json"},
            "message": message,
        }

        req_headers = {**self._headers}
        if headers:
            req_headers.update(headers)
        req_headers["Content-Type"] = "application/json"
        req_headers["Accept"] = "text/event-stream"

        with self._session.stream(
            "POST",
            url,
            json=envelope,
            headers=req_headers,
            timeout=timeout,
        ) as response:
            response.raise_for_status()
            yield from self._parse_sse_stream(response)

    def _parse_sse_stream(self, response: httpx.Response) -> Iterator[Resp]:
        flags = 0
        data_lines: list[str] = []

        for line in response.iter_lines():
            if line == "":
                if data_lines:
                    data = "\n".join(data_lines)
                    if flags & 2 != 0:
                        _check_trailer_error(data)
                    else:
                        result = self._response_type()
                        json_format.Parse(data, result)
                        yield result
                    data_lines = []
                    flags = 0
                continue

            if line.startswith(":flags "):
                try:
                    flags = int(line[7:])
                except ValueError:
                    pass
                continue

            if line.startswith(":"):
                continue

            if line.startswith("data: "):
                data_lines.append(line[6:])
            elif line.startswith("data:"):
                data_lines.append(line[5:])
