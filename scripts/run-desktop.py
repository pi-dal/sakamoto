#!/usr/bin/env python3
"""Launch the packaged desktop client on the current platform."""
import argparse
from pathlib import Path
import platform
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify", type=Path)
    args, forwarded = parser.parse_known_args()
    root = Path(__file__).resolve().parents[1]
    system = {"Darwin": "darwin", "Windows": "windows", "Linux": "linux"}[platform.system()]
    arch = "arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64"
    directory = root / "dist/desktop" / f"{system}-{arch}"
    app = directory / "Sakamoto.app"
    binary = app / "Contents/MacOS/sakamoto-menubar" if system == "darwin" else directory / "Sakamoto/bin" / ("sakamoto-desktop.exe" if system == "windows" else "sakamoto-desktop")
    if not binary.is_file():
        raise SystemExit("Build this platform first: mise run gui:build")
    if args.verify:
        forwarded += ["--verify", str(args.verify.resolve())]
        subprocess.run([str(binary), *forwarded], check=True)
    elif system == "darwin":
        subprocess.run(["open", "-n", str(app), "--args", "--show", *forwarded], check=True)
    else:
        subprocess.Popen([str(binary), "--show", *forwarded], cwd=root)


if __name__ == "__main__":
    main()
