#!/usr/bin/env python3
"""Run the stale-draft recovery regression on a disposable simulator with groups.sakamoto installed."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
import json
import os
import subprocess
import sys

sim = os.environ['SAKAMOTO_TEST_SIMULATOR']
result = Path(sys.argv[1]).resolve()
requests = []


class Feed(BaseHTTPRequestHandler):
    def do_GET(self):
        requests.append(self.path)
        if self.path != '/adopted.json':
            self.send_error(404)
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"outbounds":[{"tag":"FromAdoptedFeed","type":"socks","server":"127.0.0.1","server_port":18587}]}')

    def log_message(self, *args):
        pass


def documents():
    path = subprocess.check_output(['xcrun', 'simctl', 'get_app_container', sim, 'com.pidal.sakamoto', 'data'], text=True)
    return Path(path.strip()) / 'Documents'


with ThreadingHTTPServer(('127.0.0.1', 0), Feed) as server:
    Thread(target=server.serve_forever, daemon=True).start()
    (documents() / 'fixture.icloud-stale-draft' / 'restart').unlink(missing_ok=True)
    (documents() / 'legacy-drafts-relaunch').unlink(missing_ok=True)
    endpoint = 'draft-fixture-endpoint'
    (documents() / endpoint).write_text(f'http://127.0.0.1:{server.server_port}')
    try:
        subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', f'platform=iOS Simulator,id={sim}', '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudAdoptedSourcesSurviveStaleDraft', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testInstalledProfileMigratesLegacyPreferences', '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', '-resultBundlePath', str(result), 'test'], check=True)
        assert requests and set(requests) == {'/adopted.json'}, 'Unexpected subscription requests'
        print(json.dumps({'adopted_feed_requests': len(requests), 'retired_feed_requests': 0}))
    finally:
        server.shutdown()
        (documents() / endpoint).unlink(missing_ok=True)
