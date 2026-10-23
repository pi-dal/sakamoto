#!/usr/bin/env python3
"""Package the shared desktop client for macOS, Windows and Linux."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

TARGETS = [f"{system}-{arch}" for system in ("darwin", "windows", "linux") for arch in ("arm64", "amd64")]


def run(command, **kwargs):
    subprocess.run(command, check=True, **kwargs)


def main():
    host = {"Darwin": "darwin", "Windows": "windows", "Linux": "linux"}[platform.system()]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", action="append", choices=TARGETS)
    parser.add_argument("--all", action="store_true")
    parser.add_argument("--output", type=Path, default=Path("dist/desktop"))
    parser.add_argument("--sign-identity", help="Forward the macOS code signing identity")
    parser.add_argument("--team-id", help="Expected macOS signing team")
    parser.add_argument("--notarize", action="store_true", help="Require signed and notarized macOS release artifacts")
    parser.add_argument("--notary-profile", help="Local notarytool keychain profile; CI uses ASC_* credentials")
    args = parser.parse_args()
    native_arch = "arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64"
    targets = list(dict.fromkeys(TARGETS if args.all else args.target or [f"{host}-{native_arch}"]))
    if any(t.startswith("darwin-") for t in targets) and host != "darwin":
        raise SystemExit("macOS packaging requires a Mac; select Windows/Linux targets on this host")
    root = Path(__file__).resolve().parents[1]
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    version = re.search(r'const version = "([^"]+)"', (root / "cmd/sakamoto/main.go").read_text()).group(1)
    artifacts = []
    darwin_arches = [t.split("-")[1] for t in targets if t.startswith("darwin-")]
    if darwin_arches:
        command = ["python3", str(root / "scripts/build-menubar.py"), "--output", str(output)]
        if args.notarize:
            command += ["--notarize"]
        for name in ("sign_identity", "team_id", "notary_profile"):
            value = getattr(args, name)
            if value:
                command += ["--" + name.replace("_", "-"), value]
        for arch in darwin_arches:
            command += ["--arch", arch]
        run(command, cwd=root)
        mac = json.loads((output / "release-metadata.json").read_text())
        artifacts.extend(dict(item, target=f"darwin-{item['arch']}", lifecycle="sakamoto-supervisor") for item in mac["artifacts"])
    modules = {}
    for name in ("github.com/egoist/mygo", "github.com/oneo-me/onevra/source"):
        modules[name] = json.loads(subprocess.check_output(["go", "mod", "download", "-json", name], cwd=root / "desktop", text=True))
    with tempfile.TemporaryDirectory(prefix="sakamoto-desktop-assets-") as temporary:
        assets = Path(temporary)
        run(["go", "run", "./cmd/desktop-assets", str(root / "assets/app-icon.png"), str(assets)], cwd=root / "desktop")
        for target in targets:
            system, arch = target.split("-")
            if system == "darwin":
                continue
            staging = output / target / "Sakamoto"
            if staging.exists():
                shutil.rmtree(staging)
            binary = staging / "bin" / ("sakamoto-desktop.exe" if system == "windows" else "sakamoto-desktop")
            binary.parent.mkdir(parents=True)
            env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
            flags = "-s -w" + (" -H=windowsgui" if system == "windows" else "")
            run(["go", "build", "-trimpath", f"-ldflags={flags}", "-o", str(binary), "./cmd/sakamoto-menubar"], cwd=root / "desktop", env=env)
            resources = staging / "resources"
            resources.mkdir()
            for name in ("sakamoto.png", "sakamoto.ico"):
                shutil.copy2(assets / name, resources / name)
            shutil.copy2(root / "desktop/README.md", staging / "README.md")
            shutil.copy2(root / "LICENSE", staging / "LICENSE")
            shutil.copy2(root / "NOTICE.md", staging / "NOTICE.md")
            licenses = resources / "licenses"
            licenses.mkdir()
            shutil.copy2(Path(modules["github.com/egoist/mygo"]["Dir"]) / "LICENSE", licenses / "MyGo-MIT.txt")
            onevra = Path(modules["github.com/oneo-me/onevra/source"]["Dir"])
            shutil.copy2(root / "desktop/LICENSE.onevra", licenses / "Onevra-MIT.txt")
            shutil.copy2(root / "desktop/LICENSE.radix", licenses / "Radix-Colors-MIT.txt")
            shutil.copy2(onevra / "components/LICENSE.mygo", licenses / "MyGo-components-MIT.txt")
            shutil.copy2(onevra / "icons/lucide/LICENSE", licenses / "Lucide.txt")
            if system == "linux":
                applications = staging / "share/applications"
                applications.mkdir(parents=True)
                (applications / "dev.pi-dal.sakamoto.desktop").write_text("[Desktop Entry]\nType=Application\nName=Sakamoto\nComment=Connection controls\nExec=sakamoto-desktop --show\nIcon=dev.pi-dal.sakamoto\nTerminal=false\nCategories=Network;\n")
                icons = staging / "share/icons/hicolor/256x256/apps"
                icons.mkdir(parents=True)
                shutil.copy2(assets / "sakamoto.png", icons / "dev.pi-dal.sakamoto.png")
            archive = output / f"sakamoto-desktop-v{version}-{target}{'.zip' if system == 'windows' else '.tar.gz'}"
            if system == "windows":
                with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
                    for item in sorted(staging.rglob("*")):
                        if item.is_file():
                            bundle.write(item, Path("Sakamoto") / item.relative_to(staging))
            else:
                with tarfile.open(archive, "w:gz") as bundle:
                    bundle.add(staging, arcname="Sakamoto")
            artifacts.append({"target": target, "file": archive.name, "bytes": archive.stat().st_size, "binaryBytes": binary.stat().st_size, "sha256": hashlib.new("sha256", archive.read_bytes()).hexdigest(), "lifecycle": "external-core", "signing": "unsigned"})
            print(f"Packaged shared desktop client: {archive}")
    metadata = {"schema": 2, "version": version, "builderHost": host, "gitCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(), "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip()), "modules": {name: value["Version"] for name, value in modules.items()}, "nativeExecutionVerifiedByBuild": False, "artifacts": artifacts}
    (output / "release-metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    (output / "SHA256SUMS").write_text("".join(f"{item['sha256']}  {item['file']}\n" for item in artifacts))


if __name__ == "__main__":
    main()
