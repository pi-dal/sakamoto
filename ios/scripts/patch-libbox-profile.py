#!/usr/bin/env python3
"""Use the supported Go profiling API in the private iOS libbox build."""
from pathlib import Path
import sys


ORIGINAL = '''func writeOOMProfile(filePath string, name string) {
	err := oomprofile.WriteFile(filePath, name)
	if err != nil {
		return
	}
	chownReport(filePath)
}'''
REPLACEMENT = '''func writeOOMProfile(filePath string, name string) {
	// Go 1.27 changed the layout of its private heap profile records.
	// The public pprof API uses the layout of the runtime we build with.
	profile := pprof.Lookup(name)
	if profile == nil {
		return
	}
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	writeErr := profile.WriteTo(file, 0)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(filePath)
		return
	}
	chownReport(filePath)
}'''


def patch(root: Path) -> None:
    report = root / "experimental/libbox/oom_report.go"
    source = report.read_text()
    if REPLACEMENT in source:
        return
    private_import = '\t"github.com/sagernet/sing-box/experimental/libbox/internal/oomprofile"\n'
    imports = '\t"path/filepath"\n'
    if any(source.count(anchor) != 1 for anchor in (ORIGINAL, private_import, imports)):
        raise RuntimeError("Upstream OOM profiler changed. Review the iOS profiling patch.")
    source = source.replace(private_import, "").replace(imports, '\t"os"\n' + imports + '\t"runtime/pprof"\n')
    report.write_text(source.replace(ORIGINAL, REPLACEMENT))


if __name__ == "__main__":
    patch(Path(sys.argv[1]))
