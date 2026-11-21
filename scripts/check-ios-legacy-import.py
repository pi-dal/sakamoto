#!/usr/bin/env python3
"""Replay a historical desktop package against a safe control on an isolated simulator."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from legacy_route_fixture import format_legacy_config, move_proxy_rule_up

parser = argparse.ArgumentParser()
parser.add_argument('historical', type=Path)
parser.add_argument('control', type=Path)
parser.add_argument('output', type=Path)
parser.add_argument('--move-proxy-up', action='store_true')
parser.add_argument('--format', action='store_true')
args = parser.parse_args()
sim = os.environ['SAKAMOTO_TEST_SIMULATOR']
args.output.mkdir(parents=True, exist_ok=True)


def documents():
    data = subprocess.check_output(['xcrun', 'simctl', 'get_app_container', sim, 'com.pidal.sakamoto', 'data'], text=True)
    return Path(data.strip()) / 'Documents'


old = args.historical.read_bytes()
learned = 'host-learned.fixture.example'
assert learned in json.loads(old)['config']
docs = documents()
installed = old
if args.move_proxy_up or args.format:
    package = json.loads(old)
    if args.move_proxy_up:
        package['config'] = move_proxy_rule_up(package['config'])
    if args.format:
        package['config'] = format_legacy_config(package['config'])
    installed = json.dumps(package).encode()
(docs / 'historical-learned.sakamoto').write_bytes(installed)
shutil.copyfile(args.control, docs / 'legacy-control.sakamoto')
for marker in ('legacy-upgrade-first.ok', 'legacy-upgrade-restart.ok', 'legacy-upgrade-manifest', 'legacy-safe-manifest', 'legacy-runtime-first.json', 'legacy-runtime-restart.json'):
    (docs / marker).unlink(missing_ok=True)
try:
    subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests',
                    '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery',
                    '-parallel-testing-enabled', 'NO', '-resultBundlePath', str(args.output / 'legacy-import.xcresult'),
                    'CODE_SIGNING_ALLOWED=YES', 'CODE_SIGN_IDENTITY=-',
                    '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testHistoricalLearnedPackageImport',
                    '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', 'test'], check=True)
    report = {'historical_package_sha256': hashlib.sha256(old).hexdigest(),
              'formatted': args.format, 'moved_rs_proxy_up': args.move_proxy_up, 'tested_package_sha256': hashlib.sha256(installed).hexdigest()}
    for phase in ('first', 'restart'):
        runtime = (documents() / f'legacy-runtime-{phase}.json').read_text()
        assert learned not in runtime
        route = json.loads(runtime)['route']
        explicit = {}
        for asset in route['rule_set']:
            if 'path' not in asset:
                continue
            with tempfile.TemporaryDirectory() as tmp:
                output = Path(tmp) / 'rules.json'
                subprocess.run(['sing-box', 'rule-set', 'decompile', '-o', str(output), asset['path']], check=True)
                rules = output.read_text()
            assert learned not in rules
            for action in ('direct', 'proxy'):
                if f'explicit-{action}.fixture.example' in rules:
                    explicit[action] = next(rule['outbound'] for rule in route['rules'] if asset['tag'] in rule.get('rule_set', []))
        assert explicit == {'direct': 'direct', 'proxy': 'MainProxy'}
        assert (documents() / f'legacy-upgrade-{phase}.ok').read_text() == 'verified'
        report[phase] = {'installed_legacy_blocked': True, 'confirmed_and_cold_start_blocked': True,
                         'installed_manifest_sources_and_rules_preserved': True, 'working_manifest_preserved': True, 'learned_domain_present': False, 'explicit_routes': explicit}
    (args.output / 'ios-legacy-import.json').write_text(json.dumps(report, indent=2) + '\n')
finally:
    for marker in ('legacy-upgrade-first.ok', 'legacy-upgrade-restart.ok', 'legacy-upgrade-manifest', 'historical-learned.sakamoto', 'legacy-control.sakamoto', 'legacy-safe-manifest', 'legacy-runtime-first.json', 'legacy-runtime-restart.json'):
        (documents() / marker).unlink(missing_ok=True)
