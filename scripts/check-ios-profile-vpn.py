#!/usr/bin/env python3
"""Check the iOS profile lifecycle on a disposable, booted simulator."""
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import threading

fixtures = Path(sys.argv[1]).resolve()
sim = os.environ['SAKAMOTO_TEST_SIMULATOR']

def documents():
    path = subprocess.check_output(['xcrun', 'simctl', 'get_app_container', sim, 'com.pidal.sakamoto', 'data'], text=True)
    return Path(path.strip()) / 'Documents'

class Origin(http.server.BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.send_response(204)
        self.end_headers()
    def do_GET(self):
        if self.path == '/missing-subscription':
            self.send_error(404)
            return
        if self.path == '/exit-subscription':
            if (documents() / 'fail-exit-download').exists():
                self.send_error(404)
            else:
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"outbounds":[{"tag":"Seed","type":"socks","server":"127.0.0.1","server_port":18587}]}')
            return
        if self.path != '/slow-subscription':
            return self.do_HEAD()
        docs = documents()
        (docs / 'subscription-started').touch()
        import time
        for _ in range(200):
            if (docs / 'subscription-release').exists():
                break
            time.sleep(.05)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"outbounds":[{"tag":"FromSub","type":"socks","server":"127.0.0.1","server_port":18587}]}')

