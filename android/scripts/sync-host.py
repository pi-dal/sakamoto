#!/usr/bin/env python3
"""Copy a validated host source snapshot into the debug app's private storage.
Run with mise -E android exec -- python3 android/scripts/sync-host.py SERIAL.
Requires the existing PyYAML and sing-box host tools; never writes secrets to stdout.
"""
import argparse
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tarfile
import tempfile
from datetime import datetime, timezone
import yaml
import zipfile

parser = argparse.ArgumentParser()
parser.add_argument('serial', nargs='?', help='Debug device serial for USB transfer')
parser.add_argument('--export', type=Path, help='Write a private ZIP for Android Config → Import tunnel package')
parser.add_argument('--export-ios', type=Path, help='Write a complete private .sakamoto configuration for iOS')
args = parser.parse_args()
if sum(bool(value) for value in (args.serial, args.export, args.export_ios)) != 1:
    parser.error('Use a device serial, --export ZIP_PATH, or --export-ios PACKAGE_PATH')
root = Path.home() / '.config/sakamoto'
serial = args.serial
pkg = 'com.pidal.sakamoto'
adb = ['adb', '-s', serial]
phone = f'/data/user/0/{pkg}/files'
settings = yaml.safe_load((root / 'sakamoto.yaml').read_text())
config = json.loads((root / 'config.json').read_text())
source = Path(settings['conf']).resolve()
node_file = Path(settings.get('nodes_file') or root / 'nodes.txt').resolve()
files = {}

def collect(path):
    path = path.resolve()
    if not path.is_relative_to(root) or not path.is_file():
        raise RuntimeError('Source outside host runtime directory or missing')
    dest = 'imports/local/' + str(path.relative_to(root))
    if dest in files:
        return
    files[dest] = path.read_bytes()
    if path.suffix == '.conf':
        for line in path.read_text().splitlines():
            match = re.match(r'\s*include\s*=\s*(.*?)\s*$', line, re.I)
            if match:
                for name in match.group(1).split(','):
                    name = name.strip()
                    if name and '://' not in name:
                        collect(path.parent / name)

for path in sorted(root.glob('*.conf')) + sorted((root / 'sources/current').rglob('*.conf')):
    collect(path)
collect(source)
collect(node_file)
links = [line.strip() for line in node_file.read_text().splitlines() if line.strip() and not line.lstrip().startswith('#')]
state_result = subprocess.run(adb + ['exec-out', 'run-as', pkg, 'cat', 'files/sakamoto-config.json'], capture_output=True) if serial else subprocess.CompletedProcess([], 1, b'')
try:
    previous = json.loads(state_result.stdout) if state_result.returncode == 0 and state_result.stdout.strip() else {}
except json.JSONDecodeError:
    previous = {}
# Refuse to overwrite device-local edits of runtime policy/exit/defaults.
prior_snapshot = subprocess.run(adb + ['exec-out', 'run-as', pkg, 'cat', 'files/imports/local/config.json'], capture_output=True) if serial else subprocess.CompletedProcess([], 1, b'')
if prior_snapshot.returncode == 0 and prior_snapshot.stdout.strip() and previous.get('generatedContent'):
    if json.loads(previous.get('generatedContent') or '{}') != json.loads(prior_snapshot.stdout):
        raise RuntimeError('Device tunnel configuration was edited; reconcile before replacing it with a host snapshot')
# Preserve device edits: this command never resolves divergent source state.
for field, expected in [('nodes', links), ('policy', settings.get('policy') or []), ('subscriptions', settings.get('subscriptions') or [])]:
    if previous.get(field) and previous[field] != expected:
        raise RuntimeError(f'Device {field} differs; reconcile before replacing the host snapshot')
for name, data in files.items():
    if not previous:
        break
    existing = subprocess.run(adb + ['exec-out', 'run-as', pkg, 'cat', 'files/' + name], capture_output=True)
    if existing.returncode == 0 and existing.stdout != data:
        raise RuntimeError('Device source differs from host; reconcile before replacing snapshot')
for rule in config.get('route', {}).get('rule_set', []):
    if rule.get('path'):
        path = Path(rule['path'])
        if not path.is_absolute():
            path = root / path
        dest = 'rules/' + path.name
        data = path.read_bytes()
        if dest in files and files[dest] != data:
            raise RuntimeError('Rule set filename collision')
        files[dest] = data
        rule['path'] = phone + '/' + dest
# Host loopback DNS takeover and interface names cannot travel to Android.
config['inbounds'] = [item for item in config.get('inbounds', []) if item.get('tag') != 'protected-dns']
config['route']['rules'] = [rule for rule in config['route']['rules'] if 'protected-dns' not in rule.get('inbound', [])]
for inbound in config['inbounds']:
    if inbound['type'] == 'tun':
        inbound.pop('interface_name', None)
old_config = json.loads(previous.get('generatedContent') or '{}') if previous.get('generatedContent') else {}
old_secrets = {item.get('tag', item['type']): item.get('secret') for item in old_config.get('services', []) if item['type'] == 'api'}
for service in config.get('services', []):
    if service['type'] == 'api':
        service['secret'] = old_secrets.get(service.get('tag', service['type'])) or secrets.token_hex(32)
        service['listen'] = '127.0.0.1'
