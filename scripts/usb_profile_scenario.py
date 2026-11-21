"""Exercise the debug USB entry point against the VPN scenario's synthetic packages."""
import hashlib
import json
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
from unittest.mock import patch
import zipfile


def regenerate_edited(adb, pkg):
    def read(path):
        return subprocess.check_output(adb + ['exec-out', 'run-as', pkg, 'cat', path])

    with tempfile.TemporaryDirectory() as folder:
        directory = Path(folder)
        root = directory / '.config/sakamoto'
        root.mkdir(parents=True)
        manifest = json.loads(read('files/sakamoto-config.json'))
        assert manifest['sourceNeedsGenerate'] and manifest['packageRevision'] != manifest['confirmedRevision']
        base = 'files/package-revisions/' + manifest['packageRevision'] + '/imports/local/'
        for name in ['main.conf', 'nodes.txt']:
            (root / name).write_bytes(read(base + name))
        assert 'explicit-direct.fixture.example,PROXY' in (root / 'main.conf').read_text()
        settings = dict(conf=str(root / 'main.conf'), nodes_file=str(root / 'nodes.txt'), tailscale_optimize=False,
                        chain_enabled=False, fallback_enabled=False, automatic_group_exclusions={'OthersAuto': ['Excluded']},
                        urltest={'url': 'http://health-b.fixture.example/health/B', 'interval': '7m'}, experiment={'mode': 'off'})
        (root / 'sakamoto.yaml').write_text(json.dumps(settings))
        exporter = Path(__file__).resolve().parents[1] / 'android/scripts/sync-host.py'
        output = directory / 'regenerated.zip'
        with patch.object(Path, 'home', return_value=directory), patch.object(sys, 'argv', [str(exporter), '--export', str(output)]):
            try:
                runpy.run_path(str(exporter), run_name='__main__')
            except SystemExit as done:
                assert done.code == 0
        subprocess.run(adb + ['shell', f"run-as {pkg} sh -c 'cat >cache/generated.tmp && mv cache/generated.tmp cache/regenerated.zip'"], input=output.read_bytes(), check=True)


def exercise(serial, fixtures, check_edits=False, chain_switch=False, install_b=False):
    pkg = 'com.pidal.sakamoto'
    adb = ['adb', '-s', serial]
    exporter = Path(__file__).resolve().parents[1] / 'android/scripts/sync-host.py'
    run = subprocess.run

    def read(path):
        return subprocess.check_output(adb + ['exec-out', 'run-as', pkg, 'cat', path])

    def snapshot():
        manifest = read('files/sakamoto-config.json')
        revision = json.loads(manifest)['packageRevision']
        base = 'files/package-revisions/' + revision
        names = subprocess.check_output(adb + ['shell', 'run-as', pkg, 'find', base, '-type', 'f'], text=True).splitlines()
        files = {name: hashlib.sha256(read(name)).hexdigest() for name in names}
        assert any(name.endswith('.srs') for name in files) and any(name.endswith('.conf') for name in files)
        return hashlib.sha256(manifest).hexdigest(), files

    transmitted = 0

    def interrupt(command, **kwargs):
        nonlocal transmitted
        if command[:len(adb)] == adb and 'input' in kwargs:
            data = kwargs['input']
            transmitted = len(data) // 2
            run(command, **dict(kwargs, input=data[:transmitted]))
            raise InterruptedError('USB transfer interrupted')
        return run(command, **kwargs)

    with tempfile.TemporaryDirectory() as folder:
        directory = Path(folder)
        root = directory / '.config/sakamoto'
        root.mkdir(parents=True)
        with zipfile.ZipFile(fixtures / ('regional-B.zip' if chain_switch else 'A.zip')) as archive:
            for name in ['main.conf', 'nodes.txt']:
                (root / name).write_bytes(archive.read('imports/local/' + name))
        def sync(label, transport, subscriptions=None):
            settings = dict(conf=str(root / 'main.conf'), nodes_file=str(root / 'nodes.txt'), tailscale_optimize=False,
                            chain_enabled=False, fallback_enabled=False, automatic_group_exclusions={'OthersAuto': ['Excluded']},
                            urltest={'url': f'http://health-{label.lower()}.fixture.example/health/{label}', 'interval': '4m' if label == 'A' else '7m'},
                            experiment={'mode': 'auto' if label == 'A' else 'off', 'threshold': 1})
            if chain_switch:
                settings['dns_guard'] = dict(enabled=True, local_domains=['lan'], bootstrap_ip='192.0.2.54', bootstrap_server_name='allowed.fixture.example')
            settings['subscriptions'] = subscriptions or []
            (root / 'sakamoto.yaml').write_text(json.dumps(settings))
            with patch.object(Path, 'home', return_value=directory), patch.object(sys, 'argv', [str(exporter), serial]), patch.object(subprocess, 'run', transport):
                runpy.run_path(str(exporter), run_name='__main__')
        if chain_switch or install_b:
            sync('B', run)
            assert not json.loads(read('files/sakamoto-config.json')).get('proxyChain')
            return
        if check_edits:
            def instrument(phase):
                run(adb + ['shell', 'am', 'force-stop', pkg], check=True)
                result = run(adb + ['shell', 'am', 'instrument', '-w', '-e', 'profileIsolation', phase,
                             pkg + '.test/' + pkg + '.NativeBindingSmokeTest'], check=True, capture_output=True, text=True)
                assert 'PASS:' in result.stdout, result.stdout
            results = {}
            for phase in ['usb-delete', 'usb-url']:
                instrument(phase)
                edited = snapshot()
                subscriptions = [{'name': 'USB fixture subscription', 'url': 'https://allowed.fixture.example/sub', 'format': 'auto'}] if phase == 'usb-delete' else []
                try:
                    sync('B', run, subscriptions)
                except RuntimeError as error:
                    assert 'pending source edits' in str(error), str(error)
                else:
                    raise AssertionError('USB replaced pending device edits')
                instrument(phase + '-restart')
                assert snapshot() == edited
                results[phase] = dict(sync_refused=True, pending_edit_verified_after_restart=True, all_revision_hashes_match=True)
            (fixtures / 'usb-source-conflicts.json').write_text(json.dumps(results, indent=2))
            return
        sync('A', run)
        before = snapshot()
        try:
            sync('B', interrupt)
        except InterruptedError:
            pass
        assert transmitted > 0 and snapshot() == before
        (fixtures / 'usb-interruption.json').write_text(json.dumps(dict(bytes_transferred_before_interrupt=transmitted,
            manifest_unchanged=True, revision_files_verified=len(before[1]), sources_and_rules_unchanged=True), indent=2))
