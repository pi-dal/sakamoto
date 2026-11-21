#!/usr/bin/env python3
"""Exercise both private package exporters against an isolated source tree."""
import hashlib
import secrets
import shutil
import subprocess
import json
from pathlib import Path
import runpy
import sys
import tempfile
from unittest.mock import patch
import zipfile

exporter = Path(__file__).resolve().parents[1] / 'android/scripts/sync-host.py'
with tempfile.TemporaryDirectory() as temp:
    home_fixture = Path(temp)
    root = home_fixture / '.config/sakamoto'
    (root / 'nested').mkdir(parents=True)
    (root / 'main.conf').write_text('[General]\ninclude = nested/site.conf\n[Rule]\nFINAL,PROXY\n[Script]\ninclude = unrelated.conf\n')
    (root / 'nested/site.conf').write_text('[General]\ninclude = base.conf\n')
    (root / 'nested/base.conf').write_text('[Rule]\nDOMAIN,explicit-include.fixture.example,PROXY\n')
    (root / 'unrelated.conf').write_text('unrelated-private-marker')
    (root / 'nodes.txt').write_text('hy2://fixture@192.0.2.30:443#fixture-relay\n')
    host_password = secrets.token_urlsafe(36)
    settings = {'conf': str(root / 'main.conf'), 'nodes_file': str(root / 'nodes.txt'), 'tailscale_optimize': False, 'chain_enabled': False,
                'experiment': {'mode': 'off'}, 'system_proxy': {'enabled': False},
                'mixed_inbound': {'enabled': True, 'allow_lan': True, 'lan_password': host_password},
                'dns_guard': {'enabled': True, 'local_domains': ['fixture.lan']}}
    sidecar = root / 'sakamoto.yaml'
    sidecar.write_text(json.dumps(settings))
    # Pin the canonical generated outbound exactly as the desktop settings editor does.
    subprocess.run(['go', 'run', './tools/main.go', str(sidecar)], cwd=exporter.parents[2], check=True, capture_output=True)
    leaf = next(n for n in json.loads((root / 'config.json').read_bytes())['outbounds'] if n['tag'] == 'fixture-relay')
    pin = hashlib.sha256(json.dumps(leaf, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    regional = dict(domains='voice.example', resolver_ip='192.0.2.53', tls_name='resolver.example', relay='fixture-relay', relay_fingerprint=pin)
    settings['regional_dns'] = regional
    sidecar.write_text(json.dumps(settings))
    (root / 'config.json').write_text('{"route":{"final":"direct"}}')
    expected = {'main.conf', 'nested/site.conf', 'nested/base.conf'}
    def export(flag, dest):
        with patch.object(Path, 'home', return_value=home_fixture), patch.object(sys, 'argv', [str(exporter), flag, str(dest)]):
            try:
                runpy.run_path(str(exporter), run_name='__main__')
            except SystemExit as done:
                assert done.code == 0

    for flag in ('--export', '--export-ios'):
        dest = home_fixture / flag[2:]
        export(flag, dest)
        if flag == '--export':
            if len(sys.argv) == 2:
                shutil.copyfile(dest, Path(sys.argv[1]) / 'nested.zip')
            with zipfile.ZipFile(dest) as archive:
                state = json.loads(archive.read('sakamoto-config.json'))
                runtime = json.loads(state['generatedContent'])
                assert 'FINAL,PROXY' in state['sourceConfContent']
                assert json.loads(state['hostMetadata'])['sourceFinal'] == 'PROXY'
                assert state['regionalDNS'] == regional and 'regional-dns.json' in archive.namelist()
                assert all(host_password.encode() not in archive.read(name) for name in archive.namelist())
                names = {name.removeprefix('imports/local/') for name in archive.namelist()}
                assert all(b'unrelated-private-marker' not in archive.read(name) for name in archive.namelist())
        else:
            package = dest.read_text()
            payload = json.loads(package)
            assert 'unrelated-private-marker' not in package and host_password not in package
            assert payload['sourceFinal'] == 'PROXY'
            assert payload['regionalDNS'] == regional and payload['format'] == 'sakamoto-tunnel-v2'
            # Exercise the same Go generator and regional overlay as iOS on Generate/Apply.
            regenerated = json.loads(subprocess.check_output(['go', 'run', './scripts/e2e/mobileexport', dest], cwd=exporter.parents[2]))
            assert any('fixture.lan' in r.get('domain_suffix', []) for r in regenerated['dns']['rules'])
            assert all(r.get('clash_mode') != 'Direct' for r in regenerated['dns']['rules'])
            assert all(i.get('tag') != 'protected-dns' for i in regenerated['inbounds'])
            runtime = json.loads(json.loads(package)['config'])
            sources = json.loads(json.loads(package)['sourceBundleJSON'])['files']
            assert 'FINAL,PROXY' in sources['main.conf']
            names = sources.keys()
        assert all(i['type'] != 'mixed' for i in runtime['inbounds'])
        assert runtime['route']['final'] == 'MainProxy'
        assert any(node['tag'] == 'fixture-relay' for node in runtime['outbounds'])
        assert json.loads((root / 'config.json').read_text())['route']['final'] == 'direct'
        assert {name for name in names if name.endswith('.conf')} == expected
    # Inherit FINAL from an include while an explicit or migrated mode overrides it.
    (root / 'main.conf').write_text('[General]\ninclude = nested/site.conf\n')
    (root / 'nested/base.conf').write_text('[Rule]\nFINAL,DIRECT\n')
    settings.pop('experiment')
    for label in ('plain-yaml-on', 'legacy'):
        import yaml
        raw = yaml.safe_dump(settings)
        sidecar.write_text(raw + ('experiment:\n  mode: on\n' if label == 'plain-yaml-on' else ''))
        (root / 'config.json').write_text('{"route":{"final":"MainProxy"}}')
        dest = home_fixture / label
        export('--export-ios', dest)
        package = json.loads(dest.read_bytes())
        assert json.loads(package['hostMetadataJSON'])['experiment']['mode'] == 'on'
        assert package['sourceFinal'] == 'DIRECT'
        assert json.loads(package['config'])['route']['final'] == 'MainProxy'
        regenerated = json.loads(subprocess.check_output(['go', 'run', './scripts/e2e/mobileexport', dest], cwd=exporter.parents[2]))
        assert regenerated['route']['final'] == 'MainProxy'
    (root / 'nested/site.conf').write_text('[General]\ninclude = ../main-base.conf\n')
    (root / 'main-base.conf').write_text('[Rule]\nFINAL,PROXY\n')
    dest = home_fixture / 'invalid-graph'
    try:
        export('--export-ios', dest)
    except RuntimeError:
        assert not dest.exists()
    else:
        raise AssertionError('exported a source graph rejected by the mobile importer')
print('Verified private exports, DNS policy, Experiment mode migration, source fallback and iOS include checks')
