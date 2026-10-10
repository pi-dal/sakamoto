#!/usr/bin/env python3
"""Apply the ordered size policy only to gomobile's archive/shared builds.

Go's last matching per-package flag wins. The policy preserves inlining in
standard-library and packet-processing packages. Other Go commands, including
SDK discovery and binding generation, use their ordinary compiler settings.
"""
import json
import os
import pathlib
import sys


def archive_arguments(args, profile):
    if not args or args[0] != 'build' or not any(
        mode in args for mode in ('-buildmode=c-archive', '-buildmode=c-shared')
    ):
        return args
    policy = json.loads(pathlib.Path(profile).read_text())
    if policy.get('schema') != 1 or not isinstance(policy.get('gcflags'), list):
        raise ValueError('Unsupported mobile compiler policy')
    flags = policy['gcflags']
    if not all(isinstance(flag, str) and '=' in flag for flag in flags):
        raise ValueError('Invalid mobile compiler flag')
    return [args[0], *('-gcflags=' + flag for flag in flags), *args[1:]]


def main():
    real_go = os.environ['SAKAMOTO_MOBILE_REAL_GO']
    args = archive_arguments(sys.argv[1:], os.environ['SAKAMOTO_MOBILE_SIZE_PROFILE'])
    os.execv(real_go, [real_go, *args])


if __name__ == '__main__':
    main()
