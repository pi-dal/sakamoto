#!/usr/bin/env python3
"""Build signed universal and ABI-specific APKs using private credentials."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import xml.etree.ElementTree as ET
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument("--init-script", help="Optional existing Gradle repository configuration")
parser.add_argument("--offline", action="store_true")
parser.add_argument("--instrumentation", action="store_true", help="Also build an officially signed release smoke-test APK")
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
command = ["./gradlew", ":app:testReleaseUnitTest", ":app:lintRelease", ":app:assembleRelease", "-PsakamotoAbiSplits=true", "--no-daemon", "--max-workers=2"]
if args.instrumentation:
    command.extend([":app:assembleReleaseAndroidTest", "-PsakamotoTestBuildType=release"])
if args.offline:
    command.append("--offline")
if args.init_script:
    command.extend(["--init-script", args.init_script])
subprocess.run(command, cwd=root / "android", env=env, check=True)
apk_dir = root / "android/app/build/outputs/apk/release"
metadata = json.loads((apk_dir / "output-metadata.json").read_text())
elements = metadata["elements"]
expected_abis = {"arm64-v8a", "armeabi-v7a", "x86_64", "x86"}
variants = {}
for element in elements:
    filters = element["filters"]
    if len(filters) > 1 or any(f["filterType"] != "ABI" for f in filters):
        raise SystemExit("Unexpected APK output filter")
    abi = filters[0]["value"] if filters else "universal"
    if abi in variants:
        raise SystemExit("Duplicate APK variant: " + abi)
    variants[abi] = element
if set(variants) != expected_abis | {"universal"}:
    raise SystemExit("Release must contain four ABI installers and a universal APK")
version = variants["universal"]["versionName"]
version_code = variants["universal"]["versionCode"]
if any(e["versionName"] != version or e["versionCode"] != version_code for e in elements):
    raise SystemExit("APK variants have inconsistent versions")
tools = Path(env["ANDROID_HOME"]) / "build-tools/35.0.0"
manifests = root / "android/app/build/intermediates/merged_manifests/release/processReleaseManifest"
report = root / "dist/android"
report.mkdir(parents=True, exist_ok=True)
artifacts = []
certificates = set()
for abi, element in sorted(variants.items()):
    xml = ET.parse(manifests / abi / "AndroidManifest.xml").getroot()
    application = xml.find("application")
    if application is None or application.get("{http://schemas.android.com/apk/res/android}debuggable") == "true":
        raise SystemExit("Release manifest must not be debuggable: " + abi)
    apk = apk_dir / element["outputFile"]
    signed = subprocess.check_output([str(tools / "apksigner"), "verify", "--verbose", "--print-certs", str(apk)], text=True)
    certificate = [line for line in signed.splitlines() if "certificate SHA-256 digest:" in line]
    if len(certificate) != 1:
        raise SystemExit("Expected exactly one release signer")
    certificates.add(certificate[0].split(": ", 1)[1])
    subprocess.run([str(tools / "zipalign"), "-c", "-P", "16", "4", str(apk)], check=True)
    with zipfile.ZipFile(apk) as archive:
        libraries = {name.split("/")[1] for name in archive.namelist() if name.startswith("lib/") and name.endswith("/libbox.so")}
    if libraries != (expected_abis if abi == "universal" else {abi}):
        raise SystemExit("Unexpected native architectures in " + apk.name)
    suffix = "" if abi == "universal" else "-" + abi
    name = f"sakamoto-android-v{version}{suffix}.apk"
    output = report / name
    output.write_bytes(apk.read_bytes())
    artifacts.append({"file": name, "abi": abi, "bytes": output.stat().st_size, "sha256": hashlib.sha256(output.read_bytes()).hexdigest()})
    print(f"Verified signed release ({abi}): {output}")
if len(certificates) != 1:
    raise SystemExit("APK variants have different signing certificates")
(report / "SHA256SUMS").write_text("".join(f"{a['sha256']}  {a['file']}\n" for a in artifacts))
(report / "release-metadata.json").write_text(json.dumps({
    "versionName": version, "versionCode": version_code, "applicationId": metadata["applicationId"],
    "gitCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
    "dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip()),
    "debuggable": False, "certificateSHA256": certificates.pop(), "artifacts": artifacts,
}, indent=2) + "\n")
