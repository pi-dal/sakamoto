#!/usr/bin/env python3
"""Enable just the supported DERP bind policy in the pinned iOS stubs.

Apply to a disposable module copy, never to the shared Go module cache.
All other debug knobs remain disabled. The existing magicsock DERP-only bind
branch substitutes a blocking UDP socket, so peer data cannot use direct UDP.
"""
from pathlib import Path
import sys


def patch(root: Path) -> None:
    target = root / "wgengine/magicsock/debugknobs_stubs.go"
    source = target.read_text()
    import_anchor = '"github.com/sagernet/tailscale/types/opt"'
    function_anchor = "func debugAlwaysDERP() bool            { return false }"
    if source.count(import_anchor) != 1 or source.count(function_anchor) != 1:
        raise RuntimeError("Pinned Tailscale iOS DERP adapter drift; inspect upstream before building")
    transport = (root / "wgengine/magicsock/magicsock.go").read_text()
    branch = transport.partition("if debugAlwaysDERP() {")[2].partition("\n\t}")[0]
    if "ruc.setConnLocked(newBlockForeverConn()" not in branch or "return nil" not in branch:
        raise RuntimeError("Pinned DERP-only bind path drift; inspect upstream before building")
    source = source.replace(import_anchor, '"github.com/pi-dal/sakamoto/pkg/mobilerelay"\n\t' + import_anchor)
    source = source.replace(function_anchor, "func debugAlwaysDERP() bool            { return mobilerelay.ForceDERP() }")
    source = source.replace("// All knobs are disabled on iOS and Wasm.", "// Only the app-owned DERP bind policy is enabled; other knobs stay disabled.")
    target.write_text(source)


if __name__ == "__main__":
    patch(Path(sys.argv[1]))
