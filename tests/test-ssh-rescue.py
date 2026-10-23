#!/usr/bin/env python3
"""nanokvm-rescue starts SSH only when SSH is off and the server never listens."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SERVICE = ROOT / 'firmware/alpine/openrc/nanokvm-rescue'


class RescueTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.calls = self.root / 'calls'
        for name in ('rc-service', 'ssh-keygen', 'logger'):
            f = self.bin / name
            f.write_text(f'#!/bin/sh\necho "{name} $*" >> "$CALLS"\n')
            f.chmod(0o755)
        # The application's checks, as nanokvm-app defines them; LISTENING
        # says after how many checks the server listens (never when unset).
        self.app = self.root / 'nanokvm-app'
        self.app.write_text('server_ports() { printf "0050 01BB\\n"; }\n'
                            'server_listening() {\n'
                            '    n=$(cat "$COUNT" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$COUNT"\n'
                            '    if [ -n "${EVERY:-}" ]; then [ $((n % EVERY)) -eq 0 ]; return; fi\n'
                            '    [ -n "${LISTENING:-}" ] && [ "$n" -ge "$LISTENING" ]\n'
                            '}\n')
        self.stop = self.root / 'ssh_stop'
        self.stop.touch()
        self.marker = self.root / 'run/nanokvm/ssh-rescue'

    def run_service(self, function, **env):
        script = ('ebegin() { :; }; eend() { return "$1"; }; sleep() { :; }; '
                  '. "$SERVICE"; ' + function)
        environment = {**os.environ, 'SERVICE': str(SERVICE), 'CALLS': str(self.calls),
                       'COUNT': str(self.root / 'count'), 'PATH': f'{self.bin}:/usr/bin:/bin',
                       'NANOKVM_APP_SERVICE': str(self.app), 'NANOKVM_SSH_STOP': str(self.stop),
                       'NANOKVM_RESCUE_MARKER': str(self.marker), 'NANOKVM_RESCUE_WAIT': '5',
                       'NANOKVM_KMSG': str(self.root / 'kmsg'), **env}
        return subprocess.run(['sh', '-c', script], capture_output=True, text=True, env=environment)

    def calls_made(self):
        return self.calls.read_text().splitlines() if self.calls.exists() else []

    def test_a_server_that_listens_needs_no_rescue(self):
        result = self.run_service('rescue', LISTENING='3', NANOKVM_RESCUE_WAIT='20')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [])
        self.assertFalse(self.marker.exists())
        # Up from the third check, stable for five.
        self.assertEqual((self.root / 'count').read_text().strip(), '7')

    def test_a_server_that_keeps_restarting_gets_ssh(self):
        # Listening on every third check only, as under a crash loop.
        result = self.run_service('rescue', EVERY='3', NANOKVM_RESCUE_WAIT='20')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())

    def test_a_server_that_never_listens_gets_ssh(self):
        result = self.run_service('rescue')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls_made()
        self.assertTrue(calls[0].startswith('logger -p daemon.warning -t nanokvm-rescue'), calls)
        self.assertEqual(calls[1:], ['ssh-keygen -A', 'rc-service sshd start'])
        self.assertTrue(self.marker.exists())
        self.assertIn('nanokvm-rescue: The NanoKVM server did not start', (self.root / 'kmsg').read_text())
        # Every second of the wait was a check.
        self.assertEqual((self.root / 'count').read_text().strip(), '5')

    def test_ssh_that_is_on_needs_no_rescue(self):
        self.stop.unlink()
        result = self.run_service('start; wait')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls_made(), [])
        self.assertFalse(self.marker.exists())

    def test_start_does_not_hold_the_boot(self):
        result = self.run_service('start; wait', NANOKVM_RESCUE_WAIT='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('rc-service sshd start', self.calls_made())

    def test_it_is_in_the_default_runlevel_on_install_and_update(self):
        base = ROOT / 'firmware/alpine/packages/nanokvm-base'
        self.assertIn('nanokvm-rescue', (base / 'nanokvm-base.post-install').read_text())
        self.assertIn('rc-update add nanokvm-rescue default', (base / 'nanokvm-base.post-upgrade').read_text())
        self.assertRegex((ROOT / 'platform/packages.list').read_text(),
                         r'(?m)^base +file +/etc/init\.d/nanokvm-rescue +0755 +firmware/alpine/openrc/nanokvm-rescue$')


if __name__ == '__main__':
    unittest.main()