# Keep host-owned policy/chain intent distinct from device runtime state.
# Never include the host API secret, cloud settings or unrelated YAML fields.
metadata = {key: settings[key] for key in (
    'chain_enabled', 'socks_exit', 'fallbacks', 'fallback_enabled', 'recover_after',
    'block_quic', 'block_stun', 'experiment', 'strict_route', 'tun_stack'
) if key in settings}
files['imports/local/policy.json'] = json.dumps(settings.get('policy') or [], indent=2).encode()
files['imports/local/chain.json'] = json.dumps(metadata, indent=2).encode()
state = dict(previous, sourceNeedsGenerate=False, hostMetadata=json.dumps(metadata, separators=(',', ':')), generatedContent=json.dumps(config, separators=(',', ':')), sourceConfName=source.name,
             sourceConfPath=str(source.relative_to(root)), sourceConfIsUrl=False, sourceConfContent=source.read_text(),
             nodes=links, policy=settings.get('policy') or [], subscriptions=settings.get('subscriptions') or [],
             hostSnapshotAt=datetime.now(timezone.utc).isoformat(timespec='seconds'))
files['sakamoto-config.json'] = json.dumps(state, separators=(',', ':')).encode()
files['imports/local/config.json'] = json.dumps(config, indent=2).encode()
with tempfile.TemporaryDirectory(prefix='sakamoto-sync-check-') as tmp:
    local = copy.deepcopy(config)
    for rule in local['route'].get('rule_set', []):
        if rule.get('path'):
            dest = rule['path'].removeprefix(phone + '/')
            path = Path(tmp) / dest
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(files[dest]); path.chmod(0o600)
            rule['path'] = str(path)
    path = Path(tmp) / 'config.json'
    path.write_text(json.dumps(local)); path.chmod(0o600)
    result = subprocess.run(['sing-box', 'check', '-D', tmp, '-c', str(path)], capture_output=True)
    if result.returncode:
        raise RuntimeError('Adapted config failed sing-box validation; nothing transferred')
# iOS profiles contain the exact runtime config and every local rule file.
# Only the owner-selected destination receives this private snapshot; source
# sync stays source-only and never silently uploads runtime/API credentials.
if args.export_ios:
    import base64
    sources_hash = hashlib.sha256()
    for name in sorted(files):
        if name.startswith('imports/local/') and name != 'imports/local/config.json':
            sources_hash.update(name.encode() + b'\0' + files[name])
    source_files = {}
    for name, data in files.items():
        if name.startswith('imports/local/') and name.endswith('.conf'):
            source_files[name.removeprefix('imports/local/')] = data.decode('utf-8')
    main_conf = str(source.relative_to(root))
    source_files['nodes.txt'] = node_file.read_text()
    source_files['policy.json'] = json.dumps(settings.get('policy') or [])
    source_files['subscriptions.json'] = json.dumps(settings.get('subscriptions') or [])
    source_bundle = dict(version=1, main_conf=main_conf, files=source_files)
    package = dict(format='sakamoto-tunnel-v1', name=source.stem,
                   sourceBundleJSON=json.dumps(source_bundle, separators=(',', ':')),
                   hostMetadataJSON=json.dumps(metadata, separators=(',', ':')),
                   config=json.dumps(config, separators=(',', ':')),
                   files={name: base64.b64encode(data).decode() for name, data in files.items() if name.startswith('rules/')},
                   sourceDigest=sources_hash.hexdigest())
    args.export_ios.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.export_ios, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(package, output, separators=(',', ':'))
    print('Validated complete private iOS configuration exported; import the .sakamoto file in Config')
    sys.exit(0)
# Portable official-client path: never publish this user-specific package.
if args.export:
    args.export.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.export, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'wb') as output:
        with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
            for name, data in files.items():
                archive.writestr(name, data)
    print('Validated private tunnel package exported; import it from Android Config')
    sys.exit(0)
# Keep a private rollback copy and transfer in one archive, never through /sdcard.
if previous:
    subprocess.run(adb + ['shell', 'run-as', pkg, 'sh', '-c', '"umask 077; cp files/sakamoto-config.json files/host-sync-backup.json"'], check=True, capture_output=True)
stream = io.BytesIO()
with tarfile.open(fileobj=stream, mode='w') as archive:
    for name, data in files.items():
        item = tarfile.TarInfo(name); item.size = len(data); item.mode = 0o600
        archive.addfile(item, io.BytesIO(data))
subprocess.run(adb + ['shell', 'run-as', pkg, 'sh', '-c', '"umask 077; cd files; tar xf -"'], input=stream.getvalue(), check=True, capture_output=True)
for name, data in files.items():
    actual = subprocess.check_output(adb + ['exec-out', 'run-as', pkg, 'cat', 'files/' + name])
    if hashlib.sha256(actual).digest() != hashlib.sha256(data).digest():
        raise RuntimeError('Transferred file hash differs')
print(f'PASS: {len(links)} nodes, {sum(name.endswith(".conf") for name in files)} conf files, {sum(name.endswith(".srs") for name in files)} rule sets; every file SHA-256 verified')
print(f'User policy: {len(state["policy"])} overrides; route policy: {len(config["route"]["rules"])} rules; chain metadata and exit outbound preserved')
print('Host snapshot updated. Reopen Config to view sources; reload/reconnect to apply runtime changes.')
