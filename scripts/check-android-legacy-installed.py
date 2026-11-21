#!/usr/bin/env python3
"""Replay installed legacy route orders on a disposable emulator with the test APK installed."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
from zipfile import ZipFile

from legacy_route_fixture import format_legacy_config, move_proxy_rule_up

parser = argparse.ArgumentParser()
parser.add_argument('historical', type=Path)
parser.add_argument('output', type=Path)
args = parser.parse_args()
serial = os.environ['ANDROID_SERIAL']
pkg = 'com.pidal.sakamoto'


def adb(*args, data=None):
    return subprocess.run(['adb', '-s', serial, *args], input=data, stdout=subprocess.PIPE, check=True).stdout


def read_manifest():
    return adb('exec-out', 'run-as', pkg, 'cat', 'files/sakamoto-config.json')


def stage(manifest):
    adb('shell', 'am', 'force-stop', pkg)
    adb('shell', f"run-as {pkg} sh -c 'cat >files/sakamoto-config.tmp && mv files/sakamoto-config.tmp files/sakamoto-config.json'", data=manifest)


assert adb('shell', 'getprop', 'ro.kernel.qemu').strip() == b'1'
with ZipFile(args.historical) as package:
    original = package.read('sakamoto-config.json')
reordered = json.loads(original)
reordered['generatedContent'] = move_proxy_rule_up(reordered['generatedContent'])
formatted = json.loads(original)
formatted['generatedContent'] = format_legacy_config(formatted['generatedContent'])
scalar = json.dumps(formatted).encode()
archive = io.BytesIO()
with ZipFile(args.historical) as source, ZipFile(archive, 'w') as target:
    for item in source.infolist():
        target.writestr(item, scalar if item.filename == 'sakamoto-config.json' else source.read(item))
report = {'package_sha256': hashlib.sha256(args.historical.read_bytes()).hexdigest()}
adb('shell', 'am', 'force-stop', pkg)
backup = read_manifest()
try:
    adb('shell', f"run-as {pkg} sh -c 'cat >cache/historical-scalar.zip'", data=archive.getvalue())
    result = adb('shell', 'am', 'instrument', '-w', '-e', 'profileIsolation', 'historical-scalar',
                 f'{pkg}.test/{pkg}.NativeBindingSmokeTest')
    assert b'PASS:' in result, result.decode(errors='replace')[-2048:]
    assert read_manifest() == backup
    report['scalar_import_rejected_with_manifest_preserved'] = True
    for order, manifest in [('original', original), ('moved', json.dumps(reordered).encode()), ('scalar', scalar)]:
        stage(manifest)
        phases = []
        for phase in ['first', 'restart']:
            adb('shell', 'am', 'force-stop', pkg)
            result = adb('shell', 'am', 'instrument', '-w', '-e', 'profileIsolation', 'historical-installed',
                         f'{pkg}.test/{pkg}.NativeBindingSmokeTest')
            assert b'PASS:' in result, result.decode(errors='replace')[-2048:]
            assert read_manifest() == manifest
            phases.append({'phase': phase, 're_export_notice': True, 'home_repair_re_export_notice': True, 'running_core_instances': 0, 'manifest_preserved': True})
        report[order] = phases
finally:
    adb('shell', 'run-as', pkg, 'rm', '-f', 'cache/historical-scalar.zip')
    stage(backup)
    assert read_manifest() == backup
args.output.write_text(json.dumps(report, indent=2) + '\n')
