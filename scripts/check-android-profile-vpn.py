#!/usr/bin/env python3
"""Observe real direct SYNs on a disposable rootable Android emulator."""
import json
import http.server
import ssl
import socketserver
import threading
from concurrent.futures import ThreadPoolExecutor
import urllib.request
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

fixtures = Path(sys.argv[1]).resolve()
serial = os.environ['ANDROID_SERIAL']
adb = ['adb', '-s', serial]
pkg = 'com.pidal.sakamoto'

def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)

def shell(*args):
    return run(*adb, 'shell', *args, capture_output=True, text=True).stdout.strip()

assert shell('getprop', 'ro.kernel.qemu') == '1'
run(*adb, 'root')
run(*adb, 'wait-for-device')
assert shell('id', '-u') == '0'
assert shell('getprop', 'ro.product.cpu.abi') == 'arm64-v8a'
run(*adb, 'install', '-r', '-g', 'android/app/build/outputs/apk/debug/app-debug.apk')
run(*adb, 'install', '-r', 'android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk')
shell('appops', 'set', pkg, 'ACTIVATE_VPN', 'allow')
shell('run-as', pkg, 'mkdir', '-p', 'cache')
for name in ['A.zip', 'B.zip', 'legacy-B.zip', 'regional-A.zip', 'regional-B.zip']:
    run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/{name}'", input=(fixtures / name).read_bytes())
