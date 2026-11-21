#!/usr/bin/env python3
"""Check DNS from the three configurations captured by the iOS Apply/relaunch test."""
import argparse
import contextlib
import hashlib
import http.server
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import tempfile
import threading
import time

parser = argparse.ArgumentParser()
parser.add_argument('package', type=Path)
parser.add_argument('report', type=Path)
parser.add_argument('--legacy-local', action='store_true')
args = parser.parse_args()
sim = os.environ['SAKAMOTO_TEST_SIMULATOR']
name = b'\x08regional\x07fixture\x07example\x00'
question = name + b'\x00\x01\x00\x01'
answer = bytes.fromhex('cb007122')
queries = []


def docs():
    path = subprocess.check_output(['xcrun', 'simctl', 'get_app_container', sim, 'com.pidal.sakamoto', 'data'], text=True)
    return Path(path.strip()) / 'Documents'


class Resolver(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        packet = self.rfile.read(int(self.headers['Content-Length']))
        if self.path != '/dns-query' or packet[12:12 + len(question)] != question:
            self.send_error(400)
            return
        queries.append(packet[:2])
        body = packet[:2] + bytes.fromhex('81800001000100000000') + question + bytes.fromhex('c00c00010001000000000004') + answer
        self.send_response(200)
        self.send_header('Content-Type', 'application/dns-message')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


@contextlib.contextmanager
def core(config, root, label):
    path = root / (label + '.json')
    path.write_text(json.dumps(config))
    with (root / (label + '.log')).open('w+') as log:
        process = subprocess.Popen(['sing-box', 'run', '-c', str(path)], cwd=root, stdout=log, stderr=log)
        try:
            yield process
        finally:
            process.terminate()
            process.wait(timeout=5)
            if process.returncode not in (0, -15):
                log.seek(0)
                raise RuntimeError(log.read()[-1500:])


def fixture_config(payload, pem):
    dns = next(s for s in payload['dns']['servers'] if s['tag'] == 'regional-protected-doh')
    assert (dns['server'], dns['detour'], dns['tls']['server_name']) == ('192.0.2.53', 'Excluded', 'allowed.fixture.example')
    relay = next(s for s in payload['outbounds'] if s['tag'] == dns['detour'])
    assert (relay['server'], relay['server_port'], relay['tls']['server_name']) == ('192.0.2.30', 18582, 'allowed.fixture.example')
    # Map the documentation-only relay to the loopback fixture. Keep the DoH address and detour.
    relay['server'] = '127.0.0.1'
    for tls in [relay['tls'], dns['tls']]:
        tls['certificate'] = pem
    payload['inbounds'] = [dict(type='direct', tag='dns-fixture', listen='127.0.0.1', listen_port=19853, network='udp')]
    payload['route']['rules'].insert(0, dict(inbound='dns-fixture', action='hijack-dns'))
    payload.pop('services', None)
    return payload


stages = ['local', 'restart'] if args.legacy_local else ['generate1', 'generate2', 'restart']
markers = ['regional-ios.sakamoto', 'regional-reference.json', *[f'regional-{s}.json' for s in stages]]
report = {'package_sha256': hashlib.sha256(args.package.read_bytes()).hexdigest()}
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    for marker in markers:
        (docs() / marker).unlink(missing_ok=True)
    package = args.package.read_bytes()
    if args.legacy_local:
        legacy = json.loads(package)
        del legacy['routeSourceVersion'], legacy['regionalDNS']
        package = json.dumps(legacy).encode()
    (docs() / markers[0]).write_bytes(package)
    report['installed_package_sha256'] = hashlib.sha256(package).hexdigest()
    try:
        subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testRegionalDNSGeneratedProfile', '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', 'test'], check=True)
        cert, key = root / 'cert.pem', root / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=allowed.fixture.example', '-addext', 'subjectAltName=DNS:allowed.fixture.example', '-keyout', key, '-out', cert], check=True, capture_output=True)
        with http.server.ThreadingHTTPServer(('127.0.0.1', 0), Resolver) as doh:
            tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            tls.load_cert_chain(cert, key)
            doh.socket = tls.wrap_socket(doh.socket, server_side=True)
            threading.Thread(target=doh.serve_forever, daemon=True).start()
            relay = {'inbounds': [dict(type='vless', listen='127.0.0.1', listen_port=18582, users=[dict(uuid='11111111-1111-4111-8111-111111111111')], tls=dict(enabled=True, certificate_path=str(cert), key_path=str(key)))], 'outbounds': [dict(type='direct', tag='local')], 'route': {'rules': [dict(ip_cidr='192.0.2.53/32', port=443, action='route', outbound='local', override_address='127.0.0.1', override_port=doh.server_port), dict(action='reject')]}}
            with core(relay, root, 'relay'):
                for stage in stages:
                    before = len(queries)
                    payload = json.loads((docs() / f'regional-{stage}.json').read_bytes())
                    with core(fixture_config(payload, cert.read_text()), root, stage) as process, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                        sock.settimeout(.5)
                        deadline = time.monotonic() + 12
                        while time.monotonic() < deadline and process.poll() is None:
                            sock.sendto(bytes.fromhex('123401000001000000000000') + question, ('127.0.0.1', 19853))
                            try:
                                response, _ = sock.recvfrom(2048)
                                break
                            except TimeoutError:
                                continue
                        else:
                            raise RuntimeError(f'{stage} DNS query timed out')
                        assert response[:2] == b'\x12\x34' and response[6:8] == b'\x00\x01' and response.endswith(answer), (stage, response.hex())
                        assert len(queries) > before
                        report[stage] = dict(answer=socket.inet_ntoa(answer), regional_doh_requests=len(queries)-before, relay='Excluded')
            doh.shutdown()
    finally:
        for marker in markers:
            (docs() / marker).unlink(missing_ok=True)
args.report.write_text(json.dumps(report, indent=2) + '\n')
