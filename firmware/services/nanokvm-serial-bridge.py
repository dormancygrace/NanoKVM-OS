#!/usr/bin/env python3
"""Opt-in NanoKVM window-size transport; run on the USB receiving Linux host.

The PTY child is a normal authenticated getty. Only replies to this process's
random OSC nonce are interpreted; all other input is forwarded unchanged.
"""
import fcntl
import os
import re
import secrets
import select
import signal
import struct
import subprocess
import sys
import termios
import time
import tty


class ResizeParser:
    def __init__(self, nonce, resize):
        self.prefix = ("\x1b]777;nkos-size;1;" + nonce + ";").encode()
        self.pending = b""
        self.resize = resize

    def feed(self, data):
        data = self.pending + data
        self.pending = b""
        output = bytearray()
        while data:
            if data.startswith(self.prefix):
                end = data.find(b"\x07", len(self.prefix))
                if end < 0 and len(data) < len(self.prefix) + 24:
                    self.pending = data
                    break
                if end >= 0:
                    match = re.fullmatch(rb"([0-9]{1,4});([0-9]{1,4})", data[len(self.prefix):end])
                    if match:
                        rows, cols = map(int, match.groups())
                        if 2 <= rows <= 1000 and 2 <= cols <= 2000:
                            self.resize(rows, cols)
                            data = data[end + 1:]
                            continue
            if self.prefix.startswith(data):
                self.pending = data
                break
            output.append(data[0])
            data = data[1:]
        return bytes(output)

    def flush(self):
        data, self.pending = self.pending, b""
        return data


def main():
    if len(sys.argv) != 2:
        sys.exit("Usage: nanokvm-serial-bridge.py /dev/ttyACM0")
    serial = os.open(sys.argv[1], os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
    old = termios.tcgetattr(serial)
    tty.setraw(serial)
    attrs = termios.tcgetattr(serial)
    attrs[4] = attrs[5] = termios.B9600
    attrs[2] |= termios.CLOCAL | termios.CREAD
    termios.tcsetattr(serial, termios.TCSANOW, attrs)
    master, slave = os.openpty()
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    os.set_blocking(master, False)
    child = subprocess.Popen(
        ["/sbin/agetty", "-o", "-- \\u", "--noreset", "--noclear", "-L",
         "9600", os.ttyname(slave).removeprefix("/dev/"), "xterm-256color"],
        start_new_session=True, stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    nonce = secrets.token_hex(16)
    query = ("\x1b]777;nkos-resize;1;" + nonce + "\x07").encode()
    parser = ResizeParser(nonce, lambda r, c: fcntl.ioctl(
        master, termios.TIOCSWINSZ, struct.pack("HHHH", r, c, 0, 0)))
    queues = {serial: bytearray(), master: bytearray()}
    next_query = 0
    last_output = 0
    last_input = time.monotonic()
    def stop(_sig, _frame):
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        while child.poll() is None:
            now = time.monotonic()
            # Repeat negotiation so a newly opened browser can join an existing login.
            if now >= next_query and not queues[serial] and now - last_output > 0.15:
                queues[serial].extend(query)
                next_query = now + 2
            if parser.pending and now - last_input > 0.5:
                queues[master].extend(parser.flush())
            readable, writable, _ = select.select(
                [fd for fd in (serial, master) if len(queues[master if fd == serial else serial]) < 65536],
                [fd for fd in (serial, master) if queues[fd]], [], 0.1)
            for fd in readable:
                try:
                    data = os.read(fd, 8192)
                except BlockingIOError:
                    continue
                if not data:
                    return
                if fd == serial:
                    last_input = time.monotonic()
                    data = parser.feed(data)
                else:
                    last_output = time.monotonic()
                queues[master if fd == serial else serial].extend(data)
            for fd in writable:
                try:
                    sent = os.write(fd, queues[fd])
                    del queues[fd][:sent]
                except BlockingIOError:
                    pass
    finally:
        # systemd also kills the whole unit cgroup, including the login session.
        if child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=2)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
        try:
            termios.tcsetattr(serial, termios.TCSANOW, old)
        except termios.error:
            pass
        for fd in (serial, master, slave):
            os.close(fd)


if __name__ == "__main__":
    main()
