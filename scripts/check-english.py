#!/usr/bin/env python3
"""Fail CI when repository text introduces Chinese characters or punctuation.

Checks tracked and new non-ignored files so local work is covered before a
commit. Third-party downloads and user runtime state are not part of this repo.
"""

from pathlib import Path
import re
import subprocess
import sys

NON_ENGLISH = re.compile(
    r"[\u3400-\u9fff\uf900-\ufaff\U00020000-\U0003134f"
    r"\u3001\u3002\u3008-\u3011\uff01-\uff0f\uff1a-\uff20]"
)


def main() -> int:
    names = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"]
    ).split(b"\0")
    violations: list[str] = []
    for raw_name in names:
        if not raw_name:
            continue
        path = Path(raw_name.decode("utf-8"))
        if not path.is_file():
            continue
        data = path.read_bytes()
        if b"\0" in data[:8192]:  # Ignore binary inputs, not embedded UTF-8 assets.
            continue
        try:
            lines = data.decode("utf-8").splitlines()
        except UnicodeDecodeError:
            continue
        for number, line in enumerate(lines, 1):
            if NON_ENGLISH.search(line):
                violations.append(f"{path}:{number}")
    if violations:
        print("Chinese text found in repository files:\n" + "\n".join(violations), file=sys.stderr)
        return 1
    print("English-only repository check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
