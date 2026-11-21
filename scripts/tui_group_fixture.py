"""Drive the built desktop TUI against the disposable two-node export fixture."""
import json
import subprocess
import time
import uuid

import yaml


def save_socks_group(repo, sidecar, receipt):
    binary = sidecar.parent / 'sakamoto-fixture'
    subprocess.run(['go', 'build', '-o', binary, './cmd/sakamoto'], cwd=repo, check=True)
    subprocess.run(['go', 'run', './tools/main.go', str(sidecar)], cwd=repo, check=True, capture_output=True)
    tmux = ['tmux', '-L', 'sakamoto-' + uuid.uuid4().hex, '-f', '/dev/null']

    def screen():
        return subprocess.check_output([*tmux, 'capture-pane', '-p'], text=True)

    def wait_for(value):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if value in screen():
                return
            time.sleep(.05)
        raise AssertionError(f'TUI did not display {value!r}\n{screen()}')

    def keys(*items):
        for item in items:
            subprocess.run([*tmux, 'send-keys', item], check=True)
            time.sleep(.12)

    subprocess.run([*tmux, 'new-session', '-d', '-x', '120', '-y', '100', str(binary), '--config', str(sidecar)], check=True)
    try:
        wait_for('Home')
        keys('2', 'e')
        wait_for('Latency tolerance')
        lines = screen().splitlines()
        y, line = next((y, line) for y, line in enumerate(lines, 1) if 'Latency tolerance' in line)
        x = line.index('Latency tolerance') + 1
        click = f'\x1b[<0;{x};{y}M\x1b[<0;{x};{y}m'.encode()
        subprocess.run([*tmux, 'send-keys', '-H', *(f'{b:02x}' for b in click)], check=True)
        wait_for('100▏')
        keys('BSpace', 'BSpace', 'BSpace', '250', 'Enter', 'C-s')
        assert yaml.safe_load(sidecar.read_text())['urltest']['tolerance'] == 250
        keys('p', 'j', 'Enter')
        wait_for('exclusions survive regeneration')
        # Exclude the VLESS row. On older builds it is the sole displayed row.
        if 'Allowed' in screen():
            keys('j')
        keys('Enter', 'C-s')
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            current = screen()
            if 'keep at least one available member' in current:
                raise AssertionError('TUI rejected the valid SOCKS-only selection')
            saved = yaml.safe_load(sidecar.read_text()).get('automatic_group_exclusions', {})
            if saved.get('OthersAuto') == ['Excluded']:
                receipt.write_text(json.dumps({'entry': 'Config / Proxy groups / OthersAuto', 'save': 'Ctrl-S', 'excluded': saved['OthersAuto'], 'tolerance_ms': 250}, indent=2) + '\n')
                return
            time.sleep(.05)
        raise AssertionError('The desktop TUI did not persist the selected exclusion')
    finally:
        subprocess.run([*tmux, 'kill-server'], check=False, capture_output=True)
