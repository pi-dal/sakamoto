#!/usr/bin/env python3
"""Build a reproducibly identified, officially signed APK with private credentials.
CI supplies SAKAMOTO_ANDROID_* variables. Local builds read signing.json from
~/.local/share/sakamoto/android-signing, never from the source tree.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import xml.etree.ElementTree as ET

parser = argparse.ArgumentParser()
parser.add_argument("--init-script", help="Optional existing Gradle repository configuration")
parser.add_argument("--offline", action="store_true")
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
env = os.environ.copy()
credentials = Path.home() / ".local/share/sakamoto/android-signing/signing.json"
if not env.get("SAKAMOTO_ANDROID_KEYSTORE"):
    if not credentials.is_file():
        raise SystemExit("Release signing credentials are required")
    data = json.loads(credentials.read_text())
    env.update({
        "SAKAMOTO_ANDROID_KEYSTORE": data["keystore"],
        "SAKAMOTO_ANDROID_STORE_PASSWORD": data["store_password"],
        "SAKAMOTO_ANDROID_KEY_ALIAS": data["alias"],
        "SAKAMOTO_ANDROID_KEY_PASSWORD": data["key_password"],
    })
for key in ("SAKAMOTO_ANDROID_KEYSTORE", "SAKAMOTO_ANDROID_STORE_PASSWORD", "SAKAMOTO_ANDROID_KEY_ALIAS", "SAKAMOTO_ANDROID_KEY_PASSWORD"):
    if not env.get(key):
        raise SystemExit("Incomplete release signing configuration")
command = ["./gradlew", ":app:testReleaseUnitTest", ":app:assembleRelease", "--no-daemon", "--max-workers=2"]
if args.offline:
    command.append("--offline")
if args.init_script:
    command.extend(["--init-script", args.init_script])
subprocess.run(command, cwd=root / "android", env=env, check=True)
apk = root / "android/app/build/outputs/apk/release/app-release.apk"
metadata = json.loads(apk.with_name("output-metadata.json").read_text())
element = metadata["elements"][0]
version = element["versionName"]
sdk = Path(env["ANDROID_HOME"])
tools = sdk / "build-tools/35.0.0"
subprocess.run([str(tools / "apksigner"), "verify", "--verbose", "--print-certs", str(apk)], check=True)
manifest = root / "android/app/build/intermediates/merged_manifests/release/processReleaseManifest/AndroidManifest.xml"
xml = ET.parse(manifest).getroot()
application = xml.find("application")
if application is None or application.get("{http://schemas.android.com/apk/res/android}debuggable") == "true":
    raise SystemExit("Release manifest must not be debuggable")
report = root / "dist/android"
report.mkdir(parents=True, exist_ok=True)
name = f"sakamoto-android-v{version}.apk"
output = report / name
output.write_bytes(apk.read_bytes())
(report / "SHA256SUMS").write_text(hashlib.sha256(output.read_bytes()).hexdigest() + "  " + name + "\n")
(report / "release-metadata.json").write_text(json.dumps({
    "versionName": version, "versionCode": element["versionCode"], "applicationId": metadata["applicationId"],
    "gitCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
    "debuggable": False,
}, indent=2) + "\n")
print(f"Verified signed release: {output}")
