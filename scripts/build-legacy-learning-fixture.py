#!/usr/bin/env python3
"""Build the synthetic pre-scope Android B package with the historical desktop exporter."""
import argparse
import json
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
from unittest.mock import patch
from zipfile import ZipFile

parser = argparse.ArgumentParser()
parser.add_argument('legacy_checkout', type=Path)
parser.add_argument('output', type=Path)
parser.add_argument('--export-mode', choices=['off', 'auto', 'on'], default='off')
parser.add_argument('--fallback', action='store_true')
args = parser.parse_args()
repo = args.legacy_checkout.resolve()
output = args.output.resolve()
with tempfile.TemporaryDirectory() as tmp:
    home = Path(tmp)
    root = home / '.config/sakamoto'
    root.mkdir(parents=True)
    conf = root / 'main.conf'
    conf.write_text('[General]\ndns-server = 127.0.0.9\ntun-included-routes = 192.0.2.0/24\n[Rule]\nDOMAIN,explicit-direct.fixture.example,DIRECT\nDOMAIN,explicit-proxy.fixture.example,PROXY\nDOMAIN,rejected.fixture.example,REJECT\nFINAL,DIRECT\n')
    nodes = root / 'nodes.txt'
    nodes.write_text('vmess://11111111-1111-4111-8111-111111111111@127.0.0.1:18581#Allowed\n')
    if args.fallback:
        with nodes.open('a') as extra:
            extra.write('vmess://22222222-2222-4222-8222-222222222222@127.0.0.1:18581#Backup\n')
            extra.write('vmess://33333333-3333-4333-8333-333333333333@127.0.0.1:18581#ProbeOnly\n')
    settings = root / 'sakamoto.yaml'
    settings.write_text(json.dumps(dict(conf=str(conf), nodes_file=str(nodes), tailscale_optimize=False, chain_enabled=False,
                                        fallback_enabled=args.fallback, fallbacks={'MainProxy': ['OthersAuto', 'ManualPick']} if args.fallback else {},
                                        automatic_group_exclusions={'OthersAuto': ['Backup', 'ProbeOnly']} if args.fallback else {},
                                        recover_after=1, urltest=dict(url='http://health-b.fixture.example/health/B', interval='7m'),
                                        experiment=dict(mode=args.export_mode, threshold=1))))
    subprocess.run(['go', 'run', './tools/main.go', str(settings)], cwd=repo, check=True)
    if args.fallback:
        path = root / 'config.json'
        core = json.loads(path.read_text())
        selectors = {group['tag']: group for group in core['outbounds']}
        selectors['ManualPick']['outbounds'] = ['Backup', 'ProbeOnly']
        selectors['ManualPick']['default'] = 'Backup'
        selectors['MainProxy']['default'] = 'ManualPick'
        path.write_text(json.dumps(core))
    script = repo / 'android/scripts/sync-host.py'
    with patch.object(Path, 'home', return_value=home), patch.object(sys, 'argv', [str(script), '--export', str(output)]):
        try:
            runpy.run_path(str(script), run_name='__main__')
        except SystemExit as done:
            if done.code != 0:
                raise
with ZipFile(output) as archive:
    manifest = json.loads(archive.read('sakamoto-config.json'))
    assert not manifest.get('scopedExperiment') and not manifest.get('modeCacheID')
    assert json.loads(manifest['hostMetadata'])['experiment']['mode'] == args.export_mode
    route = json.loads(manifest['generatedContent'])['route']
    assert {tag for rule in route['rules'] for tag in rule.get('rule_set', [])} <= {s['tag'] for s in route['rule_set']}
