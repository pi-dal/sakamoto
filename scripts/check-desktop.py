#!/usr/bin/env python3
"""Run the isolated desktop service/API and shared-view workflows."""
import argparse
import os
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--race", action="store_true", help="Requires a supported native C compiler")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    artifacts = root / "dist/menubar-verification"
    artifacts.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, SAKAMOTO_MENUBAR_ARTIFACT_DIR=str(artifacts))
    flags = ["-count=1", "-timeout=30s"]
    if args.race:
        flags += ["-race"]
    for directory, packages in ((root, ["./internal/desktop"]), (root / "desktop", ["./..."])):
        subprocess.run(["go", "test", *packages, *flags], cwd=directory, env=env, check=True)
        subprocess.run(["go", "vet", *packages], cwd=directory, env=env, check=True)
    subprocess.run(["go", "run", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0", "run", "./..."], cwd=root / "desktop", env=env, check=True)


if __name__ == "__main__":
    main()
