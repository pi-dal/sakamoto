#!/usr/bin/env python3
"""Copy a validated host source snapshot into the debug app's private storage.
Run with mise -E android exec -- python3 android/scripts/sync-host.py SERIAL.
Requires PyYAML, the project Go toolchain and sing-box.
"""
import argparse
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import shlex
import tempfile
from datetime import datetime, timezone
import yaml
import zipfile
import uuid

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
settings_bytes = (root / 'sakamoto.yaml').read_bytes()
settings = yaml.safe_load(settings_bytes)
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
        section = ''
        for line in files[dest].decode('utf-8-sig').splitlines():
            line = line.strip()
            if line.startswith('[') and line.endswith(']'):
                section = line[1:-1].lower()
                continue
            if section != 'general':
                continue
            key, separator, value = line.partition('=')
            if separator and key.strip() == 'include':
                for name in value.split(','):
                    name = name.strip()
                    if name and '://' not in name:
                        if Path(name).is_absolute() or not (path.parent / name).resolve().is_relative_to(root):
                            raise RuntimeError('Mobile includes must be relative to the source tree')
                        collect(path.parent / name)

collect(source)
collect(node_file)
source_key = 'imports/local/' + str(source.relative_to(root))
node_key = 'imports/local/' + str(node_file.relative_to(root))
source_text = files[source_key].decode('utf-8')
node_text = files[node_key].decode('utf-8')
links = [line.strip() for line in node_text.splitlines() if line.strip() and not line.lstrip().startswith('#')]
state_result = subprocess.run(adb + ['exec-out', 'run-as', pkg, 'cat', 'files/sakamoto-config.json'], capture_output=True) if serial else subprocess.CompletedProcess([], 1, b'')
try:
    previous = json.loads(state_result.stdout) if state_result.returncode == 0 and state_result.stdout.strip() else {}
except json.JSONDecodeError as error:
    raise RuntimeError('Device configuration is unreadable') from error
if previous.get('sourceNeedsGenerate') or previous.get('sourceConfIsUrl'):
    raise RuntimeError('Device has pending source edits or an HTTPS source. Reconcile these in Config before USB sync.')
revision = previous.pop('packageRevision', '')
if revision and str(uuid.UUID(revision)) != revision:
    raise RuntimeError('Invalid device package revision')
previous_root = f'files/package-revisions/{revision}/' if revision else 'files/'
# Refuse to overwrite device-local edits of runtime policy/exit/defaults.
prior_snapshot = subprocess.run(adb + ['exec-out', 'run-as', pkg, 'cat', previous_root + 'imports/local/config.json'], capture_output=True) if serial else subprocess.CompletedProcess([], 1, b'')
if prior_snapshot.returncode == 0 and prior_snapshot.stdout.strip() and previous.get('generatedContent'):
    if json.loads(previous.get('generatedContent') or '{}') != json.loads(prior_snapshot.stdout):
        raise RuntimeError('Device tunnel configuration was edited; reconcile before replacing it with a host snapshot')
# Preserve device edits: this command never resolves divergent source state.
for field, expected in [('nodes', links), ('policy', settings.get('policy') or []), ('subscriptions', settings.get('subscriptions') or [])]:
    if field in previous and previous[field] != expected:
        raise RuntimeError(f'Device {field} differs; reconcile before replacing the host snapshot')
for name, data in files.items():
    if not previous:
        break
    existing = subprocess.run(adb + ['exec-out', 'run-as', pkg, 'cat', shlex.quote(previous_root + name)], capture_output=True)
    if existing.returncode == 0 and existing.stdout != data:
        raise RuntimeError('Device source differs from host; reconcile before replacing snapshot')
# Compile the bytes used in the package before handing anything to a device.
with tempfile.TemporaryDirectory(prefix='sakamoto-export-') as tmp:
    work = Path(tmp)
    for name, data in files.items():
        target = work / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    sidecar = work / 'sakamoto.yaml'
    sidecar.write_bytes(settings_bytes)
    legacy = work / 'sr.json'
    if settings.get('allow_hosts'):
        legacy.write_bytes(Path(settings.get('srjson') or Path.home() / 'Documents/Shadowrocket.json').read_bytes())
    # Config.Load migrates older sidecars using just the previous final tag.
    if 'experiment' not in settings and (root / 'config.json').is_file():
        prior_final = json.loads((root / 'config.json').read_bytes()).get('route', {}).get('final')
        (work / 'config.json').write_text(json.dumps({'route': {'final': prior_final}}))
    repo = Path(__file__).resolve().parents[2]
    result = subprocess.run(['go', 'run', './tools/main.go', '-mobile-export', sidecar, work / source_key,
                             work / node_key, legacy], cwd=repo, capture_output=True)
    if result.returncode:
        raise RuntimeError('Could not generate and validate the selected mobile sources')
    config = json.loads((work / 'config.json').read_bytes())
    metadata = json.loads((work / 'mobile-policy.json').read_bytes())
    host_generation = json.loads((work / 'mobile-generation.json').read_bytes()) if args.export_ios else None
    group_source = (work / 'groups.json').read_text()
    guard_file = work / 'mobile-dns-guard.json'
    dns_guard = json.loads(guard_file.read_bytes()) if guard_file.exists() else None
    for rule in config['route'].get('rule_set', []):
        if rule.get('path'):
            path = (work / rule['path']).resolve()
            if not path.is_relative_to(work / 'rules'):
                raise RuntimeError('Generated rule file outside the private build directory')
            dest = str(path.relative_to(work))
            files[dest] = path.read_bytes()
            rule['path'] = phone + '/' + dest
