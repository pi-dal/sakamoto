#!/usr/bin/env python3
"""Prepare or remove an ephemeral macOS CI signing keychain without logging secrets."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import subprocess
import urllib.request


def run(command):
    result = subprocess.run(command, capture_output=True, text=True)
    if result.returncode:
        raise SystemExit(f"{command[0]} {command[1]} failed (exit {result.returncode})")
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "cleanup"))
    args = parser.parse_args()
    root = Path(os.environ["RUNNER_TEMP"]) / "sakamoto-signing"
    keychain = root / "release.keychain-db"
    search_list = root / "original-search-list.json"
    if args.action == "cleanup":
        try:
            if search_list.exists():
                run(["security", "list-keychains", "-d", "user", "-s", *json.loads(search_list.read_text())])
            if keychain.exists():
                run(["security", "delete-keychain", str(keychain)])
        finally:
            for name in ("certificate.p12", "AuthKey.p8", "original-search-list.json"):
                (root / name).unlink(missing_ok=True)
        return
    for name in ("MACOS_CERTIFICATE_P12_BASE64", "MACOS_CERTIFICATE_PASSWORD", "MACOS_TEAM_ID", "ASC_PRIVATE_KEY", "ASC_KEY_ID", "ASC_ISSUER_ID"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing release credential: {name}")
    os.umask(0o077)
    root.mkdir(mode=0o700)
    original = shlex.split(run(["security", "list-keychains", "-d", "user"]))
    search_list.write_text(json.dumps(original))
    certificate = root / "certificate.p12"
    certificate.write_bytes(base64.b64decode(os.environ["MACOS_CERTIFICATE_P12_BASE64"], validate=True))
    private_key = root / "AuthKey.p8"
    private_key.write_text(os.environ["ASC_PRIVATE_KEY"])
    unlock_token = secrets.token_urlsafe(32)
    run(["security", "create-keychain", "-p", unlock_token, str(keychain)])
    run(["security", "set-keychain-settings", "-lut", "21600", str(keychain)])
    run(["security", "unlock-keychain", "-p", unlock_token, str(keychain)])
    # Import Apple's public intermediates without changing system trust settings.
    # Fresh runners may not have cached the Developer ID certificate chain.
    for name in ("DeveloperIDCA.cer", "DeveloperIDG2CA.cer"):
        intermediate = root / name
        with urllib.request.urlopen(f"https://www.apple.com/certificateauthority/{name}", timeout=30) as response:
            intermediate.write_bytes(response.read())
        run(["security", "import", str(intermediate), "-k", str(keychain)])
    run(["security", "import", str(certificate), "-k", str(keychain), "-P", os.environ["MACOS_CERTIFICATE_PASSWORD"], "-T", "/usr/bin/codesign", "-T", "/usr/bin/security"])
    run(["security", "list-keychains", "-d", "user", "-s", str(keychain), *original])
    run(["security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", unlock_token, str(keychain)])
    identities = run(["security", "find-identity", "-v", "-p", "codesigning", str(keychain)])
    matches = re.findall(r'([A-F0-9]{40}) "Developer ID Application: [^\n"]+ \(' + re.escape(os.environ["MACOS_TEAM_ID"]) + r'\)"', identities)
    if len(matches) != 1:
        raise SystemExit("Expected exactly one valid Developer ID Application identity for MACOS_TEAM_ID")
    with Path(os.environ["GITHUB_ENV"]).open("a") as output:
        output.write(f"MACOS_SIGN_IDENTITY={matches[0]}\nASC_PRIVATE_KEY_PATH={private_key}\n")
    certificate.unlink()
    print("Prepared ephemeral Developer ID signing keychain")


if __name__ == "__main__":
    main()
