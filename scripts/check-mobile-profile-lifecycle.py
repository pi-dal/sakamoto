#!/usr/bin/env python3
"""Build synthetic host exports for the mobile profile lifecycle scenarios."""
import argparse
import hashlib
import base64
import subprocess
import json
import runpy
import sys
import tempfile
import zipfile
from pathlib import Path
from unittest.mock import patch

repo = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('output', type=Path)
parser.add_argument('--baseline', action='store_true')
args = parser.parse_args()
args.output.mkdir(parents=True, exist_ok=True)
marker = 'host-learned.fixture.example'
report = {}
with tempfile.TemporaryDirectory() as directory:
    home = Path(directory)
    root = home / '.config/sakamoto'
    root.mkdir(parents=True)
    conf = root / 'main.conf'
    conf.write_text('[General]\ndns-server = 127.0.0.9\ntun-included-routes = 192.0.2.0/24\n[Rule]\nDOMAIN,regional.fixture.example,DIRECT\nDOMAIN,explicit-direct.fixture.example,DIRECT\nDOMAIN,explicit-proxy.fixture.example,PROXY\nFINAL,DIRECT\n')
    (root / 'nodes.txt').write_text('vmess://11111111-1111-4111-8111-111111111111@127.0.0.1:18581#Allowed\nvless://11111111-1111-4111-8111-111111111111@192.0.2.30:18582?sni=allowed.fixture.example#Excluded\n')
    (root / 'auto-proxy.json').write_text(json.dumps({'domains': [marker]}))
    cases = [('A.zip', 'auto', '--export'), ('B.zip', 'off', '--export'), ('groups.sakamoto', 'auto', '--export-ios')]
    if not args.baseline:
        cases += [('implicit.sakamoto', 'auto', '--export-ios'), ('sr-only.sakamoto', 'auto', '--export-ios'), ('mixed.sakamoto', 'auto', '--export-ios'), ('socks-groups.sakamoto', 'auto', '--export-ios'), ('regional-ios.sakamoto', 'off', '--export-ios'), ('regional-A.zip', 'off', '--export'), ('regional-B.zip', 'off', '--export')]
    for name, mode, flag in cases:
        nodes = root / 'nodes.txt'
        original_nodes = nodes.read_text()
        if name == 'socks-groups.sakamoto':
            nodes.write_text('socks://127.0.0.1:18587#Allowed\n' + original_nodes.splitlines(keepends=True)[1])
        label = 'B' if name == 'B.zip' else 'A'
        health = {'url': f'http://health-{label.lower()}.fixture.example/health/{label}', 'interval': '7m' if label == 'B' else '4m'}
        (root / 'sakamoto.yaml').write_text(json.dumps({'conf': str(conf), 'nodes_file': str(root / 'nodes.txt'), 'tailscale_optimize': False, 'chain_enabled': False, 'fallback_enabled': False, 'urltest': health, 'experiment': {'mode': mode, 'threshold': 1}, 'automatic_group_exclusions': {'OthersAuto': ['Excluded']}}))
        sidecar = root / 'sakamoto.yaml'
        if name == 'socks-groups.sakamoto':
            from tui_group_fixture import save_socks_group
            settings = json.loads(sidecar.read_text())
            del settings['automatic_group_exclusions']
            settings['api'] = {'url': 'http://127.0.0.1:1'}
            sidecar.write_text(json.dumps(settings))
            save_socks_group(repo, sidecar, args.output / 'tui-group-save.json')
        if name == 'implicit.sakamoto':
            settings = json.loads(sidecar.read_bytes())
            settings['chain_enabled'] = True
            sidecar.write_text(json.dumps(settings))
            nodes.write_text(original_nodes + 'socks://127.0.0.1:18587#ExitOne\nsocks://127.0.0.1:18588#ExitTwo\n')
        if name in ('mixed.sakamoto', 'sr-only.sakamoto'):
            settings = json.loads(sidecar.read_bytes())
            legacy = root / 'sr.json'
            legacy.write_text(json.dumps([dict(type='socks', title='Imported', host='127.0.0.2', port='18587', note='private-sr-note'), dict(type='socks', title='Omitted', host='127.0.0.3', port='18588')]))
            settings.update(srjson=str(legacy), allow_hosts=['127.0.0.2'], utls_fingerprint='firefox')
            sidecar.write_text(json.dumps(settings))
            key = base64.urlsafe_b64encode(bytes(range(32))).decode().rstrip('=')
            nodes.write_text(original_nodes + f'vless://11111111-1111-4111-8111-111111111111@127.0.0.1:18589?security=reality&pbk={key}&sid=01&fp=chrome&sni=fixture.example#RealityLink\n')
        if name == 'sr-only.sakamoto':
            nodes.write_text('# \u8282\u70b9\u6765\u81ea Shadowrocket\n', encoding='utf-8')
        if name.startswith('regional-'):
            conf.write_text(conf.read_text().replace('dns-server = 127.0.0.9', 'dns-server = https://192.0.2.54/dns-query'))
            settings = json.loads(sidecar.read_bytes())
            settings['dns_guard'] = dict(enabled=True, local_domains=['lan'], bootstrap_ip='192.0.2.54', bootstrap_server_name='allowed.fixture.example')
            sidecar.write_text(json.dumps(settings))
        if name in ('regional-A.zip', 'regional-ios.sakamoto'):
            subprocess.run(['go', 'run', './tools/main.go', str(sidecar)], cwd=repo, check=True, capture_output=True)
            relay = next(row for row in json.loads((root / 'config.json').read_bytes())['outbounds'] if row['tag'] == 'Excluded')
            pin = hashlib.sha256(json.dumps(relay, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
            settings['regional_dns'] = dict(domains='regional.fixture.example', resolver_ip='192.0.2.53', tls_name='allowed.fixture.example', relay='Excluded', relay_fingerprint=pin)
            sidecar.write_text(json.dumps(settings))
        destination = args.output / name
        script = repo / 'android/scripts/sync-host.py'
        with patch.object(Path, 'home', return_value=home), patch.object(sys, 'argv', [str(script), flag, str(destination)]):
            try:
                runpy.run_path(str(script), run_name='__main__')
            except SystemExit as done:
                assert done.code == 0
        nodes.write_text(original_nodes)
        if flag == '--export':
            with zipfile.ZipFile(destination) as archive:
                leaked = any(marker.encode() in archive.read(entry) for entry in archive.namelist())
                state = json.loads(archive.read('sakamoto-config.json'))
                assert bool(state['regionalDNS']) == (name == 'regional-A.zip')
                config = json.loads(state['generatedContent'])
                if name == 'B.zip' and not args.baseline:
                    with zipfile.ZipFile(args.output / 'legacy-B.zip', 'w', zipfile.ZIP_DEFLATED) as legacy:
                        for entry in archive.namelist():
                            data = archive.read(entry)
                            if entry == 'sakamoto-config.json':
                                state = json.loads(data)
                                del state['sourceGroupExclusions']
                                data = json.dumps(state).encode()
                            legacy.writestr(entry, data)
                assets = {n: archive.read(n) for n in archive.namelist() if n.endswith('.srs')}
        else:
            package = json.loads(destination.read_text())
            leaked = marker in destination.read_text()
            assets = {n: base64.b64decode(v, validate=True) for n, v in package['files'].items()}
            config = json.loads(package['config'])
            groups = json.loads(package['sourceBundleJSON'])['files'].get('groups.json')
            if not args.baseline:
                assert groups and json.loads(groups) == {'OthersAuto': ['Excluded']}
                legacy_sources = json.loads(package['sourceBundleJSON'])
                del legacy_sources['files']['groups.json']
                legacy = dict(package, sourceBundleJSON=json.dumps(legacy_sources))
                if name == 'sr-only.sakamoto':
                    assert legacy_sources['files']['nodes.txt'] == '# \u8282\u70b9\u6765\u81ea Shadowrocket\n'
                    legacy = dict(package, format='sakamoto-tunnel-v2')
                    del legacy['hostGeneration']
                    (args.output / 'legacy-sr-only.sakamoto').write_text(json.dumps(legacy))
                if name == 'groups.sakamoto':
                    (args.output / 'legacy-groups.sakamoto').write_text(json.dumps(legacy))
        group = next(row for row in config['outbounds'] if row['tag'] == 'OthersAuto')
        if name == 'socks-groups.sakamoto':
            health['tolerance'] = 250
        assert all(group[key] == value for key, value in health.items()), group
        members = group['outbounds']
        assert members == {'mixed.sakamoto': ['Imported', 'Allowed'], 'sr-only.sakamoto': ['Imported']}.get(name, ['Allowed'])
        if name == 'mixed.sakamoto':
            assert 'private-sr-note' not in destination.read_text() and '127.0.0.3' not in destination.read_text()
            assert next(r for r in config['outbounds'] if r['tag'] == 'RealityLink')['tls']['utls']['fingerprint'] == 'firefox'
        assert leaked == (args.baseline and mode == 'auto')
        checked = {}
        for asset, data in assets.items():
            path = home / Path(asset).name
            path.write_bytes(data)
            decoded = path.with_suffix('.json')
            subprocess.run(['sing-box', 'rule-set', 'decompile', '-o', decoded, path], check=True, capture_output=True)
            contents = decoded.read_text()
            assert marker not in contents
            for action, outbound in [('direct', 'direct'), ('proxy', 'ExitOne' if name == 'implicit.sakamoto' else 'MainProxy')]:
                if 'explicit-' + action + '.fixture.example' in contents:
                    tag = next(row['tag'] for row in config['route']['rule_set'] if row.get('path', '').endswith('/' + asset))
                    assert any(tag in row.get('rule_set', []) and row.get('outbound') == outbound for row in config['route']['rules'])
                    checked[action] = outbound
        assert checked == {'direct': 'direct', 'proxy': 'ExitOne' if name == 'implicit.sakamoto' else 'MainProxy'}
        report[name] = {'health_check': health, 'automatic_members': members, 'host_learned_marker_present': leaked, 'explicit_routes': checked}
(args.output / 'exports.json').write_text(json.dumps(report, indent=2))
