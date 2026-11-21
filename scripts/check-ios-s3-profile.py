#!/usr/bin/env python3
"""Run iOS settings sync against a loopback SigV4 endpoint on a disposable simulator."""
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import threading
import time

from s3_test_endpoint import SignedFixtureEndpoint

fixtures = Path(sys.argv[1]).resolve()
sim = os.environ['SAKAMOTO_TEST_SIMULATOR']
ctl = ['xcrun', 'simctl']
app = next(Path('ios/build/mobile-recovery/Build/Products/Debug-iphonesimulator').glob('*.app'))
subprocess.run([*ctl, 'install', sim, app], check=True)


def documents():
    container = subprocess.check_output([*ctl, 'get_app_container', sim, 'com.pidal.sakamoto', 'data'], text=True)
    return Path(container.strip()) / 'Documents'


docs = documents()
docs.mkdir(exist_ok=True)
objects = {}
receipts = {}


class Endpoint(SignedFixtureEndpoint):
    def do_GET(self):
        if not self.authentic(b''):
            return
        if self.path not in objects:
            self.send_error(404)
            return
        prefix = self.path.split('/')[2]
        docs = documents()
        if (docs / 's3-restart').exists():
            assert prefix == 'control'
            receipts['cold_start_signed_get'] = True
        elif (docs / 's3-after-rejection').exists():
            assert prefix == 'control'
            receipts['same_session_signed_get'] = True
        else:
            (docs / ('s3-waiting-' + prefix)).touch()
            for _ in range(400):
                if (docs / ('s3-release-' + prefix)).exists():
                    break
                time.sleep(.05)
        self.send_response(200)
        self.send_header('ETag', '"fixture"')
        self.end_headers()
        try:
            self.wfile.write(objects[self.path])
        except (BrokenPipeError, ConnectionResetError, ssl.SSLEOFError):
            pass

    def do_PUT(self):
        body = self.rfile.read(int(self.headers['Content-Length']))
        if not self.authentic(body):
            return
        prefix = self.path.split('/')[2]
        digest = hashlib.sha256(body).hexdigest()
        if self.path in objects:
            assert self.headers['If-Match'] == '"fixture"'
            receipts[prefix]['update_puts'] += 1
        else:
            assert self.headers['If-None-Match'] == '*'
            receipts[prefix] = {'before': digest, 'update_puts': 0}
        receipts[prefix].update(after=digest, marker_received=('pending-' + prefix + '-fixture').encode() in body)
        objects[self.path] = body
        self.send_response(200)
        self.end_headers()

    def log_message(self, *args):
        pass


with tempfile.TemporaryDirectory() as tmp, http.server.ThreadingHTTPServer(('127.0.0.1', 0), Endpoint) as server:
    cert, key = Path(tmp) / 'fixture.pem', Path(tmp) / 'fixture.key'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=S3 loopback fixture', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', key, '-out', cert], check=True, capture_output=True)
    subprocess.run([*ctl, 'keychain', sim, 'add-root-cert', cert], check=True)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.authenticated = []
    threading.Thread(target=server.serve_forever, daemon=True).start()
    for name in ['groups.sakamoto', 'mixed.sakamoto']:
        shutil.copyfile(fixtures / name, docs / name)
    cases = ['cancel', 'switch', 'edit', 'control']
    markers = [f's3-{step}-{case}' for step in ['waiting', 'release'] for case in cases]
    for marker in ['s3-after-rejection', 's3-restart', *markers]:
        (docs / marker).unlink(missing_ok=True)
    (docs / 's3-endpoint').write_text(f'https://127.0.0.1:{server.server_port}')
    try:
        subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-resultBundlePath', str(fixtures / ('s3-' + str(time.time_ns()) + '.xcresult')), '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testS3SettingsLifecycle', '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', 'test-without-building'], check=True)
        for prefix in cases:
            row = receipts[prefix]
            assert row['update_puts'] == int(prefix == 'control') and row['marker_received'] == (prefix == 'control') and (row['before'] == row['after']) == (prefix != 'control'), row
        assert receipts['same_session_signed_get'] and receipts['cold_start_signed_get']
        (fixtures / 'ios-s3-settings.json').write_text(json.dumps(receipts, indent=2) + '\n')
    finally:
        (documents() / 's3-endpoint').unlink(missing_ok=True)
        server.shutdown()
