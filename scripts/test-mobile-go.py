#!/usr/bin/env python3
"""Check compiler-policy isolation and Go's order-sensitive flag forwarding."""
import importlib.util
import json
import pathlib
import tempfile
import unittest

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location('mobile_go', SCRIPT_DIR / 'mobile-go.py')
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class MobileCompilerPolicyTests(unittest.TestCase):
    def test_host_and_discovery_commands_do_not_read_the_mobile_policy(self):
        for args in [['env', 'GOROOT'], ['version'], ['test', './...'], ['build', './cmd/sakamoto']]:
            self.assertEqual(MODULE.archive_arguments(args, '/not/a/profile'), args)

    def test_archive_receives_the_complete_policy_in_override_order(self):
        profile = SCRIPT_DIR / 'go-size-profile.json'
        flags = json.loads(profile.read_text())['gcflags']
        args = ['build', '-buildmode=c-archive', '-tags=ios', '-o', 'core.a', '.']
        actual = MODULE.archive_arguments(args, profile)
        self.assertEqual(actual, ['build', *['-gcflags=' + f for f in flags], *args[1:]])
        self.assertLess(flags.index('all=-l'), flags.index('std='))
        for package in ['sing-box/route', 'sing-box/protocol', 'sing-tun', 'quic-go', 'tailscale', 'wireguard-go']:
            self.assertIn('github.com/sagernet/' + package + '/...=', flags)

    def test_android_shared_build_preserves_linker_and_feature_flags(self):
        profile = SCRIPT_DIR / 'go-size-profile.json'
        flags = json.loads(profile.read_text())['gcflags']
        args = ['build', '-buildmode=c-shared', '-tags=with_quic,with_wireguard', '-ldflags=-s -w', '-o', 'libbox.so', '.']
        self.assertEqual(MODULE.archive_arguments(args, profile), ['build', *['-gcflags=' + f for f in flags], *args[1:]])

    def test_invalid_archive_policy_fails_instead_of_silently_changing_flags(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'profile.json'
            for value in [{'schema': 2, 'gcflags': []}, {'schema': 1, 'gcflags': ['-l']}, {'schema': 1, 'gcflags': [None]}]:
                path.write_text(json.dumps(value))
                with self.assertRaises(ValueError):
                    MODULE.archive_arguments(['build', '-buildmode=c-archive', '.'], path)


if __name__ == '__main__':
    unittest.main()
