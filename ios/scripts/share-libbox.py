#!/usr/bin/env python3
"""Convert combined gomobile archives to one shareable dynamic framework."""
import argparse
import pathlib
import plistlib
import subprocess
import tempfile


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def convert(root):
    info = plistlib.loads((root / 'Info.plist').read_bytes())
    for library in info['AvailableLibraries']:
        identifier = library['LibraryIdentifier']
        framework = root / identifier / library['LibraryPath']
        binary = framework / 'Libbox'
        sdk = 'iphonesimulator' if library.get('SupportedPlatformVariant') == 'simulator' else 'iphoneos'
        sdk_path = run('xcrun', '--sdk', sdk, '--show-sdk-path')
        symbols = set()
        for line in run('xcrun', 'nm', '-gU', str(binary)).splitlines():
            parts = line.split()
            if parts and parts[-1].startswith(('_Libbox', '_Mobilecore', '_Mobilegen', '_Mobileexperiment', '_Universe', '_Go', '_go_seq_', '_OBJC_CLASS_$_', '_OBJC_METACLASS_$_')):
                symbols.add(parts[-1])
        if len(symbols) < 100:
            raise RuntimeError('Missing combined public binding symbols')
        with tempfile.TemporaryDirectory(prefix='sakamoto-dynamic-') as temp:
            work = pathlib.Path(temp)
            exports = work / 'exports.txt'
            exports.write_text('\n'.join(sorted(symbols)) + '\n')
            slices = []
            for arch in library['SupportedArchitectures']:
                target = f'{arch}-apple-ios16.0' + ('-simulator' if sdk == 'iphonesimulator' else '')
                output = work / arch
                command = ['xcrun', '--sdk', sdk, 'clang', '-target', target, '-isysroot', sdk_path,
                           '-dynamiclib', '-Wl,-all_load', str(binary),
                           '-Wl,-install_name,@rpath/Libbox.framework/Libbox',
                           '-Wl,-exported_symbols_list,' + str(exports), '-Wl,-dead_strip', '-Wl,-application_extension']
                for name in ['Foundation', 'UIKit', 'Security', 'CFNetwork', 'CoreTelephony', 'Network', 'SystemConfiguration']:
                    command.extend(['-framework', name])
                command.extend(['-lresolv', '-lc++', '-o', str(output)])
                subprocess.run(command, check=True)
                subprocess.run(['xcrun', 'strip', '-S', '-x', str(output)], check=True)
                slices.append(str(output))
            output = work / 'Libbox'
            subprocess.run(['xcrun', 'lipo', '-create', *slices, '-output', str(output)], check=True)
            binary.write_bytes(output.read_bytes())
            binary.chmod(0o755)
        metadata_path = framework / 'Info.plist'
        metadata = plistlib.loads(metadata_path.read_bytes())
        metadata.update(CFBundleExecutable='Libbox', CFBundlePackageType='FMWK',
                        CFBundleInfoDictionaryVersion='6.0', CFBundleVersion='1',
                        CFBundleSupportedPlatforms=['iPhoneSimulator' if sdk == 'iphonesimulator' else 'iPhoneOS'],
                        MinimumOSVersion='16.0')
        metadata_path.write_bytes(plistlib.dumps(metadata, fmt=plistlib.FMT_BINARY))
        # An empty desktop Resources directory makes CFBundle ignore the iOS
        # root Info.plist, causing MobileInstallation to reject the framework.
        resources = framework / 'Resources'
        if resources.is_dir() and not any(resources.iterdir()):
            resources.rmdir()
        print(f'{identifier}: dynamic Libbox, {binary.stat().st_size} bytes')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('xcframework', type=pathlib.Path)
    convert(parser.parse_args().xcframework)