with http.server.ThreadingHTTPServer(('127.0.0.1', 0), Origin) as origin:
    threading.Thread(target=origin.serve_forever, daemon=True).start()
    config = {'inbounds': [{'type': 'vmess', 'listen': '127.0.0.1', 'listen_port': 18581, 'users': [{'uuid': '11111111-1111-4111-8111-111111111111'}]}], 'outbounds': [{'type': 'direct', 'tag': 'fixture'}], 'route': {'rules': [{'action': 'route', 'outbound': 'fixture', 'override_address': '127.0.0.1', 'override_port': origin.server_port}]}}
    path = fixtures / 'ios-loopback.json'
    path.write_text(json.dumps(config))
    proxy = subprocess.Popen(['sing-box', 'run', '-c', path])
    try:
        if '--home-write' in sys.argv[2:]:
            class DirectOrigin(Origin):
                def do_HEAD(self):
                    self.send_response(202)
                    self.end_headers()

            direct = http.server.ThreadingHTTPServer(('127.0.0.1', 0), DirectOrigin)
            threading.Thread(target=direct.serve_forever, daemon=True).start()
            markers = ['arm-home-write', 'home-write-reference.json', 'home-write-result.json', 'home-direct-port']
            try:
                for marker in markers:
                    (documents() / marker).unlink(missing_ok=True)
                (documents() / markers[0]).touch()
                (documents() / 'home-direct-port').write_text(str(direct.server_port))
                test = 'SakamotoNativeTests/NativeProfileIsolationTests/testHomePreferenceBeforeStartup'
                cmd = ['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-only-testing:' + test, 'test']
                first = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
                assert first.returncode and 'intentional Home interruption 86' in first.stdout, first.stdout[-5000:]
                failures = [line for line in first.stdout.splitlines() if ': error: -[' in line]
                assert not failures, failures
                subprocess.run(cmd, check=True)
                report = json.loads((documents() / markers[2]).read_bytes())
                for scenario, expected in [('widget_confirmed', 202), ('widget_interrupted', 204)]:
                    assert report[scenario] == expected, report
                    assert report[scenario + '_host_reads'] == 0, report
                    assert report[scenario + '_provider_reads'] > 0, report
                assert report['proxy_status'] == 204 and report['confirmed_direct_status'] == 202, report
                (fixtures / markers[2]).write_text(json.dumps(report, indent=2) + '\n')
            finally:
                direct.shutdown()
                direct.server_close()
                for marker in markers:
                    (documents() / marker).unlink(missing_ok=True)
            sys.exit(0)
        if any(flag in sys.argv[2:] for flag in ('--no-id-vpn', '--lost-manifest', '--newer-vpn', '--takeover-vpn', '--reload-takeover-vpn', '--native-replace')):
            if '--native-replace' in sys.argv[2:]:
                prefixes = ['native-replace', 'native-replace-dropped', 'native-replace-restore', 'native-replace-unsent', 'native-replace-retry', 'native-replace-busy', 'native-replace-overlap']
                markers = ['arm-native-replace'] + [prefix + '-reference.json' for prefix in prefixes]
                reports = [prefix + '-' + phase + '.json' for prefix in prefixes for phase in ('live', 'restart')]
                test = 'testCrossProfileNativeStartFailureRestoresRunningProfile'
            elif '--reload-takeover-vpn' in sys.argv[2:]:
                markers = ['arm-newer-vpn', 'reload-takeover-vpn-reference.json']
                reports = ['reload-takeover-vpn-' + phase + '.json' for phase in ('cancel', 'live', 'restart')]
                test = 'testApplyKeepsVPNTakingOverDuringRollback'
            elif '--takeover-vpn' in sys.argv[2:]:
                markers = ['arm-newer-vpn', 'takeover-vpn-reference.json']
                reports = ['takeover-vpn-cancel.json', 'takeover-vpn-live.json', 'takeover-vpn-restart.json']
                test = 'testApplyKeepsVPNTakingOverDuringStart'
            elif '--newer-vpn' in sys.argv[2:]:
                markers = ['arm-newer-vpn', 'newer-vpn-reference.json']
                reports = ['newer-vpn-cancel.json', 'newer-vpn-restart.json']
                test = 'testCancelledApplyKeepsNewerVPNReference'
            elif '--lost-manifest' in sys.argv[2:]:
                markers = ['arm-lost-vpn'] + [f'fixture.applied-manifest-{fault}.json' for fault in ('missing', 'corrupt')]
                reports = [f'lost-vpn-{fault}-{phase}.json' for fault in ('missing', 'corrupt') for phase in ('apply', 'restart')]
                reports += [f'lost-vpn-legacy-{fault}.json' for fault in ('missing', 'corrupt', 'prior-missing', 'prior-corrupt', 'prior-id-only')]
                test = 'testApplyAfterLostManifest'
            else:
                markers = ['arm-no-id-vpn', 'no-id-reference.json']
                reports = ['no-id-apply.json', 'no-id-restart.json']
                test = 'testApplyAfterUnidentifiedLegacyVPN'
            markers += reports
            try:
                for marker in markers:
                    (documents() / marker).unlink(missing_ok=True)
                (documents() / markers[0]).touch()
                subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/' + test, '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', 'test'], check=True)
                for name in reports:
                    report = json.loads((documents() / name).read_bytes())
                    expected = {'extra_writes': 0} if name.endswith('-vpn-cancel.json') else {'explicit_proxy_status': 204}
                    if name.endswith('takeover-vpn-live.json') or (name.startswith('native-replace') and name.endswith('-live.json')):
                        expected['stop_requests'] = 0
                    if name == 'native-replace-unsent-live.json':
                        expected.update(rejected_sends=1, same_instance=1)
                    if name == 'native-replace-overlap-live.json':
                        expected.update(competing_completion=1)
                    if name in ('native-replace-busy-live.json', 'native-replace-overlap-live.json'):
                        expected.update(provider_rejections=1, candidate_rechecked=1, restore_commands=2, starting_snapshots=1, completed_restore_snapshots=1)
                    if name == 'native-replace-retry-live.json':
                        expected.update(rejected_sends=1, candidate_rechecked=1)
                    if name in ('native-replace-restore-live.json', 'native-replace-retry-live.json'):
                        expected.update(restore_commands=1, starting_snapshots=1, completed_restore_snapshots=1)
                    if name == 'native-replace-dropped-live.json':
                        expected['snapshots_checked'] = 2
                    if name == 'no-id-apply.json':
                        expected.update(legacy_preference_writes=0, enabled_after_apply=1)
                    if name.startswith('lost-vpn-legacy-'):
                        expected['preference_writes'] = 0
                    assert report == expected, name
                    (fixtures / name).write_text(json.dumps(report, indent=2) + '\n')
            finally:
                for marker in markers:
                    (documents() / marker).unlink(missing_ok=True)
            sys.exit(0)
        if '--legacy-manual' in sys.argv[2:]:
            legacy = json.loads((fixtures / 'groups.sakamoto').read_bytes())
            del legacy['routeSourceVersion']
            markers = ['manual-old.sakamoto', 'manual-reference.json', 'manual-apply.json', 'manual-restart.json']
            try:
                for marker in markers:
                    (documents() / marker).unlink(missing_ok=True)
                (documents() / markers[0]).write_text(json.dumps(legacy))
                subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testLegacyManualDomainEdit', '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', 'test'], check=True)
                for name in markers[2:]:
                    report = json.loads((documents() / name).read_bytes())
                    assert report == {'explicit_proxy_status': 204}
                    (fixtures / name).write_text(json.dumps(report, indent=2) + '\n')
            finally:
                for marker in markers:
                    (documents() / marker).unlink(missing_ok=True)
            sys.exit(0)
        for name in ['groups.sakamoto', 'legacy-groups.sakamoto', 'socks-groups.sakamoto', 'mixed.sakamoto', 'implicit.sakamoto', 'legacy-sr-only.sakamoto']:
            (documents() / name).write_bytes((fixtures / name).read_bytes())
        for marker in ['subscription-started', 'subscription-release', 'fail-exit-download']:
            (documents() / marker).unlink(missing_ok=True)
        (documents() / 'subscription-url').write_text(f'http://127.0.0.1:{origin.server_port}/slow-subscription')
        (documents() / 'final-route-fixture').touch()
        (documents() / 'profile-restart').unlink(missing_ok=True)
        (documents() / 'profile-repeat').touch()
        assert proxy.poll() is None
        subprocess.run(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-resultBundlePath', str(fixtures / 'lifecycle.xcresult'), 'CODE_SIGNING_ALLOWED=YES', 'CODE_SIGN_IDENTITY=-', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests', '-test-iterations', '2', '-test-repetition-relaunch-enabled', 'YES', 'test'], check=True)
        for phase in ['first', 'restart']:
            hidden_name = 'hidden-draft-' + phase + '.json'
            flags = json.loads((documents() / hidden_name).read_text())
            assert flags == {'url': False, 'domain': False}, flags
            (fixtures / hidden_name).write_text(json.dumps(flags, indent=2) + '\n')
            demand_name = 'on-demand-' + phase + '.json'
            demand = json.loads((documents() / demand_name).read_text())
            assert demand == {'mode': 'domains', 'domains': ['trigger.fixture.example'], 'probeURL': 'http://127.0.0.1/check'}, demand
            (fixtures / demand_name).write_text(json.dumps(demand, indent=2) + '\n')
            shortcut_name = 'shortcut-' + phase + '.json'
            shortcut = json.loads((documents() / shortcut_name).read_text())
            assert shortcut == {fault: 204 for fault in ['read', 'selection', 'start']}, shortcut
            (fixtures / shortcut_name).write_text(json.dumps(shortcut, indent=2) + '\n')
            rejected_name = 'apply-rejected-' + phase + '.json'
            rejected = json.loads((documents() / rejected_name).read_text())
            assert rejected == {'home_proxy_status': 204, 'explicit_retry_confirmed': phase == 'restart'}, rejected
            (fixtures / rejected_name).write_text(json.dumps(rejected, indent=2) + '\n')
            subscription_name = 'subscription-' + phase + '.json'
            subscription = json.loads((documents() / subscription_name).read_text())
            assert subscription == {'same_profile_and_revision': True, 'pending_subscription': True, 'home_proxy_status': 204}, subscription
            (fixtures / subscription_name).write_text(json.dumps(subscription, indent=2) + '\n')
            editor_name = 'editor-' + phase + '.json'
            editor = json.loads((documents() / editor_name).read_text())
            assert editor == {'same_profile_and_revision': True, 'home_proxy_status': 204}, editor
            (fixtures / editor_name).write_text(json.dumps(editor, indent=2) + '\n')
            exit_name = 'exit-failure-' + phase + '.json'
            exit_report = json.loads((documents() / exit_name).read_text())
            assert exit_report == {'proxy_status': 204, 'source_and_revision_match': True}, exit_report
            (fixtures / exit_name).write_text(json.dumps(exit_report, indent=2) + '\n')
            chain_name = 'implicit-' + phase + '.json'
            chain = json.loads((documents() / chain_name).read_text())
            for stage in (['restart'] if phase == 'restart' else ['import', 'generate1', 'generate2', 'apply']):
                assert chain[stage] == [['Allowed'], ['Allowed', 'Excluded'], ['MainProxy']], chain
            (fixtures / chain_name).write_text(json.dumps(chain, indent=2) + '\n')
            mixed_name = 'mixed-' + phase + '.json'
            mixed = json.loads((documents() / mixed_name).read_text())
            for stage in (['restart'] if phase == 'restart' else ['import', 'generate1', 'generate2', 'apply']):
                assert mixed[stage] == ['Imported', 'Allowed', 'firefox'], mixed
            if phase == 'first':
                assert mixed['cancel'] == ['revision and manifest preserved'], mixed
            (fixtures / mixed_name).write_text(json.dumps(mixed, indent=2) + '\n')
            tolerance_name = 'tolerance-' + phase + '.json'
            tolerance = json.loads((documents() / tolerance_name).read_text())
            expected = ['restart'] if phase == 'restart' else ['import', 'generate1', 'generate2']
            assert tolerance == {stage: 250 for stage in expected}, tolerance
            (fixtures / tolerance_name).write_text(json.dumps(tolerance, indent=2) + '\n')
            socks_name = 'socks-' + phase + '.json'
            socks_report = json.loads((documents() / socks_name).read_text())
            stages = ['restart'] if phase == 'restart' else ['import', 'generate1', 'generate2']
            assert socks_report == {stage: ['Allowed'] for stage in stages}, socks_report
            (fixtures / socks_name).write_text(json.dumps(socks_report, indent=2) + '\n')
            name = 'final-' + ('apply' if phase == 'first' else phase) + '.json'
            report = json.loads((documents() / name).read_text())
            assert report == {'mode': 'auto', 'fresh_process': phase == 'restart', 'proxy_status': 204, 'route_final': 'MainProxy'}, report
            (fixtures / name).write_text(json.dumps(report, indent=2) + '\n')
    finally:
        proxy.terminate()
        proxy.wait()
        (documents() / 'final-route-fixture').unlink(missing_ok=True)
        (documents() / 'profile-repeat').unlink(missing_ok=True)
