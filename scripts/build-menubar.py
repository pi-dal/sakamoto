#!/usr/bin/env python3
"""Build macOS menu bar apps, optionally signed with Developer ID and notarized."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import shutil
import subprocess
import tempfile


def run(command, **kwargs):
    subprocess.run(command, check=True, **kwargs)


def notarize(app, archive, receipt, profile):
    run(["ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", str(app), str(archive)])
    if profile:
        auth = ["--keychain-profile", profile]
    else:
        auth = ["--key", os.environ["ASC_PRIVATE_KEY_PATH"], "--key-id", os.environ["ASC_KEY_ID"], "--issuer", os.environ["ASC_ISSUER_ID"]]
    result = subprocess.run(["xcrun", "notarytool", "submit", str(archive), *auth, "--wait", "--timeout", "20m", "--output-format", "json"], capture_output=True, text=True, timeout=1500)
    # Preserve Apple's public build receipt even when the submission is rejected.
    receipt.write_text(result.stdout)
    try:
        status = json.loads(result.stdout)
    except json.JSONDecodeError:
        raise SystemExit(f"Notarization failed: {result.stderr}")
    if result.returncode or status.get("status") != "Accepted":
        if status.get("id"):
            subprocess.run(["xcrun", "notarytool", "log", status["id"], *auth, str(receipt.with_suffix(".log.json"))], check=False)
        raise SystemExit(f"Notarization was not accepted; inspect {receipt}")
    run(["xcrun", "stapler", "staple", str(app)])
    run(["xcrun", "stapler", "validate", str(app)])
    run(["codesign", "--verify", "--deep", "--strict", str(app)])
    run(["spctl", "--assess", "--type", "execute", "--verbose=4", str(app)])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", action="append", choices=["arm64", "amd64"])
    parser.add_argument("--output", type=Path, default=Path("dist/desktop"))
    parser.add_argument("--sign-identity", default=os.environ.get("MACOS_SIGN_IDENTITY", "-"), help="Default: local ad-hoc signature")
    parser.add_argument("--team-id", default=os.environ.get("MACOS_TEAM_ID"))
    parser.add_argument("--notarize", action="store_true", help="Require Developer ID, Apple acceptance and a stapled ticket")
    parser.add_argument("--notary-profile", default=os.environ.get("NOTARY_KEYCHAIN_PROFILE"))
    args = parser.parse_args()
    if args.notarize:
        if args.sign_identity == "-" or not args.team_id:
            parser.error("Notarization requires a Developer ID identity and --team-id")
        if not args.notary_profile:
            for name in ("ASC_PRIVATE_KEY_PATH", "ASC_KEY_ID", "ASC_ISSUER_ID"):
                if not os.environ.get(name):
                    parser.error(f"Notarization requires {name} or --notary-profile")
    if platform.system() != "Darwin":
        raise SystemExit("Build the macOS app on macOS with Xcode command-line tools")
    root = Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    version = re.search(r'const version = "([^"]+)"', (root / "cmd/sakamoto/main.go").read_text()).group(1)
    modules = json.loads(subprocess.check_output(["go", "list", "-m", "-json", "github.com/egoist/mygo"], cwd=root / "desktop", text=True))
    onevra = json.loads(subprocess.check_output(["go", "list", "-m", "-json", "github.com/oneo-me/onevra/source"], cwd=root / "desktop", text=True))
    artifacts = []
    for arch in dict.fromkeys(args.arch or ["arm64" if platform.machine() == "arm64" else "amd64"]):
        target = output / ("darwin-" + arch)
        target.mkdir(exist_ok=True)
        app = target / "Sakamoto.app"
        # Rebuild only this script's named output bundle, never an installed app.
        if app.exists():
            shutil.rmtree(app)
        contents = app / "Contents"
        executable = contents / "MacOS" / "sakamoto-menubar"
        resources = contents / "Resources"
        cli = resources / "cli"
        executable.parent.mkdir(parents=True)
        cli.mkdir(parents=True)
        env = dict(os.environ, GOOS="darwin", GOARCH=arch, CGO_ENABLED="1")
        run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(executable), "./cmd/sakamoto-menubar"], cwd=root / "desktop", env=env)
        run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(cli / "sakamoto"), "./cmd/sakamoto"], cwd=root, env=env)
        for name in ["sakamoto.example.yaml", "nodes.example.txt", "LICENSE", "NOTICE.md", "README.md"]:
            shutil.copy2(root / name, cli / name)
        for name in ["scripts", "launchd", "docs"]:
            shutil.copytree(root / name, cli / name, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        rescue = cli / "scripts/sakamoto-rescue"
        run(["go", "build", "-trimpath", f"-ldflags=-s -w -X main.version={version}", "-o", str(rescue), "./cmd/sakamoto-rescue"], cwd=root, env=dict(env, CGO_ENABLED="0"))
        shutil.copy2(root / "LICENSE", resources / "LICENSE")
        shutil.copy2(root / "NOTICE.md", resources / "NOTICE.md")
        licenses = resources / "licenses"
        licenses.mkdir()
        shutil.copy2(Path(modules["Dir"]) / "LICENSE", licenses / "MyGo-MIT.txt")
        # Onevra's license is at the repository root, outside its source module;
        # the published module carries the same MIT grant for adapted widgets.
        shutil.copy2(Path(onevra["Dir"]) / "components" / "LICENSE.mygo", licenses / "MyGo-components-MIT.txt")
        shutil.copy2(Path(onevra["Dir"]) / "icons" / "lucide" / "LICENSE", licenses / "Lucide.txt")
        shutil.copy2(root / "desktop" / "LICENSE.onevra", licenses / "Onevra-MIT.txt")
        with tempfile.TemporaryDirectory(prefix="sakamoto-icon-") as work:
            iconset = Path(work) / "AppIcon.iconset"
            iconset.mkdir()
            for size in [16, 32, 128, 256, 512]:
                for scale in [1, 2]:
                    suffix = "@2x" if scale == 2 else ""
                    run(["sips", "-z", str(size * scale), str(size * scale), str(root / "assets/app-icon.png"), "--out", str(iconset / f"icon_{size}x{size}{suffix}.png")], stdout=subprocess.DEVNULL)
            run(["iconutil", "-c", "icns", str(iconset), "-o", str(resources / "AppIcon.icns")])
        info = {
            "CFBundleIdentifier": "dev.pi-dal.sakamoto.menubar",
            "CFBundleName": "Sakamoto", "CFBundleDisplayName": "Sakamoto",
            "CFBundleExecutable": "sakamoto-menubar", "CFBundlePackageType": "APPL",
            "CFBundleIconFile": "AppIcon", "CFBundleShortVersionString": version,
            "CFBundleVersion": version, "LSMinimumSystemVersion": "12.0",
            "LSUIElement": True, "NSHighResolutionCapable": True,
            "NSHumanReadableCopyright": "Copyright 2026 pi-dal. GPL-3.0-or-later.",
        }
        with (contents / "Info.plist").open("wb") as file:
            plistlib.dump(info, file)
        signing = ["codesign", "--force", "--sign", args.sign_identity]
        if args.sign_identity != "-":
            signing += ["--options", "runtime", "--timestamp"]
        run(signing + [str(rescue)])
        run(signing + [str(cli / "sakamoto")])
        run(signing + [str(app)])
        run(["codesign", "--verify", "--deep", "--strict", str(app)])
        if args.notarize:
            for signed in (rescue, cli / "sakamoto", app):
                details = subprocess.check_output(["codesign", "--display", "--verbose=4", str(signed)], stderr=subprocess.STDOUT, text=True)
                for required in ("Authority=Developer ID Application:", f"TeamIdentifier={args.team_id}", "Timestamp=", "runtime"):
                    if required not in details:
                        raise SystemExit(f"Unexpected signing identity/options for {signed.name}: missing {required}")
        archive = output / f"sakamoto-menubar-v{version}-darwin-{arch}.zip"
        receipt = output / f"notary-{arch}.json"
        if args.notarize:
            # Submit a separate ZIP; the public archive is created only after stapling.
            with tempfile.TemporaryDirectory(prefix="sakamoto-notary-") as work:
                notarize(app, Path(work) / archive.name, receipt, args.notary_profile)
        run(["ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", str(app), str(archive)])
        artifacts.append({"file": archive.name, "arch": arch, "bytes": archive.stat().st_size, "binaryBytes": executable.stat().st_size, "sha256": hashlib.new("sha256", archive.read_bytes()).hexdigest(),
                          "signing": "ad-hoc" if args.sign_identity == "-" else "developer-id" if args.notarize else "identity",
                          "notarized": args.notarize, "teamID": args.team_id if args.notarize else None,
                          "notaryReceipt": receipt.name if args.notarize else None})
        print(f"Packaged menu bar app: {app}")
    (output / "SHA256SUMS").write_text("".join(f"{item['sha256']}  {item['file']}\n" for item in artifacts))
    (output / "release-metadata.json").write_text(json.dumps({
        "schema": 1, "version": version,
        "gitCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip()),
        "mygo": modules["Version"], "onevra": onevra["Version"],
        "cgoEnabled": True, "signing": "ad-hoc" if args.sign_identity == "-" else "developer-id" if args.notarize else "identity", "notarized": args.notarize,
        "artifacts": artifacts,
    }, indent=2) + "\n")


if __name__ == "__main__":
    main()
