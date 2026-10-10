#!/usr/bin/env python3
"""Build the macOS CLI/TUI with the shared Go size policy and package resources."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", action="append", choices=["arm64", "amd64"], help="Default: both supported macOS architectures")
    parser.add_argument("--output", type=Path, default=Path("dist/cli"))
    args = parser.parse_args()
    if os.uname().sysname != "Darwin":
        raise SystemExit("Build the macOS CLI/TUI on macOS with the Xcode command-line tools")
    root = Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    policy = json.loads((root / "scripts/go-size-profile.json").read_text())
    if policy.get("schema") != 1 or not all(isinstance(f, str) and "=" in f for f in policy.get("gcflags", [])):
        raise SystemExit("Unsupported Go compiler policy")
    version = re.search(r'const version = "([^"]+)"', (root / "cmd/sakamoto/main.go").read_text()).group(1)
    artifacts = []
    for arch in dict.fromkeys(args.arch or ["arm64", "amd64"]):
        name = f"sakamoto-v{version}-darwin-{arch}"
        with tempfile.TemporaryDirectory(prefix="sakamoto-cli-") as work:
            package = Path(work) / name
            package.mkdir()
            binary = package / "sakamoto"
            # Preserve upstream macOS TLS, certificate-store and native helpers.
            env = dict(os.environ, GOOS="darwin", GOARCH=arch, CGO_ENABLED="1")
            command = ["go", "build", "-trimpath", "-ldflags=-s -w", *["-gcflags=" + f for f in policy["gcflags"]], "-o", str(binary), "./cmd/sakamoto"]
            subprocess.run(command, cwd=root, env=env, check=True)
            # Cross builds carry Go's linker signature. On macOS finalize and
            # verify an explicit ad-hoc signature for both executable slices.
            if os.uname().sysname == "Darwin":
                subprocess.run(["codesign", "--force", "--sign", "-", str(binary)], check=True)
                subprocess.run(["codesign", "--verify", "--strict", str(binary)], check=True)
            for filename in ["sakamoto.example.yaml", "nodes.example.txt", "LICENSE", "NOTICE.md", "README.md"]:
                shutil.copy2(root / filename, package / filename)
            for directory in ["scripts", "launchd", "docs"]:
                shutil.copytree(root / directory, package / directory, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
            target = output / (name + ".tar.gz")
            with tarfile.open(target, "w:gz") as archive:
                archive.add(package, arcname=name)
            artifacts.append({"file": target.name, "arch": arch, "binaryBytes": binary.stat().st_size, "bytes": target.stat().st_size, "sha256": hashlib.sha256(target.read_bytes()).hexdigest()})
            print(f"Packaged CLI/TUI ({arch}): {target}")
    (output / "SHA256SUMS").write_text("".join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
    (output / "release-metadata.json").write_text(json.dumps({
        "version": version,
        "gitCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip()),
        "compilerPolicySHA256": hashlib.sha256((root / "scripts/go-size-profile.json").read_bytes()).hexdigest(),
        "cgoEnabled": True,
        "artifacts": artifacts,
    }, indent=2) + "\n")


if __name__ == "__main__":
    main()