if '--legacy-regional' in sys.argv[2:] or '--legacy-manual' in sys.argv[2:]:
    manual = '--legacy-manual' in sys.argv[2:]
    import io
    import zipfile
    archive = io.BytesIO()
    with zipfile.ZipFile(fixtures / ('A.zip' if manual else 'regional-B.zip')) as src, zipfile.ZipFile(archive, 'w') as dest:
        for item in src.infolist():
            data = src.read(item)
            if item.filename == 'sakamoto-config.json':
                state = json.loads(data)
                del state['routeSourceVersion']
                data = json.dumps(state).encode()
            dest.writestr(item, data)
    name = 'manual-old.zip' if manual else 'regional-local.zip'
    run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/{name}'", input=archive.getvalue())
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    cert, key = root / 'source.pem', root / 'source.key'
    run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=allowed.fixture.example', '-addext', 'subjectAltName=DNS:allowed.fixture.example,IP:192.0.2.54', '-keyout', key, '-out', cert, capture_output=True)
    run(*adb, 'shell', f"run-as {pkg} sh -c 'cat >cache/source.pem'", input=cert.read_bytes())
    regional_queries = []
    class Source(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            packet = self.rfile.read(int(self.headers['Content-Length']))
            name = b'\x08regional\x07fixture\x07example\x00'
            if self.path != '/dns-query' or not packet[12:].startswith(name):
                self.send_error(400)
                return
            (regional_queries if self.server is regional_server else bootstrap_queries).append('regional.fixture.example')
            end = 12 + len(name) + 4
            answer = bytes.fromhex('c00c00010001000000000004cb007122') if packet[end-4:end-2] == b'\x00\x01' else b''
            response = packet[:2] + b'\x81\x80\x00\x01' + int(bool(answer)).to_bytes(2, 'big') + bytes(4) + packet[12:end] + answer
            self.send_response(200)
            self.send_header('Content-Type', 'application/dns-message')
            self.send_header('Content-Length', str(len(response)))
            self.end_headers()
            self.wfile.write(response)
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'[Rule]\nFINAL,DIRECT\n# downloaded fixture\n')
        def log_message(self, *args):
            pass
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    https = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Source)
    # Count validated DNS messages on separate regional and ordinary listeners.
    bootstrap_queries = []
    regional_server = https
    ordinary = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Source)
    ordinary.socket = tls.wrap_socket(ordinary.socket, server_side=True)
    threading.Thread(target=ordinary.serve_forever, daemon=True).start()
    https.socket = tls.wrap_socket(https.socket, server_side=True)
    threading.Thread(target=https.serve_forever, daemon=True).start()
    binary = root / 'origin'
    run('go', 'build', '-o', binary, './scripts/e2e/mobileorigin', env=dict(os.environ, GOOS='linux', GOARCH='arm64', CGO_ENABLED='0'))
    remote = '/data/local/tmp/sakamoto-profile-origin'
    run(*adb, 'push', binary, remote)
    shell('chmod', '700', remote)
    config = {'log': {'level': 'warn'}, 'inbounds': [{'type': 'vmess', 'listen': '127.0.0.1', 'listen_port': 18581, 'users': [{'uuid': '11111111-1111-4111-8111-111111111111'}]}], 'route': {'rules': [{'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': 18584}]}, 'outbounds': [{'type': 'direct', 'tag': 'fixture'}]}
    config['inbounds'].append({'type': 'vless', 'tag': 'regional', 'listen': '127.0.0.1', 'listen_port': 18582,
        'users': [{'uuid': '11111111-1111-4111-8111-111111111111'}], 'tls': {'enabled': True, 'certificate_path': str(cert), 'key_path': str(key)}})
    config['route']['rules'].insert(0, {'inbound': 'regional', 'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': https.server_port})
    config['route']['rules'].insert(0, {'ip_cidr': ['192.0.2.54/32'], 'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': ordinary.server_port})
    class ChainExit(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(202)
            self.end_headers()
        do_HEAD = do_GET
        def log_message(self, *args):
            pass
    landing = http.server.ThreadingHTTPServer(('127.0.0.1', 0), ChainExit)
    threading.Thread(target=landing.serve_forever, daemon=True).start()
    config['route']['rules'][:0] = [
        {'ip_cidr': ['192.0.2.30/32'], 'port': 18582, 'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': 18582},
        {'inbound': 'regional', 'port': 80, 'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': landing.server_port},
        {'inbound': 'regional', 'port': 443, 'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': https.server_port},
    ]
    fallback_fixture = '--installed-fallback' in sys.argv[2:]
    backup_checks = []
    if fallback_fixture:
        class CandidateProbe(socketserver.BaseRequestHandler):
            def handle(self):
                self.request.settimeout(3)
                if self.request.recv(1) == b'\x16':
                    backup_checks.append(1)
        candidate = socketserver.ThreadingTCPServer(('127.0.0.1', 0), CandidateProbe)
        threading.Thread(target=candidate.serve_forever, daemon=True).start()
        config['inbounds'][0]['users'].append(dict(name='standby', uuid='22222222-2222-4222-8222-222222222222'))
        config['inbounds'][0]['users'].append(dict(name='probe-only', uuid='33333333-3333-4333-8333-333333333333'))
        config['route']['rules'][:0] = [dict(auth_user=user, port=port, action='route', outbound='fixture',
            override_address='127.0.0.1', override_port=target) for user, port, target in [
                ('probe-only', 443, candidate.server_address[1]), ('standby', 80, landing.server_port)]]
    conf = root / 'proxy.json'
    conf.write_text(json.dumps(config))
    proxy = subprocess.Popen(['sing-box', 'run', '-c', conf])
    origin = subprocess.Popen(adb + ['shell', remote])
    chain = 'SK_PROFILE_E2E'
    match = ['-o', 'lo', '-d', '127.0.0.2', '-p', 'tcp', '--dport', '80', '--syn']
    cert_hash = run('openssl', 'x509', '-subject_hash_old', '-noout', '-in', cert, capture_output=True, text=True).stdout.strip()
    ca_dir = '/data/misc/user/0/cacerts-added'
    ca_file = f'{ca_dir}/{cert_hash}.0'
    assert shell('test -e ' + ca_file + ' && echo exists || echo absent') == 'absent'
    relay_rule = ['-t', 'nat', 'OUTPUT', '-d', '192.0.2.30', '-p', 'tcp', '--dport', '18582', '-j', 'REDIRECT', '--to-ports', '18582']
    bootstrap_rule = [*relay_rule[:4], '192.0.2.54', '-p', 'tcp', '--dport', '443', '-j', 'REDIRECT', '--to-ports', '18586']
    try:
        run(*adb, 'reverse', '--no-rebind', 'tcp:18586', f'tcp:{ordinary.server_port}')
        shell('iptables', *bootstrap_rule[:2], '-I', *bootstrap_rule[2:])
        shell('mkdir', '-p', ca_dir)
        shell('chmod', '755', ca_dir)
        run(*adb, 'push', cert, ca_file)
        shell('chmod', '644', ca_file)
        shell('restorecon', '-R', ca_dir)
        run(*adb, 'reverse', '--no-rebind', 'tcp:18582', 'tcp:18582')
        shell('iptables', *relay_rule[:2], '-I', *relay_rule[2:])
        time.sleep(.5)
        assert proxy.poll() is None and origin.poll() is None
        run(*adb, 'reverse', '--no-rebind', 'tcp:18585', f'tcp:{https.server_port}')
        run(*adb, 'reverse', '--no-rebind', 'tcp:18581', 'tcp:18581')
        run(*adb, 'forward', '--no-rebind', 'tcp:18584', 'tcp:18584')
        shell('iptables', '-N', chain)
        shell('iptables', '-A', chain, '-j', 'RETURN')
        shell('iptables', '-I', 'OUTPUT', *match, '-j', chain)
        def health_counts():
            with urllib.request.urlopen('http://127.0.0.1:18584/counts', timeout=3) as response:
                return json.load(response)
        phases = ['vpn-regional-A', 'vpn-regional-B', 'vpn-regional-restart']
        if '--regional-only' not in sys.argv[2:]:
            phases = ['vpn-A', 'vpn-B', 'vpn-restart', 'vpn-usb-A'] + phases
        if '--chain-only' in sys.argv[2:]:
            phases = []
        if '--regional-only' not in sys.argv[2:]:
            phases += ['vpn-chain-A', 'vpn-chain-B', 'vpn-chain-restart']
        if '--stale-only' in sys.argv[2:]:
            phases = ['vpn-A']
        if '--regional-only' not in sys.argv[2:] and '--chain-only' not in sys.argv[2:]:
            phases += ['vpn-stale-B', 'vpn-stale-json', 'vpn-stale-node', 'vpn-stale-subscription', 'vpn-stale-url', 'vpn-delete-race', 'vpn-undo-race', 'vpn-stale-restart']
        faults = [f'vpn-snapshot-{case}{suffix}' for case in ['create', 'partial', 'source'] for suffix in ['', '-restart']]
        faults += ['vpn-save-disconnect', 'vpn-save-reconnect', 'vpn-pending-edit', 'vpn-pending-reconnect']
        deferred = ['vpn-import-picker', 'vpn-import-qr', 'vpn-import-clipboard', 'vpn-reload-expiry', 'vpn-reload-restart'] + faults
        if '--deferred-only' in sys.argv[2:]:
            phases = deferred
        elif '--stale-only' not in sys.argv[2:] and '--chain-only' not in sys.argv[2:] and '--regional-only' not in sys.argv[2:]:
            phases += deferred
        if '--reload-only' in sys.argv[2:]:
            phases = ['vpn-reload-expiry', 'vpn-reload-restart']
        if '--snapshot-only' in sys.argv[2:]:
            phases = faults
        if '--revision-only' in sys.argv[2:]:
            phases = ['vpn-snapshot-source', 'vpn-snapshot-source-restart']
        if '--pending-only' in sys.argv[2:]:
            phases = ['vpn-pending-edit', 'vpn-pending-reconnect']
        reconnect_fault = ['vpn-save-fault-edit', 'vpn-save-fault-reconnect']
        if '--reconnect-fault-only' in sys.argv[2:]:
            phases = reconnect_fault
        elif not sys.argv[2:]:
            phases += reconnect_fault
        if '--policy-only' in sys.argv[2:]:
            phases = ['vpn-policy-edit', 'vpn-policy-restart']
        elif not sys.argv[2:]:
            phases += ['vpn-policy-edit', 'vpn-policy-restart']
        forget = ['vpn-forget-edit', 'vpn-forget-restart', 'vpn-forget-denied', 'vpn-forget-denied-restart']
        learning_recovery = ['vpn-learn-kill', 'vpn-learn-restart', 'vpn-learn-rollback']
        if '--learning-recovery' in sys.argv[2:]:
            phases = learning_recovery
        elif not sys.argv[2:]:
            phases += learning_recovery
        if '--history-fault' in sys.argv[2:]:
            phases = ['vpn-history-denied', 'vpn-history-restart']
        elif not sys.argv[2:]:
            phases += ['vpn-history-denied', 'vpn-history-restart']
        if '--forget-fault' in sys.argv[2:]:
            phases = forget
        elif not sys.argv[2:]:
            phases += forget
        if '--promotion-only' in sys.argv[2:]:
            phases = ['vpn-promote-edit', 'vpn-promote-restart']
        elif not sys.argv[2:]:
            phases += ['vpn-promote-edit', 'vpn-promote-restart']
        routing_changes = ['vpn-final-edit', 'vpn-final-restart', 'vpn-reorder-edit', 'vpn-reorder-restart']
        if '--routing-changes-only' in sys.argv[2:]:
            phases = routing_changes
        elif not sys.argv[2:]:
            phases += routing_changes
        priority = ['vpn-priority-edit', 'vpn-priority-restart']
        if '--priority-only' in sys.argv[2:]:
            phases = priority
        elif not sys.argv[2:]:
            phases += priority
        if '--legacy-regional' in sys.argv[2:]:
            phases = ['vpn-regional-local', 'vpn-regional-local-restart']
        if '--legacy-manual' in sys.argv[2:]:
            phases = ['vpn-policy-manual-edit', 'vpn-policy-manual-restart']
        if '--installed-learning' in sys.argv[2:]:
            phases = ['vpn-A', 'vpn-upgrade-B', 'vpn-upgrade-restart']
            if '--confirm-learning' in sys.argv[2:]:
                phases.append('vpn-upgrade-confirm')
        if '--tailscale-key' in sys.argv[2:]:
            phases = ['vpn-tskey-save', 'vpn-tskey-apply', 'vpn-tskey-restart', 'vpn-tskey-return', 'vpn-tskey-delete', 'vpn-tskey-deleted-restart']
        if '--installed-final' in sys.argv[2:]:
            phases = ['vpn-oldfinal-upgrade', 'vpn-oldfinal-enable', 'vpn-oldfinal-restart', 'vpn-oldfinal-restore']
        if fallback_fixture:
            phases = ['vpn-fallback-upgrade', 'vpn-fallback-restart', 'vpn-fallback-enable', 'vpn-fallback-enabled-restart']
        if '--experiment-screen' in sys.argv[2:]:
            phases = ['vpn-screen-A', 'vpn-B', 'vpn-restart']
        elif not sys.argv[2:]:
            phases.append('vpn-screen-A')
        if '--repair-switch' in sys.argv[2:]:
            phases = ['vpn-repair-A', 'vpn-repair-B']
        elif not sys.argv[2:]:
            phases += ['vpn-repair-A', 'vpn-repair-B']
        if '--export-control' in sys.argv[2:]:
            phases = ['vpn-A', 'vpn-B']
        if '--s3-restart' in sys.argv[2:]:
            assert sys.argv[3] in ['cancel', 'disk']
            phases = ['vpn-s3-' + sys.argv[3] + '-restart']
        for phase in phases:
            if phase == 'vpn-chain-B':
                from usb_profile_scenario import exercise
                exercise(serial, fixtures, chain_switch=True)
            if phase == 'vpn-usb-A':
                from usb_profile_scenario import exercise
                exercise(serial, fixtures)
            if phase in ('vpn-upgrade-B', 'vpn-fallback-upgrade', 'vpn-oldfinal-upgrade'):
                from installed_learning_fixture import stage_legacy_b
                stage_legacy_b(adb, pkg, Path(sys.argv[3]), fixtures)
            probes_before = len(backup_checks)
            before = health_counts()
            regional_before = len(regional_queries)
            ordinary_before = len(bootstrap_queries)
            shell('am', 'force-stop', pkg)
            if phase in ('vpn-forget-restart', 'vpn-history-restart'):
                shell('run-as', pkg, 'chmod', forget_mode, 'shared_prefs')
            if phase in ('vpn-forget-edit', 'vpn-history-denied'):
                forget_mode = shell('run-as', pkg, 'stat', '-c', '%a', 'shared_prefs')
            shell('run-as', pkg, 'rm', '-f', f'cache/{phase}.json')
            shell('iptables', '-Z', chain)
            command = ('am', 'instrument', '-w', '-e', 'profileIsolation', phase, pkg + '.test/' + pkg + '.NativeBindingSmokeTest')
            if (phase.startswith('vpn-stale-') and not phase.endswith('restart')) or phase == 'vpn-delete-race' or phase.startswith('vpn-import-'):
                from usb_profile_scenario import exercise
                shell('run-as', pkg, 'rm', '-f', 'cache/threshold-open')
                with ThreadPoolExecutor(max_workers=1) as executor:
                    submitting = executor.submit(shell, *command)
                    # Include cold instrumentation startup as well as the in-app handshake.
                    deadline = time.monotonic() + 120
                    while not submitting.done() and time.monotonic() < deadline:
                        if shell('run-as', pkg, 'ls', 'cache/threshold-open', '2>/dev/null || true'):
                            exercise(serial, fixtures, install_b=True)
                            break
                        time.sleep(.1)
                    result = submitting.result()
            elif phase == 'vpn-learn-rollback':
                shell('run-as', pkg, 'rm', '-f', 'cache/learned-before-rollback', 'cache/measure-learning-rollback')
                with ThreadPoolExecutor(max_workers=1) as executor:
                    applying = executor.submit(shell, *command)
                    deadline = time.monotonic() + 60
                    while not applying.done() and time.monotonic() < deadline:
                        if shell('run-as', pkg, 'ls', 'cache/learned-before-rollback', '2>/dev/null || true'):
                            shell('iptables', '-Z', chain)
                            shell('run-as', pkg, 'touch', 'cache/measure-learning-rollback')
                            break
                        time.sleep(.1)
                    result = applying.result()
            elif phase == 'vpn-snapshot-source':
                from usb_profile_scenario import regenerate_edited
                shell('run-as', pkg, 'rm', '-f', 'cache/source-ready', 'cache/regenerated.zip')
                with ThreadPoolExecutor(max_workers=1) as executor:
                    applying = executor.submit(shell, *command)
                    deadline = time.monotonic() + 45
                    while not applying.done() and time.monotonic() < deadline:
                        if shell('run-as', pkg, 'ls', 'cache/source-ready', '2>/dev/null || true'):
                            regenerate_edited(adb, pkg)
                            break
                        time.sleep(.1)
                    result = applying.result()
            else:
                result = shell(*command)
            print(phase, result, flush=True)
            if phase == 'vpn-learn-kill':
                assert 'Process crashed' in result, result
            else:
                assert 'PASS:' in result, (phase, len(regional_queries) - regional_before, len(bootstrap_queries) - ordinary_before)
            report = json.loads(shell('run-as', pkg, 'cat', f'cache/{phase}.json'))
            count = int(shell('iptables', '-L', chain, '-v', '-x', '-n').splitlines()[-1].split()[0])
            after = health_counts()
            hits = {key: after[key] - value for key, value in before.items()}
            if fallback_fixture:
                probes = len(backup_checks) - probes_before
                report['fallback_candidate_requests'] = probes
                if phase == 'vpn-fallback-enable':
                    assert probes > 0, report
                elif not report['fallback_enabled']:
                    assert probes == 0, report
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2) + '\n')
                continue
            if phase.startswith('vpn-import-'):
                report.update(json.loads(shell('run-as', pkg, 'cat', 'cache/deferred-import.json')))
            if phase == 'vpn-reload-expiry':
                report.update(json.loads(shell('run-as', pkg, 'cat', 'cache/reload-expiry.json')))
            if phase == 'vpn-stale-url':
                report.update(json.loads(shell('run-as', pkg, 'cat', 'cache/stale-url.json')))
            if phase in ['vpn-delete-race', 'vpn-undo-race']:
                report.update(json.loads(shell('run-as', pkg, 'cat', 'cache/source-save-race.json')))
            report['regional_doh_queries'] = len(regional_queries) - regional_before
            if phase in learning_recovery:
                baseline = 2 if phase.endswith('kill') else 0
                report['direct_syns'] = count
                assert count == baseline, report
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2) + '\n')
                continue
            if phase.startswith('vpn-history-'):
                assert report['same_instance'] and report['learned_origin'] == report['proxy_control'] == 204, report
                report['direct_syns_to_failed_origin'] = count
                assert count == (0 if phase.endswith('restart') else 2), report
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2) + '\n')
                continue
            if phase.startswith('vpn-forget-'):
                retained = phase.startswith('vpn-forget-denied')
                assert report['learned_count'] == int(retained) and report['proxy_control'] == 204, report
                assert report['origin_response'] == ('204' if retained else 'direct_refused'), report
                if phase.endswith('-restart'):
                    assert (count == 0) == retained, report
                report['direct_syns_to_failed_origin'] = count
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2) + '\n')
                continue
            if phase.startswith('vpn-priority-'):
                report['direct_syns_to_failed_origin'] = count
                assert count == (2 if phase.endswith('-edit') else 0), report
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2))
                continue
            if phase == 'vpn-reload-restart' or phase.startswith(('vpn-tskey-', 'vpn-oldfinal-', 'vpn-s3-', 'vpn-snapshot-', 'vpn-save-', 'vpn-pending-', 'vpn-policy-', 'vpn-promote-', 'vpn-final-', 'vpn-reorder-')):
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2))
                continue
            if phase.startswith(('vpn-regional-', 'vpn-chain-')):
                report['ordinary_doh_queries'] = len(bootstrap_queries) - ordinary_before
                (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2))
                enabled = phase.endswith('-A') or phase.startswith('vpn-regional-local')
                assert (report['regional_doh_queries'] > 0) == enabled, report
                assert enabled or report['ordinary_doh_queries'] > 0, report
                if phase.startswith('vpn-regional-local'):
                    assert report['ordinary_doh_queries'] == 0, report
                continue
            report['health_endpoint_requests'] = hits
            report['direct_syns_to_failed_origin'] = count
            (fixtures / (phase + '.json')).write_text(json.dumps(report, indent=2))
            assert count == (2 if phase.endswith('-A') or phase == 'vpn-upgrade-confirm' else 1), report
            expected = 'A' if phase.endswith('-A') else 'B'
            assert hits[expected] > 0 and hits['B' if expected == 'A' else 'A'] == 0, report
            if phase == 'vpn-usb-A':
                exercise(serial, fixtures, check_edits=True)
    finally:
        if 'forget_mode' in locals():
            shell('run-as', pkg, 'chmod', forget_mode, 'shared_prefs')
        if fallback_fixture:
            candidate.shutdown()
            candidate.server_close()
        landing.shutdown()
        landing.server_close()
        ordinary.shutdown()
        ordinary.server_close()
        subprocess.run(adb + ['shell', 'iptables', *bootstrap_rule[:2], '-D', *bootstrap_rule[2:]])
        subprocess.run(adb + ['shell', 'rm', '-f', ca_file])
        subprocess.run(adb + ['shell', 'iptables', *relay_rule[:2], '-D', *relay_rule[2:]])
        for cmd in [['iptables','-D','OUTPUT',*match,'-j',chain], ['iptables','-F',chain], ['iptables','-X',chain], ['pkill','-f','^'+remote+'$'], ['rm','-f',remote]]:
            subprocess.run(adb + ['shell'] + cmd, check=False)
        https.shutdown()
        https.server_close()
        for mode, port in [('reverse','18586'),('reverse','18582'),('reverse','18585'),('forward','18584'),('reverse','18581')]:
            subprocess.run(adb + [mode,'--remove','tcp:'+port], check=False)
        origin.terminate()
        proxy.terminate()
        origin.wait()
        proxy.wait()
