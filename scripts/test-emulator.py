#!/usr/bin/env python3
"""Run Go tests, or a supplied command, against a disposable Firestore emulator."""
import argparse
import hashlib
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
VERSION = "1.20.2"
SHA256 = "4a117fc297b1441eac1b7756e80442e86ef88865b9e3caf6f59eabf83da574f8"
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--cwd", type=Path, default=ROOT)
parser.add_argument("command", nargs=argparse.REMAINDER)
args = parser.parse_args()
command = args.command[1:] if args.command[:1] == ["--"] else args.command
command = command or ["go", "test", "-race", "-count=1", "./..."]
jar = ROOT / ".build" / "tools" / f"cloud-firestore-emulator-v{VERSION}.jar"
jar.parent.mkdir(parents=True, exist_ok=True)
if not jar.exists():
    cached = Path.home() / ".cache" / "firebase" / "emulators" / jar.name
    data = cached.read_bytes() if cached.exists() else urllib.request.urlopen(
        f"https://storage.googleapis.com/firebase-preview-drop/emulator/{jar.name}", timeout=60
    ).read()
    if hashlib.sha256(data).hexdigest() != SHA256:
        raise SystemExit("Emulator checksum mismatch")
    jar.write_bytes(data)
if hashlib.sha256(jar.read_bytes()).hexdigest() != SHA256:
    raise SystemExit("Emulator checksum mismatch")
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    port = listener.getsockname()[1]
with tempfile.TemporaryFile() as log:
    process = subprocess.Popen(
        ["java", "-jar", str(jar), "--host=127.0.0.1", f"--port={port}"],
        cwd=ROOT, stdout=log, stderr=subprocess.STDOUT,
    )
    try:
        deadline = time.monotonic() + 30
        while True:
            if process.poll() is not None or time.monotonic() > deadline:
                log.seek(0)
                raise SystemExit(log.read().decode(errors="replace"))
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.2)
        result = subprocess.run(
            command, cwd=args.cwd,
            env={**os.environ, "FIRESTORE_EMULATOR_HOST": f"127.0.0.1:{port}"},
        )
        raise SystemExit(result.returncode)
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
