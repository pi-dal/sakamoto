#!/usr/bin/env python3
"""Exercise interrupted Apply with one surviving sing-box process and two loopback origins."""
import contextlib
import http.server
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time


@contextlib.contextmanager
def origin(label):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(label.encode())

        def log_message(self, *_):
            pass

    with http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler) as server:
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            yield server.server_port
        finally:
            server.shutdown()


def wait_file(path, processes, timeout=60):
    deadline = time.monotonic() + timeout
    while not path.exists():
        if any(proc.poll() is not None for proc in processes) or time.monotonic() > deadline:
            raise RuntimeError(f'Process stopped or timed out waiting for {path.name}')
        time.sleep(.02)


def run():
    sim = os.environ['SAKAMOTO_TEST_SIMULATOR']
    def container(kind):
        return Path(subprocess.check_output(['xcrun', 'simctl', 'get_app_container', sim, 'com.pidal.sakamoto', kind], text=True).strip())

    exchange = container('group.com.pidal.sakamoto') / 'InterruptedApplyFixture'
    exchange.mkdir(mode=0o700)
    with contextlib.ExitStack() as stack:
        stack.callback(shutil.rmtree, exchange)
        root = Path(stack.enter_context(tempfile.TemporaryDirectory()))
        log = stack.enter_context((root / 'process.log').open('w+'))
        def start(args):
            process = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT)
            def stop():
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=10)
            stack.callback(stop)
            return process
        proxy, direct, takeover = (stack.enter_context(origin(label)) for label in ('proxy', 'direct', 'takeover'))
        contention = os.environ.get('SAKAMOTO_LEASE_CONTENTION', 'none')
        assert contention in ('none', 'failure', 'success', 'double')
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            inbound = sock.getsockname()[1]
        subprocess.run(['xcrun', 'swiftc', '-parse-as-library', 'ios/Sources/SakamotoKit/ApplyLease.swift', 'ios/Scenarios/InterruptedApply/Peer.swift', '-o', root / 'peer'], check=True)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            relay_port = sock.getsockname()[1]
        relay_config = root / 'relay.json'
        relay_config.write_text(json.dumps({'inbounds': [{'type': 'socks', 'listen': '127.0.0.1', 'listen_port': relay_port}], 'outbounds': [{'type': 'direct', 'tag': 'local'}], 'route': {'rules': [{'action': 'route', 'outbound': 'local', 'override_address': '127.0.0.1', 'override_port': proxy}]}}))
        start(['sing-box', 'run', '-c', relay_config])
        peer = start([root / 'peer', exchange])
        docs = container('data') / 'Documents'
        for marker in ('apply-interrupt-seeded', 'apply-interrupt-resume', 'apply-interrupt-second'):
            (docs / marker).unlink(missing_ok=True)
        mode = os.environ.get('SAKAMOTO_APPLY_CASE', 'same')
        if contention == 'double':
            mode = 'cross-double'
        assert mode in ('same', 'cross-confirmed', 'cross-first', 'cross-double')
        (docs / 'arm-apply-interrupt').write_text(mode)
        stack.callback(lambda: (container('data') / 'Documents/arm-apply-interrupt').unlink(missing_ok=True))
        (exchange / 'armed').touch()
        xct = start(['xcodebuild', '-project', 'ios/SakamotoNativeTests.xcodeproj', '-scheme', 'SakamotoNativeTests', '-destination', 'platform=iOS Simulator,id=' + sim, '-derivedDataPath', 'ios/build/mobile-recovery', '-parallel-testing-enabled', 'NO', '-only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testHomeAfterInterruptedApply', '-test-iterations', '4' if contention == 'double' else '3', '-test-repetition-relaunch-enabled', 'YES', '-resultBundlePath', root / 'run.xcresult', 'test'])
        config = root / 'live.json'
        core = None
        observations = {}
        def probe():
            return subprocess.run(['curl', '-fsS', '--max-time', '1', '--noproxy', '', '--socks5', f'127.0.0.1:{inbound}', f'http://127.0.0.1:{direct}/'], capture_output=True, text=True).stdout
        try:
            phases = [('baseline', 'proxy'), ('candidate-1', 'direct')]
            if contention == 'failure':
                phases.append(('competitor-failed', 'direct'))
            phases.append(('takeover', 'takeover') if contention == 'success' else ('restored', 'proxy'))
            phases += [('candidate-2', 'direct'), ('retry-rollback', 'proxy'), ('candidate-3', 'direct')]
            if contention == 'double':
                phases = [('baseline', 'proxy'), ('candidate-1', 'direct'), ('candidate-C', 'takeover'), ('restored', 'proxy')]
            for phase, expected in phases:
                path = exchange / (phase + '.json')
                wait_file(path, [peer, xct])
                payload = json.loads(path.read_text())
                # Probe FINAL specifically. Keep the exported outbound group graph. Substitute a
                # loopback SOCKS relay for the VMess node, and a SOCKS listener for TUN ingress.
                outbounds = []
                for out in payload['outbounds']:
                    if out['type'] in ('selector', 'urltest', 'direct'):
                        outbounds.append(out)
                    else:
                        outbounds.append({'type': 'socks', 'tag': out['tag'], 'server': '127.0.0.1', 'server_port': relay_port})
                routing = {'final': payload['route']['final']}
                if phase in ('takeover', 'candidate-C'):
                    routing['rules'] = [{'action': 'route', 'outbound': 'direct', 'override_address': '127.0.0.1', 'override_port': takeover}]
                config.write_text(json.dumps({'inbounds': [{'type': 'socks', 'listen': '127.0.0.1', 'listen_port': inbound}], 'outbounds': outbounds, 'route': routing}))
                if core is None:
                    core = start(['sing-box', 'run', '-c', config])
                else:
                    core.send_signal(signal.SIGHUP)
                deadline = time.monotonic() + 5
                while probe() != expected:
                    if core.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError(f'Unexpected egress in {phase}')
                    time.sleep(.05)
                if phase == 'retry-rollback':
                    for _ in range(3):
                        time.sleep(.1)
                        assert probe() == 'proxy', 'Retry rollback lost its proxy route'
                observations[phase] = expected
                (exchange / (phase + '.observed')).touch()
                if phase == 'takeover':
                    wait_file(exchange / 'retained-takeover', [peer, xct])
                    assert probe() == 'takeover'
                    observations['after-lease-observer'] = 'takeover'
            wait_file(exchange / 'committed', [xct])
            assert peer.wait(timeout=5) == 0 and core.poll() is None and probe() == ('proxy' if contention == 'double' else 'direct')
            xct.wait(timeout=40)
            summary = json.loads(subprocess.check_output(['xcrun', 'xcresulttool', 'get', 'test-results', 'summary', '--compact', '--path', root / 'run.xcresult']))
            failures = summary.get('testFailures', [])
            log.flush()
            codes = [84, 85] if contention == 'double' else [84]
            log_text = (root / 'process.log').read_text()
            assert failures and all(any(f'exited with code {code}' in f['failureText'] for code in codes) for f in failures), failures
            assert all(log_text.count(f'intentional Apply interruption {code}\n') == 1 for code in codes)
            assert sum('testHomeAfterInterruptedApply' in line and ' passed (' in line for line in (root / 'process.log').read_text().splitlines()) == 2
            evidence = {'profile_case': mode, 'lease_contention': contention, 'same_forwarder_process': True, 'egress': observations}
            if contention == 'double':
                evidence.update(intentional_exit_codes=codes, cold_start_matches_A=True, B_and_C_pending=True)
            else:
                evidence.update(committed_retry=True, intentional_exit_code=84)
            Path(sys.argv[1]).write_text(json.dumps(evidence, indent=2) + '\n')
        except BaseException:
            log.flush()
            print((root / 'process.log').read_text()[-7000:], file=sys.stderr)
            raise


if __name__ == '__main__':
    run()
