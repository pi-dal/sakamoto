#!/usr/bin/env python3
"""Check profile-scoped S3 uploads using official zip fixtures and a disposable emulator."""
import hashlib
from s3_test_endpoint import SignedFixtureEndpoint
import http.server
import json
import os
from pathlib import Path
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import uuid
import zipfile

fixtures = Path(sys.argv[1]).resolve()
adb = ['adb', '-s', os.environ['ANDROID_SERIAL']]
pkg = 'com.pidal.sakamoto'
marker = 'private-source-A-fixture'
objects = {}
puts = []
initial = {}
authenticated = []


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def shell(*args):
    return run(*adb, 'shell', *args, capture_output=True, text=True).stdout.strip()


class Endpoint(SignedFixtureEndpoint):
    def do_GET(self):
        if self.path != '/ok.conf' and not self.authentic(b''):
            return
        if self.path == '/ok.conf':
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'[Rule]\nFINAL,DIRECT\n# downloaded fixture\n')
        elif self.path in objects:
            bundle = json.loads(objects[self.path])
            if '/nested-edit/' in self.path:
                if 'remote-main-update' not in bundle['files']['conf/main.conf']:
                    bundle['files']['conf/main.conf'] += '\n# remote-main-update\n'
                    assert 'fixture-relay' in bundle['files']['nodes.txt']
                    bundle['files']['nodes.txt'] = 'hy2://fixture@192.0.2.31:443#remote\n'
                    include = 'conf/nested/base.conf'
                    bundle['files'][include] = bundle['files'][include].replace('explicit-include.fixture.example,PROXY', 'explicit-include.fixture.example,DIRECT')
                    objects[self.path] = json.dumps(bundle).encode()
            elif '/credentials/' not in self.path and '/node-text/' not in self.path and '/duplicates/' not in self.path and '/new-profile/' not in self.path:
                self.delayed_policy(bundle)
            self.send_response(200)
            self.send_header('ETag', '"updated"')
            self.end_headers()
            try:
                self.wfile.write(json.dumps(bundle).encode())
            except (BrokenPipeError, ConnectionResetError, ssl.SSLEOFError):
                pass  # Client cancelled its GET.
        else:
            self.send_error(404)

    def delayed_policy(self, bundle):
        bundle['files']['policy.json'] = json.dumps([{'match': 'explicit-direct.fixture.example', 'action': 'reject'}])
        if '/upload-switch/' in self.path:
            archive = (fixtures / 'B.zip').read_bytes()
            transfer = str(uuid.uuid4())
            base = shell('run-as', pkg, 'sha256sum', 'files/sakamoto-config.json').split()[0]
            run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/host-sync-{transfer}.zip'", input=archive)
            response = shell('content', 'call', '--uri', f'content://{pkg}.hostsync', '--method', 'install', '--arg', transfer,
                             '--extra', 'sha256:s:' + hashlib.sha256(archive).hexdigest(), '--extra', 'base:s:' + base)
            assert 'installed=' + transfer in response, response
        shell('run-as', pkg, 'touch', 'cache/s3-waiting')
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if shell('run-as', pkg, 'ls cache/s3-release 2>/dev/null || true'):
                return
            time.sleep(.05)
        raise TimeoutError('S3 fixture response was never released')

    def do_PUT(self):
        body = self.rfile.read(int(self.headers['Content-Length']))
        puts.append(self.path)
        if not self.authentic(body):
            return
        if self.path in objects:
            assert self.headers['If-Match'] == '"updated"'
        else:
            assert self.headers['If-None-Match'] == '*'
            initial[self.path] = hashlib.sha256(body).hexdigest()
        objects[self.path] = body
        self.send_response(200)
        self.end_headers()

    def log_message(self, *args):
        pass


assert shell('getprop', 'ro.kernel.qemu') == '1'
run(*adb, 'root')
run(*adb, 'wait-for-device')
assert shell('id', '-u') == '0'
for apk in ['debug/app-debug.apk', 'androidTest/debug/app-debug-androidTest.apk']:
    run(*adb, 'install', '-r', '-g', 'android/app/build/outputs/apk/' + apk)