# Providers choose their own TUN interface name.
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
files['imports/local/policy.json'] = json.dumps(settings.get('policy') or [], indent=2).encode()
files['imports/local/chain.json'] = json.dumps(metadata, indent=2).encode()
regional = settings.get('regional_dns') or {}
if regional.get('domains'):
    regional = {k: regional[k] for k in ('domains', 'resolver_ip', 'tls_name', 'relay', 'relay_fingerprint')}
else:
    regional = {}
local_regional = previous.get('regionalDNS') or {}
origin = previous.get('regionalDNSOrigin', 'legacy') if local_regional else 'package'
if origin == 'device':
    if regional and regional != local_regional:
        raise RuntimeError('Device regional DNS differs from host; reconcile before replacing snapshot')
    regional = local_regional
elif origin != 'package' and regional != local_regional:
    raise RuntimeError('Open Regional DNS and save or clear the existing settings before syncing a different policy')
if regional and (args.export or serial):
    # A required root entry makes older Android importers reject the package
    # before they can replace the incoming regional policy with device defaults.
    files['regional-dns.json'] = json.dumps(regional).encode()
# Build a fresh profile. Profile-specific overrides from the installed manifest belong to that revision.
state = dict(routeSourceVersion=1, modeCacheID=str(uuid.uuid4()), scopedExperiment=True, sourceGroupExclusions=json.loads(group_source), regionalDNS=regional, regionalDNSOrigin=origin, sourceNeedsGenerate=False, hostMetadata=json.dumps(metadata, separators=(',', ':')), generatedContent=json.dumps(config, separators=(',', ':')), sourceConfName=source.name,
             sourceConfPath=str(source.relative_to(root)), sourceConfIsUrl=False, sourceConfContent=source_text,
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
    profile = Path(tmp) / 'regional.json'
    profile.write_text(json.dumps(regional)); profile.chmod(0o600)
    result = subprocess.run(['go', 'run', './tools/main.go', '-mobile-check', path, profile], cwd=repo, capture_output=True)
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
    source_files['groups.json'] = group_source
    source_files['nodes.txt'] = node_text
    source_files['policy.json'] = json.dumps(settings.get('policy') or [])
    source_files['subscriptions.json'] = json.dumps(settings.get('subscriptions') or [])
    source_bundle = dict(version=1, main_conf=main_conf, files=source_files)
    bundle_json = json.dumps(source_bundle, separators=(',', ':'))
    validation = subprocess.run(['go', 'run', './tools/main.go', '-ios-sources'], cwd=repo,
                                input=bundle_json.encode(), capture_output=True)
    if validation.returncode:
        raise RuntimeError('Invalid iOS source snapshot; check relative .conf includes and source limits before exporting')
    package = dict(routeSourceVersion=1, hostGeneration=host_generation, format='sakamoto-tunnel-v3' if host_generation['nodes'] or host_generation['fingerprint'] else 'sakamoto-tunnel-v2' if dns_guard or regional else 'sakamoto-tunnel-v1', name=source.stem,
                   dnsGuard=dns_guard, regionalDNS=regional or None,
                   sourceFinal=metadata['sourceFinal'], sourceBundleJSON=bundle_json,
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
# Stage one immutable archive privately. Only the app's transactional importer
# can publish a new manifest, after checking the transfer and the current base.
transfer = str(uuid.uuid4())
remote = f'cache/host-sync-{transfer}.zip'
base = hashlib.sha256(state_result.stdout).hexdigest() if state_result.returncode == 0 else 'absent'
stream = io.BytesIO()
with zipfile.ZipFile(stream, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
    for name, data in files.items():
        archive.writestr(name, data)
payload = stream.getvalue()
checksum = hashlib.sha256(payload).hexdigest()
try:
    subprocess.run(adb + ['shell', f"run-as {pkg} sh -c 'umask 077; cat > {remote}'"],
                   input=payload, check=True, capture_output=True)
    result = subprocess.run(adb + ['shell', 'content', 'call', '--uri', f'content://{pkg}.hostsync',
                            '--method', 'install', '--arg', transfer, '--extra', 'sha256:s:' + checksum,
                            '--extra', 'base:s:' + base], capture_output=True, text=True, check=True)
    if f'installed={transfer}' not in result.stdout:
        raise RuntimeError('USB import was not confirmed. Check the active configuration and the debug app version.')
finally:
    subprocess.run(adb + ['shell', 'run-as', pkg, 'rm', '-f', remote], capture_output=True)
print('PASS: USB archive verified and installed. Reconnect to apply the imported configuration.')
