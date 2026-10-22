#!/usr/bin/env python3
"""Verify real macOS release ZIPs and generate their architecture-aware Homebrew cask."""
import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import re
import subprocess
import tempfile
import zipfile


def capture(command):
    return subprocess.check_output(command, stderr=subprocess.STDOUT, text=True)


def verify_signature(path, team):
    capture(["codesign", "--verify", "--deep", "--strict", str(path)])
    details = capture(["codesign", "--display", "--verbose=4", str(path)])
    if team:
        for required in ("Authority=Developer ID Application:", f"TeamIdentifier={team}", "Timestamp=", "runtime"):
            if required not in details:
                raise ValueError(f"{path.name}: missing signature evidence {required}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, default=Path("dist/desktop"))
    parser.add_argument("--team-id")
    parser.add_argument("--allow-ad-hoc", action="store_true", help="Local packaging verification only; never use for a public cask")
    parser.add_argument("--cask-output", type=Path)
    args = parser.parse_args()
    if not args.allow_ad_hoc and not args.team_id:
        parser.error("Release verification requires --team-id")
    metadata = json.loads((args.directory / "release-metadata.json").read_text())
    version = metadata["version"]
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise ValueError("Expected a stable three-component source version")
    artifacts = {item.get("arch", item.get("target", "").removeprefix("darwin-")): item
                 for item in metadata["artifacts"] if item.get("target", "darwin").startswith("darwin")}
    if set(artifacts) != {"arm64", "amd64"}:
        raise ValueError("Release verification requires exactly both macOS architectures")
    results = []
    for arch, item in sorted(artifacts.items()):
        expected_name = f"sakamoto-menubar-v{version}-darwin-{arch}.zip"
        if item["file"] != expected_name:
            raise ValueError(f"Unexpected archive name: {item['file']}")
        archive = args.directory / expected_name
        digest = hashlib.file_digest(archive.open("rb"), "sha256").hexdigest()
        if digest != item["sha256"] or archive.stat().st_size != item["bytes"]:
            raise ValueError(f"Archive size/checksum mismatch: {archive}")
        if not args.allow_ad_hoc:
            if item.get("signing") != "developer-id" or item.get("notarized") is not True or item.get("teamID") != args.team_id:
                raise ValueError(f"Missing trusted release metadata: {archive}")
            receipt = json.loads((args.directory / item["notaryReceipt"]).read_text())
            if receipt.get("status") != "Accepted":
                raise ValueError(f"Notarization not accepted: {archive}")
        with tempfile.TemporaryDirectory(prefix="sakamoto-release-verify-") as temporary:
            with zipfile.ZipFile(archive) as bundle:
                for name in bundle.namelist():
                    if name.startswith("/") or ".." in Path(name).parts:
                        raise ValueError("Unsafe archive member")
            capture(["ditto", "-x", "-k", str(archive), temporary])
            app = Path(temporary) / "Sakamoto.app"
            info = plistlib.loads((app / "Contents/Info.plist").read_bytes())
            if info["CFBundleShortVersionString"] != version or info["LSUIElement"] is not True:
                raise ValueError("Bundle version or menu bar configuration mismatch")
            binaries = [app / "Contents/MacOS/sakamoto-menubar", app / "Contents/Resources/cli/sakamoto",
                        app / "Contents/Resources/cli/scripts/sakamoto-rescue"]
            for binary in binaries:
                native_arch = "x86_64" if arch == "amd64" else arch
                if capture(["lipo", "-archs", str(binary)]).strip() != native_arch:
                    raise ValueError(f"Unexpected executable architecture: {binary}")
                verify_signature(binary, None if args.allow_ad_hoc else args.team_id)
            verify_signature(app, None if args.allow_ad_hoc else args.team_id)
            if not args.allow_ad_hoc:
                capture(["xcrun", "stapler", "validate", str(app)])
                capture(["spctl", "--assess", "--type", "execute", "--verbose=4", str(app)])
        results.append({"arch": arch, "file": archive.name, "sha256": digest, "verified": True})
    if args.cask_output:
        template = Path(__file__).resolve().parents[1] / "packaging/homebrew/sakamoto-desktop.rb.in"
        cask = template.read_text().replace("@VERSION@", version)
        for arch in artifacts:
            cask = cask.replace(f"@SHA256_{arch.upper()}@", artifacts[arch]["sha256"])
        args.cask_output.parent.mkdir(parents=True, exist_ok=True)
        args.cask_output.write_text(cask)
    receipt = {"schema": 1, "version": version, "policy": "local-ad-hoc" if args.allow_ad_hoc else "developer-id-notarized",
               "teamID": args.team_id, "artifacts": results}
    (args.directory / "verification.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps(receipt, indent=2))


if __name__ == "__main__":
    main()
