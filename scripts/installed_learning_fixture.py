"""Install the pre-scope B layout, including a global transaction left by A, on the fixture emulator."""
import json
import subprocess
import xml.etree.ElementTree as ET
from zipfile import ZipFile


def stage_legacy_b(adb, pkg, package, fixtures):
    def write(path, data):
        subprocess.run([*adb, 'shell', f"run-as {pkg} sh -c 'cat >{path}'"], input=data, check=True)

    subprocess.run([*adb, 'shell', 'am', 'force-stop', pkg], check=True)
    with ZipFile(package) as z:
        manifest = z.read('sakamoto-config.json')
        state = json.loads(manifest)
        assert not state.get('scopedExperiment') and not state.get('modeCacheID')
        export_mode = json.loads(state['hostMetadata'])['experiment']['mode']
        assert export_mode in ('auto', 'off', 'on')
        device_mode = 'auto' if export_mode == 'off' else 'off'
        if export_mode == 'on':
            # be44dfc saveSettings(off) edited only the active core and global prefs.
            core = json.loads(state['generatedContent'])
            assert core['route']['final'] == 'MainProxy'
            core['route']['final'] = 'direct'
            state['generatedContent'] = json.dumps(core)
            manifest = json.dumps(state).encode()
        subprocess.run([*adb, 'shell', 'run-as', pkg, 'mkdir', '-p', 'files/rules', 'shared_prefs'], check=True)
        for path in z.namelist():
            if not path.startswith('rules/') or not (path.endswith('.srs') or path == 'rules/auto-proxy-rules.json'):
                continue
            assert path == 'rules/auto-proxy-rules.json' or (len(path.split('/')) == 2 and path.split('/')[1][:-4].isalnum())
            write('files/' + path, z.read(path))
    # Make recovery actionable if the new service reads the wrong journal.
    with ZipFile(fixtures / 'A.zip') as z:
        before = json.loads(z.read('sakamoto-config.json'))['generatedContent']
    prefs = ET.Element('map')
    for key, value in dict(settings=json.dumps({'mode': device_mode, 'threshold': 1, 'fallback': False}), learned='["old-device.fixture.example"]',
                           history='[{"domain":"old-device.fixture.example"}]', pendingBefore=before,
                           pendingCandidate=state['generatedContent']).items():
        ET.SubElement(prefs, 'string', name=key).text = value
    write('shared_prefs/experiments.xml', ET.tostring(prefs, encoding='utf-8'))
    write('files/sakamoto-config.json', manifest)
