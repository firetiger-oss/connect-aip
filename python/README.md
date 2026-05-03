# connectaip

Python runtime for the [`protoc-gen-aip-py`](https://github.com/firetiger-oss/connect-aip) plugin.

This package is **not intended to be used directly**. Run `protoc-gen-aip-py` against your proto files; the generated `*_aip.py` modules import the runtime symbols (`Client`, `MethodSpec`, `PathVar`, `SSEClient`) from `connectaip`.

## Install

```bash
pip install connectaip
```

## Usage

See [github.com/firetiger-oss/connect-aip](https://github.com/firetiger-oss/connect-aip) for end-to-end usage with the `protoc-gen-aip-py` codegen plugin.

## License

Apache 2.0
