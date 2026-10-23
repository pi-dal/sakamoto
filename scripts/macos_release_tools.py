"""Bounded retries for Apple's ticket-delivery transport failures."""
import subprocess
import sys
import time


def stapler(action, app):
    if action not in ("staple", "validate"):
        raise ValueError("Unsupported stapler action")
    command = ["xcrun", "stapler", action, str(app)]
    for attempt in range(3):
        try:
            result = subprocess.run(command, capture_output=True, text=True, timeout=60)
        except subprocess.TimeoutExpired:
            if attempt == 2:
                raise
            print(f"Stapler {action} timed out; retrying ticket delivery", file=sys.stderr)
        else:
            print(result.stdout, end="")
            print(result.stderr, file=sys.stderr, end="")
            if result.returncode == 0:
                return result.stdout
            # 65 also covers accepted tickets that have not propagated yet;
            # 68 includes CloudKit network disconnects. Invalid tickets still
            # fail closed after the same bounded attempts.
            if result.returncode not in (65, 68) or attempt == 2:
                raise subprocess.CalledProcessError(result.returncode, command, output=result.stdout, stderr=result.stderr)
            print(f"Stapler {action} failed; retrying ticket delivery", file=sys.stderr)
        time.sleep(2 ** (attempt + 1))
