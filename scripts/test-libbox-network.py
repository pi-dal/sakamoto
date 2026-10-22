#!/usr/bin/env python3
"""Test the real pinned libbox patch in an isolated dependency copy."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import importlib.util

REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("network_patch", REPO / "scripts/patch-libbox-network.py")
patcher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(patcher)
source = Path(os.environ.get("SING_BOX_SOURCE", subprocess.check_output(
    ["go", "list", "-m", "-f", "{{.Dir}}", "github.com/sagernet/sing-box"], cwd=REPO, text=True).strip()))
if not source.is_dir():
    raise SystemExit("Pinned libbox source missing; run mise run mobile:tools")
with tempfile.TemporaryDirectory(prefix="sakamoto-network-test-") as temporary:
    root = Path(temporary) / "core"
    shutil.copytree(source, root)
    for p in root.rglob("*"):
        p.chmod(p.stat().st_mode | 0o200)
    patcher.patch(root)
    first = {p: p.read_bytes() for p in root.rglob("*.go")}
    patcher.patch(root)
    assert all(p.read_bytes() == data for p, data in first.items()), "patch is not idempotent"
    # Drift must fail before any earlier source file is modified.
    probe = root / "protocol/tailscale/endpoint.go"
    saved = probe.read_text()
    probe.write_text(saved.replace("fallbackTCPCloser   func()", "fallbackTCPCloser2   func()"))
    # Use a fresh upstream endpoint to exercise validation, not the already
    # patched branch accepted by replace_once.
    broken = (source / "protocol/tailscale/endpoint.go").read_text().replace("fallbackTCPCloser   func()", "fallbackTCPCloser2   func()")
    probe.write_text(broken)
    try:
        patcher.patch(root)
        raise AssertionError("upstream drift was accepted")
    except RuntimeError:
        pass
    assert (root / "experimental/libbox/command_server.go").read_bytes() == first[root / "experimental/libbox/command_server.go"]
    probe.write_text(saved)
    for fixture in (REPO / "scripts/libbox-tests").glob("*.go.txt"):
        package, filename = fixture.name.split("__", 1)
        target = {"tailscale": "protocol/tailscale", "rule": "route/rule", "daemon": "daemon"}[package]
        (root / target / filename.removesuffix(".txt")).write_text(fixture.read_text())
    changed = [str(root / name) for name in ["experimental/libbox/command_server.go", "experimental/libbox/monitor.go", "protocol/tailscale/endpoint.go", "protocol/tailscale/sakamoto_transport.go", "daemon/sakamoto_service.go", "daemon/sakamoto_probe.go", "route/rule/sakamoto_domains.go"]]
    subprocess.run(["gofmt", "-w", *changed], check=True)
    subprocess.run(["go", "mod", "edit", "-require=github.com/pi-dal/sakamoto@v0.0.0", f"-replace=github.com/pi-dal/sakamoto={REPO}"], cwd=root, check=True)
    subprocess.run(["go", "test", "-mod=mod", "-race", "-tags", "with_tailscale,with_gvisor", "-run", "TestSakamoto", "-count=1", "./protocol/tailscale", "./route/rule", "./daemon"], cwd=root, check=True)
print("libbox patch: drift rejection, idempotence and native connection tests passed")
