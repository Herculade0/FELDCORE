#!/usr/bin/env python3
"""Read a QEMU serial socket until the installer is ready, then optionally type one command."""

import socket
import sys
import time


def main() -> None:
    if len(sys.argv) < 4:
        sys.exit("usage: iso-serial.py SOCKET SECONDS MARKER [COMMAND]")
    path, seconds, marker = sys.argv[1], float(sys.argv[2]), sys.argv[3]
    command = sys.argv[4] if len(sys.argv) > 4 else ""
    deadline = time.time() + seconds
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    while True:
        try:
            sock.connect(path)
            break
        except (FileNotFoundError, ConnectionRefusedError):
            if time.time() > deadline:
                sys.exit("iso-serial: qemu socket did not open")
            time.sleep(0.2)
    sock.settimeout(1)
    buf = b""
    sent = False
    marker_b = marker.encode()
    done = b"FELDCORE_INSTALL_DONE"
    while time.time() < deadline:
        try:
            chunk = sock.recv(65536)
        except socket.timeout:
            chunk = b""
        except ConnectionError:
            break
        if chunk:
            buf += chunk
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()
        if command and not sent and marker_b in buf:
            time.sleep(0.8)
            sock.sendall(command.encode() + b"\n")
            sent = True
        if command and sent and done in buf:
            return
        if not command and marker_b in buf:
            return
    sys.exit("iso-serial: timed out waiting for the installer")


if __name__ == "__main__":
    main()