for name in (['nested.zip', 'B.zip'] if sys.argv[2:] == ['duplicates'] else ['nested.zip'] if sys.argv[2:] in (['nested'], ['nested-edit'], ['node-text'], ['duplicates']) else ['A.zip', 'B.zip']):
    run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/{name}'", input=(fixtures / name).read_bytes())
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    cert, key = root / 'test.pem', root / 'test.key'
    run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=fixture',
        '-addext', 'subjectAltName=IP:127.0.0.1,DNS:allowed.fixture.example', '-keyout', key, '-out', cert, capture_output=True)
    run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/source.pem'", input=cert.read_bytes())
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
    server.authenticated = authenticated
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    ca_dir = '/data/misc/keychain/certs-added'
    ca = ca_dir + '/' + root.name + '.pem'
    created = shell('test -d ' + ca_dir + ' && echo yes || echo no') == 'no'
    run(*adb, 'reverse', '--no-rebind', 'tcp:18585', f'tcp:{server.server_port}')
    try:
        shell('mkdir', '-p', ca_dir)
        run(*adb, 'push', cert, ca)
        shell('chmod', '644', ca)
        shell('restorecon', ca_dir, ca)
        report = {}
        phases = ['s3-switch', 's3-restart'] if len(sys.argv) == 2 else ['s3-download-' + sys.argv[2]]
        if sys.argv[2:] == ['duplicates']:
            phases.extend('s3-download-duplicates-' + step for step in ['setup1', 'first', 'restart1', 'setup2', 'second', 'restart2', 'setup3', 'conflict', 'retry', 'restart3', 'switch', 'switched'])
            object_path = '/fixture/duplicates/sources-v1.json'
            link = 'hy2://fixture@192.0.2.31:443#remote'
            row = link + '\r\n'
            separator = '# between identical links\r\n\r\n'
            both = row + separator + row + '# end'
        if sys.argv[2:] == ['node-text']:
            phases.extend('s3-download-node-text-' + step for step in ['download', 'idle', 'delete', 'restore', 'legacy', 'local-delete'])
            object_path = '/fixture/node-text/sources-v1.json'
            first = 'hy2://fixture@192.0.2.31:443#remote'
            second = 'hy2://fixture@192.0.2.32:443#removed'
            full_text = f'# preserve this comment\r\n\r\n{first}\r\n{second}\r\n# end'
            reduced_text = full_text.replace(second + '\r\n', '')
        if sys.argv[2:] == ['nested-edit']:
            phases.append('s3-download-nested-edit-restart')
        if sys.argv[2:] == ['credentials']:
            phases = ['s3-credentials-fault', 's3-credentials-restart']
        for phase in phases:
            puts_before = len(puts)
            authenticated.clear()
            if phase in ['s3-download-duplicates-setup1', 's3-download-duplicates-setup2', 's3-download-duplicates-setup3']:
                bundle = json.loads(objects[object_path])
                bundle['files']['nodes.txt'] = both
                objects[object_path] = json.dumps(bundle).encode()
            shell('am', 'force-stop', pkg)
            if phase in ['s3-download-node-text-download', 's3-download-node-text-delete', 's3-download-node-text-restore']:
                bundle = json.loads(objects[object_path])
                bundle['files']['nodes.txt'] = reduced_text if phase.endswith('-delete') else full_text
                objects[object_path] = json.dumps(bundle).encode()
            if phase in ('s3-download-node-text-legacy', 's3-download-duplicates-first', 's3-download-duplicates-second', 's3-download-duplicates-conflict'):
                manifest = json.loads(shell('run-as', pkg, 'cat', 'files/sakamoto-config.json'))
                revision = str(uuid.UUID(manifest['packageRevision']))
                manifest['s3Snapshot'].pop('nodeTextVersion', None)
                run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >files/sakamoto-config.json'", input=json.dumps(manifest).encode())
                stale = (both.replace('# between identical links', '# stale host comment') if sys.argv[2] == 'duplicates'
                         else full_text.replace('# preserve this comment', '# stale host comment'))
                run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >files/package-revisions/{revision}/imports/local/nodes.txt'", input=stale.encode())
                if phase.endswith('-conflict'):
                    prior_recovery = b'# existing device recovery\r\n' * 1600 + b'# archive tail'
                    directory = f'files/package-revisions/{revision}/imports/local'
                    run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >{directory}/nodes-before-sync.txt'", input=prior_recovery)
                    remote_before = objects[object_path]
            if phase == 's3-download-nested-edit-restart':
                manifest = json.loads(shell('run-as', pkg, 'cat', 'files/sakamoto-config.json'))
                revision = str(uuid.UUID(manifest['packageRevision']))
                nodes_path = f'files/package-revisions/{revision}/imports/local/nodes.txt'
                assert shell('run-as', pkg, 'cat', nodes_path) == 'hy2://fixture@192.0.2.31:443#remote'
                if os.environ.get('CHECK_LEGACY_NODES') == '1':
                    with zipfile.ZipFile(fixtures / 'nested.zip') as archive:
                        run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >{nodes_path}'", input=archive.read('imports/local/nodes.txt'))
            result = shell('am', 'instrument', '-w', '-e', 'profileIsolation', phase, pkg + '.test/' + pkg + '.NativeBindingSmokeTest')
            assert 'PASS:' in result, result
            report[phase] = json.loads(shell('run-as', pkg, 'cat', 'cache/' + phase + '.json'))
            if phase in ['s3-download-duplicates-switch', 's3-download-duplicates-switched']:
                target = '/fixture/new-profile/sources-v1.json'
                bundle = json.loads(objects[target])
                with zipfile.ZipFile(fixtures / 'B.zip') as z:
                    b = json.loads(z.read('sakamoto-config.json'))
                assert bundle['files']['nodes.txt'].splitlines() == b['nodes']
                assert not any(m in objects[target] for m in [b'stale host comment', b'existing device recovery', b'between identical links'])
                assert not any('before-sync' in name for name in bundle['files'])
                if phase.endswith('-switched'):
                    for i, wanted in enumerate([stale.encode(), prior_recovery]):
                        actual = run(*adb, 'exec-out', 'run-as', pkg, 'cat', f'cache/exported-recovery-{i}.txt', capture_output=True).stdout
                        assert actual == wanted
                    assert len(puts) == puts_before
                    report[phase].update(exported_bytes=[len(stale.encode()), len(prior_recovery)], exports_exact=True, restart_update_puts=0)
                report[phase].update(new_profile_nodes_verified=True, private_markers_absent=True)
                continue
            if phase == 's3-download-duplicates-conflict':
                assert len(puts) == puts_before and objects[object_path] == remote_before, 'Recovery conflict modified remote object'
                assert authenticated == [], authenticated
                for name, expected_bytes in [('nodes.txt', stale.encode()), ('nodes-before-sync.txt', prior_recovery)]:
                    assert run(*adb, 'exec-out', 'run-as', pkg, 'cat', directory + '/' + name, capture_output=True).stdout == expected_bytes
                report[phase].update(requests=0, update_puts=0, remote_exact=True, manifest_exact=True, active_nodes_exact=True, recovery_exact=True)
                continue
            if phase.startswith('s3-download-duplicates-'):
                step = phase.split('-')[-1]
                setup = step.startswith('setup')
                expected = both if setup else ((separator + row) if step in ('first', 'restart1') else (row + separator)) + '# end'
                updates = int(step in ('first', 'second', 'retry'))
                assert len(puts) - puts_before == updates
                assert authenticated == [('GET', object_path)] + [('PUT', object_path)] * updates
                assert json.loads(objects[object_path])['files']['nodes.txt'] == expected
                manifest = json.loads(shell('run-as', pkg, 'cat', 'files/sakamoto-config.json'))
                assert manifest['nodes'] == [link] * (2 if setup else 1)
                revision = str(uuid.UUID(manifest['packageRevision']))
                disk = run(*adb, 'exec-out', 'run-as', pkg, 'cat', f'files/package-revisions/{revision}/imports/local/nodes.txt', capture_output=True).stdout
                assert disk == expected.encode()
                if not setup:
                    recovery = run(*adb, 'exec-out', 'run-as', pkg, 'cat', f'files/package-revisions/{revision}/imports/local/nodes-before-sync.txt', capture_output=True).stdout
                    assert recovery == stale.encode()
                    assert b'stale host comment' not in objects[object_path]
                    report[phase].update(legacy_recovery_exact=True)
                if step in ('retry', 'restart3'):
                    base = f'files/package-revisions/{revision}/imports/local'
                    names = shell('run-as', pkg, 'ls', base).splitlines()
                    archive, = [name for name in names if name.startswith('nodes-before-sync-archived-')]
                    archived = run(*adb, 'exec-out', 'run-as', pkg, 'cat', f'{base}/{archive}', capture_output=True).stdout
                    assert archived == prior_recovery and b'existing device recovery' not in objects[object_path]
                    if step == 'restart3':
                        labels = ['nodes-before-sync.txt · ' + hashlib.sha256(stale.encode()).hexdigest()[:12],
                                  archive + ' · ' + hashlib.sha256(prior_recovery).hexdigest()[:12]]
                        run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/recovery-labels'", input='\n'.join(labels).encode())
                    report[phase].update(archived_bytes=len(archived), archived_sha256=hashlib.sha256(archived).hexdigest())
                report[phase].update(update_puts=updates, exact_bytes=True, sha256=hashlib.sha256(disk).hexdigest())
            if phase.startswith('s3-download-node-text-'):
                expected = reduced_text if phase.endswith('-delete') else full_text
                edited = phase.endswith('-local-delete')
                assert puts == [object_path] * (1 + int(edited)), f'{phase}: unexpected PUT count {len(puts)}'
                assert authenticated == [('GET', object_path)] + ([('PUT', object_path)] if edited else []), authenticated
                assert json.loads(objects[object_path])['files']['nodes.txt'] == expected
                manifest = json.loads(shell('run-as', pkg, 'cat', 'files/sakamoto-config.json'))
                assert manifest['nodes'] == ([first] if phase.endswith('-delete') else [first, second])
                revision = str(uuid.UUID(manifest['packageRevision']))
                data = run(*adb, 'exec-out', 'run-as', pkg, 'cat', f'files/package-revisions/{revision}/imports/local/nodes.txt', capture_output=True).stdout
                assert data == expected.encode()
                if phase.endswith('-legacy') or edited:
                    recovered = run(*adb, 'exec-out', 'run-as', pkg, 'cat', f'files/package-revisions/{revision}/imports/local/nodes-before-sync.txt', capture_output=True).stdout
                    assert recovered == stale.encode()
                    assert b'stale host comment' not in objects[object_path]
                    report[phase].update(recovery_copy_verified=True)
                report[phase].update(update_puts=int(edited), exact_bytes=True, sha256=hashlib.sha256(data).hexdigest())
        if len(sys.argv) == 3:
            if sys.argv[2] in ['duplicates', 'node-text']:
                (fixtures / ('s3-' + sys.argv[2] + '.json')).write_text(json.dumps(report, indent=2) + '\n')
                print(report)
                sys.exit(0)
            if sys.argv[2] in ['qr', 'clipboard']:
                source = sys.argv[2]
                path = f'/fixture/{source}/sources-v1.json'
                bundle = json.loads(objects[path])
                expected = f'[General]\n[Rule]\nDOMAIN,explicit-{source}.fixture.example,PROXY\nFINAL,DIRECT'
                assert puts == [path] and bundle['main_conf'] == 'conf/imported.conf'
                assert bundle['files'][bundle['main_conf']] == expected
                report[phase].update(main=bundle['main_conf'], exact_body=True, update_puts=len(puts))
            if sys.argv[2] == 'nested-edit':
                path = '/fixture/nested-edit/sources-v1.json'
                bundle = json.loads(objects[path])
                assert puts == [path, path] and report[phase]['main'] == 'main.conf'
                assert bundle['main_conf'] == 'conf/main.conf' and 'remote-main-update' in bundle['files']['conf/main.conf']
                with zipfile.ZipFile(fixtures / 'nested.zip') as archive:
                    assert bundle['files']['conf/nested/base.conf'].encode() == archive.read('imports/local/nested/base.conf')
                rule = 'DOMAIN,explicit-include.fixture.example,PROXY'
                assert rule in bundle['files']['conf/nested/base.conf'].splitlines()
                expected_nodes = ['hy2://fixture@192.0.2.31:443#remote', 'hy2://fixture@192.0.2.32:443#added']
                assert [line for line in bundle['files']['nodes.txt'].splitlines() if line] == expected_nodes
                report[phase].update(legacy_node_file=os.environ.get('CHECK_LEGACY_NODES') == '1', downloaded_file_verified=True, editor_opened_remote_nodes=True, uploaded_node_names=['remote', 'added'])
                report[phase].update(remote_main_preserved=True, downloaded_action="DIRECT", restored_export_bytes=True, local_include_rule=rule, update_puts=1)
            if sys.argv[2] == 'nested':
                path = '/fixture/nested/sources-v1.json'
                bundle = json.loads(objects[path])
                with zipfile.ZipFile(fixtures / 'nested.zip') as archive:
                    expected = {f'conf/{name}': archive.read('imports/local/' + name).decode() for name in ['main.conf', 'nested/site.conf', 'nested/base.conf']}
                actual = {k: v for k, v in bundle['files'].items() if k.startswith('conf/')}
                assert puts == [path] and actual == expected and bundle['main_conf'] == 'conf/main.conf'
                rule = 'DOMAIN,explicit-include.fixture.example,PROXY'
                assert rule in actual['conf/nested/base.conf'].splitlines()
                report[phase].update(main=bundle['main_conf'], conf_files=sorted(actual), matched_export_bytes=True, explicit_rule=rule)
            if sys.argv[2] in ['upload-control', 'upload-cancel', 'upload-switch']:
                path = '/fixture/' + sys.argv[2] + '/sources-v1.json'
                if sys.argv[2] == 'upload-switch':
                    with zipfile.ZipFile(fixtures / 'B.zip') as archive:
                        manifest = json.loads(archive.read('sakamoto-config.json'))
                        installed = json.loads(shell('run-as', pkg, 'cat', 'files/sakamoto-config.json'))
                        assert installed['sourceConfContent'] == manifest['sourceConfContent']
                        sources = [name for name in archive.namelist() if name.startswith('imports/local/') and not name.endswith('/') and name != 'imports/local/config.json']
                        assert sources
                        for name in sources:
                            digest = shell('run-as', pkg, 'sha256sum', 'files/package-revisions/' + installed['packageRevision'] + '/' + name).split()[0]
                            assert digest == hashlib.sha256(archive.read(name)).hexdigest()
                        report[phase].update(verified_B_source_files=len(sources), B_main_matches=True)
                actual = hashlib.sha256(objects[path]).hexdigest()
                sent = b'pending-upload-marker' in objects[path]
                report[phase].update(update_puts=puts.count(path) - 1, marker_received=sent, before=initial[path], after=actual)
                assert sent == (sys.argv[2] == 'upload-control') and puts.count(path) == 1 + int(sent) and (actual != initial[path]) == sent, report
            if sys.argv[2] == 'credentials':
                assert authenticated == [('GET', '/fixture/credentials/sources-v1.json')], authenticated
                report[phase].update(original_signature_verified=True, original_prefix=True)
                report[phase].update(report['s3-credentials-fault'])
            (fixtures / (phase + '.json')).write_text(json.dumps(report[phase], indent=2) + '\n')
            print(report[phase])
            sys.exit(0)
        for profile in ['A', 'B']:
            raw = objects[f'/fixture/{profile}/sources-v1.json']
            bundle = json.loads(raw)
            report[profile] = {'A_marker': marker.encode() in raw, 'main_conf': bundle.get('main_conf'), 'files': sorted(bundle['files'])}
        print(json.dumps(report, indent=2))
        assert report['A']['A_marker'] and report['A']['main_conf']
        assert not report['B']['A_marker'] and not report['B']['main_conf']
        assert not any(f.startswith('conf/') for f in report['B']['files'])
        assert report['s3-switch'] == report['s3-restart'] == {'url_source': True, 'downloaded_B': True, 'A_marker': False}
        (fixtures / 's3-profile.json').write_text(json.dumps(report, indent=2) + '\n')
    finally:
        shell('rm', '-f', ca)
        if created:
            shell('rmdir', ca_dir)
        run(*adb, 'reverse', '--remove', 'tcp:18585')
        server.shutdown()
