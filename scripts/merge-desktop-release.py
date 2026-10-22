#!/usr/bin/env python3
"""Merge CI platform manifests only after checking their actual bytes and release evidence."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess


def main():
    inputs = Path("dist/release-inputs")
    output = Path("dist/release")
    output.mkdir(parents=True, exist_ok=True)
    manifests = [json.loads((inputs / name / "release-metadata.json").read_text()) for name in ("desktop-macos", "desktop-portable")]
    mac, portable = manifests
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    for item in manifests:
        if item["version"] != mac["version"] or item["gitCommit"] != commit or item["dirty"]:
            raise ValueError("Release inputs must be clean builds of the same source commit and version")
    verification = json.loads((inputs / "desktop-macos/verification.json").read_text())
    if verification["policy"] != "developer-id-notarized" or verification["version"] != mac["version"]:
        raise ValueError("Missing macOS release verification")
    artifacts = mac["artifacts"] + portable["artifacts"]
    expected = {f"{system}-{arch}" for system in ("darwin", "windows", "linux") for arch in ("arm64", "amd64")}
    if len(artifacts) != 6 or {item["target"] for item in artifacts} != expected:
        raise ValueError("Expected all six desktop targets")
    for item in artifacts:
        directory = inputs / ("desktop-macos" if item["target"].startswith("darwin-") else "desktop-portable")
        source = directory / item["file"]
        if source.name != item["file"] or hashlib.file_digest(source.open("rb"), "sha256").hexdigest() != item["sha256"] or source.stat().st_size != item["bytes"]:
            raise ValueError("Release artifact name, size or digest mismatch")
        if item["target"].startswith("darwin-"):
            receipt = directory / item["notaryReceipt"]
            if item.get("signing") != "developer-id" or item.get("notarized") is not True or json.loads(receipt.read_text())["status"] != "Accepted":
                raise ValueError("Refusing an unnotarized macOS artifact")
            shutil.copy2(receipt, output / receipt.name)
        shutil.copy2(source, output / source.name)
    merged = dict(mac, schema=3, builderHost="ci-matrix", artifacts=artifacts)
    (output / "release-metadata.json").write_text(json.dumps(merged, indent=2) + "\n")
    (output / "SHA256SUMS").write_text("".join(f"{item['sha256']}  {item['file']}\n" for item in artifacts))
    shutil.copy2(inputs / "desktop-macos/verification.json", output / "verification.json")
    shutil.copy2(inputs / "desktop-macos/sakamoto-desktop.rb", output / "sakamoto-desktop.rb")
    print("Merged six verified desktop artifacts")


if __name__ == "__main__":
    main()
