#!/usr/bin/env python3
"""Apply checked network recovery fixes to a private libbox v1.14.2 copy."""
from pathlib import Path
import sys


def replace_once(text, old, new, label):
    if new in text:
        return text
    if text.count(old) != 1:
        raise RuntimeError(f"Upstream {label} drift: expected one anchor")
    return text.replace(old, new)


def patch(root):
    server = root / "experimental/libbox/command_server.go"
    source = server.read_text()
    source = replace_once(source, '''	if !C.IsIos {
		instance.PauseManager().DeviceWake()
	}
''', '''	// Resume immediately on an NE wake; the minute timer is only a fallback
	// for systems that never send a wake callback.
	if s.endPauseTimer != nil {
		s.endPauseTimer.Stop()
	}
	instance.PauseManager().DeviceWake()
''', "device wake")
    monitor = root / "experimental/libbox/monitor.go"
    network = monitor.read_text()
    network = replace_once(network, 'import (\n', 'import (\n\t"slices"\n', "monitor imports")
    network = replace_once(network,
        'oldInterface.Name == m.defaultInterface.Name && oldInterface.Index == m.defaultInterface.Index {',
        '''oldInterface.Name == m.defaultInterface.Name && oldInterface.Index == m.defaultInterface.Index &&
		oldInterface.MTU == m.defaultInterface.MTU && oldInterface.Flags == m.defaultInterface.Flags &&
		slices.Equal(oldInterface.Addresses, m.defaultInterface.Addresses) {''', "interface address update")
    # Validate both anchors before writing either file. Never edit GOMODCACHE.
    server.write_text(source)
    monitor.write_text(network)


if __name__ == "__main__":
    patch(Path(sys.argv[1]))
