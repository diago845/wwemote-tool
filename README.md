# WWEmoteTool

Configuration tool for the WWEmote.
The mod works without it; the tool only generates local expression configs.

This is the complete, unobfuscated source of the shipped `WWEmoteTool.exe`,
plus the build script and assets needed to reproduce the release package.

## Build

Requires Go 1.27+ on `PATH` and Python 3.

```sh
python build_release.py
```

Produces `release/WWEmote-1.6.0-go.zip`. To verify an existing zip without
rebuilding, use `python build_release.py --check-only`.

The build is reproducible; the expected SHA-256 of `WWEmoteTool.exe` inside
the package is:

```
6a262973a02405ba2775b5db97ef890c1e27be4059afa8a749de7f38836c5053
```

The build passes `-buildvcs=false` so a fresh clone produces the same hash.

## License

MIT. See [LICENSE](LICENSE). The bundled `third_party/go-webview2` keeps its
own MIT notice.
